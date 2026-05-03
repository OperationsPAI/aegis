"""DuckDB on parquet — verify each evidence SQL is executable on the case dir.

Every ``*.parquet`` file in the case dir is mounted as a same-named view (the
file ``abnormal_traces.parquet`` becomes view ``abnormal_traces``) so the agent
can use either bare names (``FROM abnormal_traces``) or explicit
``read_parquet(...)`` calls. The SQL itself is run as-is on a fresh in-memory
DuckDB connection — there is no keyword whitelist, no path-prefix sandbox, no
time-window or service-name post-filter. The only outcomes are:

    OK         — the query executed and returned at least one row
    EMPTY      — the query executed and returned zero rows
    SQL_ERROR  — DuckDB raised on parse/exec

Coherence checks (does the SQL actually back the claim, are the rows in the
incident window, etc.) are the chain-coherence judge's job, not the
verifier's — the verifier only certifies that the SQL is *runnable*.
"""

from __future__ import annotations

import os
from enum import Enum
from pathlib import Path
from typing import Any

from pydantic import BaseModel, Field

from .schema import Evidence, EvidenceKind


class EvidenceStatus(str, Enum):
    OK = "OK"
    EMPTY = "EMPTY"
    SQL_ERROR = "SQL_ERROR"


class EvidenceVerifyResult(BaseModel):
    status: EvidenceStatus
    row_count: int = 0
    error: str | None = None
    sample_rows: list[dict[str, Any]] = Field(default_factory=list)
    columns: list[str] = Field(default_factory=list)


def _mount_views(con: Any, parquet_dir: Path) -> None:
    """Create one view per *.parquet in the case dir, named after the file stem.

    Identifiers are quoted so unusual file names don't break the CREATE.
    """
    for path in parquet_dir.glob("*.parquet"):
        view_name = path.stem.replace('"', '""')
        path_lit = str(path).replace("'", "''")
        con.execute(f"CREATE VIEW \"{view_name}\" AS SELECT * FROM read_parquet('{path_lit}')")


def verify_evidence(
    evidence: Evidence,
    parquet_dir: Path,
    timeout_seconds: float = 5.0,
    sample_limit: int = 50,
    **_: Any,  # accept and ignore legacy kwargs (start_time_ns, allowed_services, ...)
) -> EvidenceVerifyResult:
    if not evidence.sql or not evidence.sql.strip():
        return EvidenceVerifyResult(status=EvidenceStatus.SQL_ERROR, error="empty SQL")

    try:
        import duckdb
    except ImportError as exc:
        return EvidenceVerifyResult(status=EvidenceStatus.SQL_ERROR, error=f"duckdb unavailable: {exc}")

    con = duckdb.connect(database=":memory:")
    cwd = os.getcwd()
    try:
        try:
            con.execute(f"SET statement_timeout = '{int(timeout_seconds * 1000)}ms'")
        except Exception:
            pass

        try:
            _mount_views(con, parquet_dir)
        except Exception as exc:
            return EvidenceVerifyResult(status=EvidenceStatus.SQL_ERROR, error=f"view mount failed: {exc}")

        # Resolve relative read_parquet('foo.parquet') paths against the case dir.
        try:
            os.chdir(parquet_dir)
        except Exception:
            pass

        try:
            cursor = con.execute(evidence.sql)
            columns = [d[0] for d in (cursor.description or [])]
            raw_rows = cursor.fetchmany(sample_limit)
        except Exception as exc:
            return EvidenceVerifyResult(status=EvidenceStatus.SQL_ERROR, error=str(exc))
    finally:
        try:
            os.chdir(cwd)
        except Exception:
            pass
        con.close()

    sample_rows = [dict(zip(columns, row)) for row in raw_rows]

    if not sample_rows:
        return EvidenceVerifyResult(status=EvidenceStatus.EMPTY, row_count=0, columns=columns)

    return EvidenceVerifyResult(
        status=EvidenceStatus.OK,
        row_count=len(sample_rows),
        sample_rows=sample_rows[:5],
        columns=columns,
    )


_ = EvidenceKind  # re-export indirectly so callers can import from this module
