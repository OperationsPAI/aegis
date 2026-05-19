"""Regression: JSON columns must store SQL NULL, not the text string 'null'.

See issue OperationsPAI/aegis#388. SQLAlchemy's JSON type serialises Python None
as the JSON value `null` (a 4-byte text string) unless `none_as_null=True` is
passed when constructing the column.
"""

from __future__ import annotations

import os
import sqlite3
import tempfile

import pytest

sqlmodel = pytest.importorskip("sqlmodel")


def test_none_writes_sql_null_not_string(monkeypatch):
    db_path = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
    db_path.close()
    monkeypatch.setenv("LLM_EVAL_DB_URL", f"sqlite:///{db_path.name}")

    # Force a fresh engine singleton bound to our temp DB.
    from rcabench_platform.v3.sdk.llm_eval.db.eval_datapoint import (
        DatasetSample,
        EvaluationSample,
    )
    from rcabench_platform.v3.sdk.llm_eval.utils import SQLModelUtils

    SQLModelUtils._engine = None
    with SQLModelUtils.create_session() as sess:
        sess.add(
            EvaluationSample(
                exp_id="t",
                dataset="d",
                dataset_index=1,
                eval_metrics=None,
                meta=None,
                trajectories=None,
            )
        )
        sess.add(DatasetSample(dataset="d", index=1, source="d", meta=None, tags=None))
        sess.commit()

    SQLModelUtils._engine = None

    c = sqlite3.connect(db_path.name)
    try:
        eval_row = c.execute(
            "SELECT eval_metrics IS NULL, meta IS NULL, trajectories IS NULL,"
            " typeof(eval_metrics), typeof(meta), typeof(trajectories)"
            " FROM evaluation_data WHERE exp_id='t'"
        ).fetchone()
        assert eval_row == (1, 1, 1, "null", "null", "null"), eval_row

        data_row = c.execute(
            "SELECT meta IS NULL, tags IS NULL, typeof(meta), typeof(tags) FROM data"
        ).fetchone()
        assert data_row == (1, 1, "null", "null"), data_row
    finally:
        c.close()
        os.unlink(db_path.name)


def test_migration_repairs_legacy_null_strings(monkeypatch):
    db_path = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
    db_path.close()
    monkeypatch.setenv("LLM_EVAL_DB_URL", f"sqlite:///{db_path.name}")

    # Hand-craft a DB that looks like a legacy one: text 'null' in JSON cells.
    c = sqlite3.connect(db_path.name)
    c.executescript(
        """
        CREATE TABLE data (id INTEGER PRIMARY KEY, dataset TEXT, "index" INTEGER,
            source TEXT, source_index INTEGER, question TEXT, answer TEXT,
            topic TEXT, level INTEGER, file_name TEXT, meta JSON, tags JSON);
        INSERT INTO data (dataset, meta, tags) VALUES ('d', 'null', 'null');
        CREATE TABLE evaluation_data (id INTEGER PRIMARY KEY, exp_id TEXT,
            dataset TEXT, dataset_index INTEGER, meta JSON, trajectories JSON,
            eval_metrics JSON, tags JSON, stage TEXT);
        INSERT INTO evaluation_data (exp_id, meta, trajectories, eval_metrics, tags)
            VALUES ('legacy', 'null', 'null', 'null', 'null');
        """
    )
    c.commit()
    c.close()

    from rcabench_platform.v3.sdk.llm_eval.utils import SQLModelUtils

    SQLModelUtils._engine = None
    SQLModelUtils.get_engine()  # triggers _init_db_schema → _run_migrations
    SQLModelUtils._engine = None

    c = sqlite3.connect(db_path.name)
    try:
        row = c.execute(
            "SELECT meta IS NULL, trajectories IS NULL, eval_metrics IS NULL, tags IS NULL"
            " FROM evaluation_data WHERE exp_id='legacy'"
        ).fetchone()
        assert row == (1, 1, 1, 1), row
        row = c.execute(
            "SELECT meta IS NULL, tags IS NULL FROM data WHERE dataset='d'"
        ).fetchone()
        assert row == (1, 1), row
    finally:
        c.close()
        os.unlink(db_path.name)
