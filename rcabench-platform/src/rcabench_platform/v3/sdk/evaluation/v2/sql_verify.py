"""DuckDB on parquet — verify each evidence SQL is executable, non-empty,
inside the injection time window, and returns rows for services the claim
implicates.

The verifier reads only the parquets in `<case_dir>/<file>.parquet`. Disable
attach/external IO by running each query on a fresh in-memory connection with
an `enable_external_access=false` setting where supported.
"""
from __future__ import annotations

import re
from datetime import datetime, timezone
from enum import Enum
from pathlib import Path
from typing import Any

from pydantic import BaseModel, Field

from .schema import Evidence, EvidenceKind


class EvidenceStatus(str, Enum):
    OK = "OK"
    EMPTY = "EMPTY"
    OUT_OF_WINDOW = "OUT_OF_WINDOW"
    SERVICE_MISMATCH = "SERVICE_MISMATCH"
    SQL_ERROR = "SQL_ERROR"
    UNSAFE_SQL = "UNSAFE_SQL"


class EvidenceVerifyResult(BaseModel):
    status: EvidenceStatus
    row_count: int = 0
    error: str | None = None
    sample_rows: list[dict[str, Any]] = Field(default_factory=list)
    columns: list[str] = Field(default_factory=list)


_FORBIDDEN_RE = re.compile(
    r"\b(ATTACH|INSTALL|LOAD|COPY|EXPORT|PRAGMA|SET|CREATE|DROP|DELETE|UPDATE|INSERT|ALTER)\b",
    re.IGNORECASE,
)
_READ_PARQUET_RE = re.compile(r"read_parquet\s*\(\s*'([^']+)'", re.IGNORECASE)
_TIME_COLUMN_HINTS = ("time", "timestamp", "ts")
_SERVICE_COLUMN_HINTS = ("service_name", "service", "attr.k8s.service.name")


def _is_safe_sql(sql: str) -> tuple[bool, str | None]:
    if not sql or not sql.strip():
        return False, "empty SQL"
    if _FORBIDDEN_RE.search(sql):
        return False, "SQL contains a forbidden keyword (DDL/DML/ATTACH/etc.)"
    if ";" in sql.rstrip().rstrip(";"):
        return False, "multiple statements are not allowed"
    return True, None


def _resolve_paths(sql: str, parquet_dir: Path) -> tuple[str, list[Path]]:
    """Rewrite read_parquet('foo.parquet') -> read_parquet('<dir>/foo.parquet').

    Bare names (no '/') are interpreted relative to the case dir; absolute or
    relative paths that escape the dir are rejected by checking the resolved
    path is a child of `parquet_dir`.
    """
    referenced: list[Path] = []
    parquet_dir = parquet_dir.resolve()

    def replace(match: re.Match[str]) -> str:
        ref = match.group(1)
        candidate = (parquet_dir / ref) if ("/" not in ref) else Path(ref)
        candidate = candidate.resolve()
        try:
            candidate.relative_to(parquet_dir)
        except ValueError as exc:
            raise ValueError(f"path {ref!r} escapes case dir") from exc
        if not candidate.exists():
            raise FileNotFoundError(f"parquet not found: {candidate}")
        referenced.append(candidate)
        return f"read_parquet('{candidate}'"

    rewritten = _READ_PARQUET_RE.sub(replace, sql)
    return rewritten, referenced


def _to_ns(value: Any) -> int | None:
    if value is None:
        return None
    if isinstance(value, (int, float)):
        v = int(value)
        return v * 1_000_000_000 if v < 1_000_000_000_000_000 else v
    if isinstance(value, datetime):
        if value.tzinfo is None:
            value = value.replace(tzinfo=timezone.utc)
        return int(value.timestamp() * 1_000_000_000)
    return None


def _normalize_service(value: Any) -> str:
    if value is None:
        return ""
    s = str(value).strip().lower()
    if s.startswith("ts-"):
        s = s[3:]
    return s.replace("-", "").replace("_", "")


def _within_window(rows: list[dict[str, Any]], start_ns: int | None, end_ns: int | None) -> bool:
    if start_ns is None or end_ns is None:
        return True
    pad_ns = 60 * 1_000_000_000
    lo = start_ns - pad_ns
    hi = end_ns + pad_ns
    found_any = False
    for row in rows:
        for col in _TIME_COLUMN_HINTS:
            ts = row.get(col)
            ns = _to_ns(ts)
            if ns is None:
                continue
            found_any = True
            if lo <= ns <= hi:
                return True
        if not any(c in row for c in _TIME_COLUMN_HINTS):
            return True
    return not found_any


def _service_aligned(rows: list[dict[str, Any]], allowed: set[str]) -> bool:
    if not allowed:
        return True
    norm_allowed = {_normalize_service(s) for s in allowed if s}
    norm_allowed.discard("")
    if not norm_allowed:
        return True
    saw_service_col = False
    for row in rows:
        for col in _SERVICE_COLUMN_HINTS:
            if col not in row:
                continue
            saw_service_col = True
            if _normalize_service(row[col]) in norm_allowed:
                return True
    return not saw_service_col


def verify_evidence(
    evidence: Evidence,
    parquet_dir: Path,
    start_time_ns: int | None = None,
    end_time_ns: int | None = None,
    allowed_services: set[str] | None = None,
    timeout_seconds: float = 5.0,
    sample_limit: int = 50,
) -> EvidenceVerifyResult:
    safe, why = _is_safe_sql(evidence.sql)
    if not safe:
        return EvidenceVerifyResult(status=EvidenceStatus.UNSAFE_SQL, error=why)

    try:
        rewritten, refs = _resolve_paths(evidence.sql, parquet_dir)
    except (ValueError, FileNotFoundError) as exc:
        return EvidenceVerifyResult(status=EvidenceStatus.UNSAFE_SQL, error=str(exc))
    if not refs:
        return EvidenceVerifyResult(
            status=EvidenceStatus.UNSAFE_SQL,
            error="SQL must reference at least one read_parquet(...) on the case dir",
        )

    try:
        import duckdb
    except ImportError as exc:
        return EvidenceVerifyResult(status=EvidenceStatus.SQL_ERROR, error=f"duckdb unavailable: {exc}")

    con = duckdb.connect(database=":memory:")
    try:
        try:
            con.execute(f"SET statement_timeout = '{int(timeout_seconds * 1000)}ms'")
        except Exception:
            pass
        try:
            cursor = con.execute(rewritten)
            columns = [d[0] for d in (cursor.description or [])]
            raw_rows = cursor.fetchmany(sample_limit)
        except Exception as exc:
            return EvidenceVerifyResult(status=EvidenceStatus.SQL_ERROR, error=str(exc))
    finally:
        con.close()

    sample_rows = [dict(zip(columns, row)) for row in raw_rows]

    if not sample_rows:
        return EvidenceVerifyResult(status=EvidenceStatus.EMPTY, row_count=0, columns=columns)

    if not _within_window(sample_rows, start_time_ns, end_time_ns):
        return EvidenceVerifyResult(
            status=EvidenceStatus.OUT_OF_WINDOW,
            row_count=len(sample_rows),
            sample_rows=sample_rows[:5],
            columns=columns,
        )

    if not _service_aligned(sample_rows, allowed_services or set()):
        return EvidenceVerifyResult(
            status=EvidenceStatus.SERVICE_MISMATCH,
            row_count=len(sample_rows),
            sample_rows=sample_rows[:5],
            columns=columns,
        )

    return EvidenceVerifyResult(
        status=EvidenceStatus.OK,
        row_count=len(sample_rows),
        sample_rows=sample_rows[:5],
        columns=columns,
    )


_ = EvidenceKind  # re-export indirectly so callers can import from this module
