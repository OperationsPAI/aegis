"""Public SDK surface for uploading and reading per-case evaluation results.

Designed for two writers:

  - the SDK's own evaluator pipeline (`eval/processer/*`), which writes the full
    `EvaluationResultV2.model_dump()` into ``eval_metrics`` after each judge run.
  - downstream agents that run rollout/eval in environments without LLM-API
    access; they partial-UPSERT only the axes they can compute.

UPSERT keys on the logical identity ``(exp_id, dataset, dataset_index,
model_name, agent_type)`` and is implemented as SELECT-then-update-or-insert
in Python so SQLite and Postgres behave identically (no ``ON CONFLICT``
dialect, no UNIQUE constraint required). ``eval_metrics`` and ``meta`` are
shallow dict-merged on update; scalar fields follow "pass-to-update,
None-to-keep".

Aggregation runs in Python over the row set so no SQL JSON-path syntax
leaks into the SDK — consumers that want a SQL view can build one in their
own dialect outside the SDK.
"""

from __future__ import annotations

import datetime
from collections.abc import Iterable
from typing import Any

from sqlmodel import Session, select

from .db.eval_datapoint import EvaluationRolloutStats, EvaluationSample
from .utils import SQLModelUtils, get_logger

logger = get_logger(__name__)


_IDENTITY_FIELDS = ("exp_id", "dataset", "dataset_index", "model_name", "agent_type")
_ROLLOUT_STATS_FIELDS = (
    "input_tokens",
    "output_tokens",
    "cache_hit_tokens",
    "cache_write_tokens",
    "n_llm_calls",
)


def upload_case_result(
    *,
    # identity (composite logical key)
    exp_id: str,
    dataset: str,
    dataset_index: int,
    model_name: str | None,
    agent_type: str | None = None,
    # rollout
    response: str | None = None,
    trace_id: str | None = None,
    trace_url: str | None = None,
    time_cost: float | None = None,
    trajectories: Any | None = None,
    # judgement
    eval_metrics: dict[str, Any] | None = None,
    correct: bool | None = None,
    confidence: float | None = None,
    judged_response: str | None = None,
    reasoning: str | None = None,
    extracted_final_answer: str | None = None,
    # rollout stats (sibling table; same partial-update semantics)
    rollout_stats: dict[str, Any] | None = None,
    # base info / meta
    raw_question: str | None = None,
    source: str | None = None,
    level: int | None = None,
    correct_answer: str | None = None,
    augmented_question: str | None = None,
    file_name: str | None = None,
    meta: dict[str, Any] | None = None,
    stage: str | None = None,
    # session control
    session: Session | None = None,
) -> int:
    """UPSERT one EvaluationSample row.

    Returns the row id (auto-PK).

    ``eval_metrics`` and ``meta`` are shallow-merged when updating an existing
    row (new keys overwrite, existing keys are preserved). Scalar fields are
    only updated when the caller passes a non-None value, so partial uploads
    don't clobber state set by another writer.

    ``rollout_stats`` accepts a dict subset of {input_tokens, output_tokens,
    cache_hit_tokens, cache_write_tokens, n_llm_calls}; populates / updates
    the sibling ``evaluation_rollout_stats`` row keyed on the same id.

    If ``session`` is provided, the caller owns transaction lifecycle.
    Otherwise this opens a session and commits on success.
    """
    own_session = session is None
    sess = session if session is not None else SQLModelUtils.create_session()
    try:
        existing = _find_existing(
            sess,
            exp_id=exp_id,
            dataset=dataset,
            dataset_index=dataset_index,
            model_name=model_name,
            agent_type=agent_type,
        )

        scalar_updates = {
            "response": response,
            "trace_id": trace_id,
            "trace_url": trace_url,
            "time_cost": time_cost,
            "trajectories": trajectories,
            "correct": correct,
            "confidence": confidence,
            "judged_response": judged_response,
            "reasoning": reasoning,
            "extracted_final_answer": extracted_final_answer,
            "raw_question": raw_question,
            "source": source,
            "level": level,
            "correct_answer": correct_answer,
            "augmented_question": augmented_question,
            "file_name": file_name,
            "stage": stage,
        }

        if existing is None:
            row = EvaluationSample(
                exp_id=exp_id,
                dataset=dataset,
                dataset_index=dataset_index,
                model_name=model_name,
                agent_type=agent_type,
                eval_metrics=dict(eval_metrics) if eval_metrics else None,
                meta=dict(meta) if meta else None,
            )
            for k, v in scalar_updates.items():
                if v is not None:
                    setattr(row, k, v)
            sess.add(row)
            sess.flush()  # populate row.id for FK in rollout_stats
        else:
            row = existing
            if eval_metrics:
                row.eval_metrics = _shallow_merge(row.eval_metrics, eval_metrics)
            if meta:
                row.meta = _shallow_merge(row.meta, meta)
            for k, v in scalar_updates.items():
                if v is not None:
                    setattr(row, k, v)
            row.updated_at = datetime.datetime.now()

        if rollout_stats:
            _upsert_rollout_stats(sess, row.id, rollout_stats)

        if own_session:
            sess.commit()
            sess.refresh(row)

        assert row.id is not None
        return row.id
    except Exception:
        if own_session:
            sess.rollback()
        raise
    finally:
        if own_session:
            sess.close()


def upload_case_results(rows: Iterable[dict[str, Any]]) -> list[int]:
    """Batch UPSERT. All rows go in one transaction; failure rolls back the batch."""
    ids: list[int] = []
    with SQLModelUtils.create_session() as sess:
        try:
            for r in rows:
                ids.append(upload_case_result(session=sess, **r))
            sess.commit()
        except Exception:
            sess.rollback()
            raise
    return ids


def list_experiment_cases(
    exp_id: str,
    *,
    model_name: str | None = None,
    agent_type: str | None = None,
    stage: str | None = None,
    session: Session | None = None,
) -> list[EvaluationSample]:
    """Read raw case rows for review tooling."""
    own_session = session is None
    sess = session if session is not None else SQLModelUtils.create_session()
    try:
        q = select(EvaluationSample).where(EvaluationSample.exp_id == exp_id)
        if model_name is not None:
            q = q.where(EvaluationSample.model_name == model_name)
        if agent_type is not None:
            q = q.where(EvaluationSample.agent_type == agent_type)
        if stage is not None:
            q = q.where(EvaluationSample.stage == stage)
        return list(sess.exec(q).all())
    finally:
        if own_session:
            sess.close()


def aggregate_experiment_summary(
    exp_id: str | None = None,
    *,
    session: Session | None = None,
) -> list[dict[str, Any]]:
    """(exp_id × model_name × agent_type) rollup over judged rows.

    Aggregates in Python so the same code works on SQLite and Postgres
    without SQL JSON-path syntax. For each numeric key present in
    ``eval_metrics`` (booleans count as 0/1) returns ``avg_<key>`` over rows
    where the key was non-null. Token / call counts from
    ``evaluation_rollout_stats`` are summed under ``total_<field>``.
    """
    own_session = session is None
    sess = session if session is not None else SQLModelUtils.create_session()
    try:
        q = select(EvaluationSample).where(EvaluationSample.stage == "judged")
        if exp_id is not None:
            q = q.where(EvaluationSample.exp_id == exp_id)
        rows = list(sess.exec(q).all())

        groups: dict[tuple[str, str | None, str | None], list[EvaluationSample]] = {}
        for r in rows:
            key = (r.exp_id, r.model_name, r.agent_type)
            groups.setdefault(key, []).append(r)

        all_ids = [r.id for r in rows if r.id is not None]
        stats_by_id: dict[int, EvaluationRolloutStats] = {}
        if all_ids:
            stats_q = select(EvaluationRolloutStats).where(EvaluationRolloutStats.id.in_(all_ids))  # type: ignore[attr-defined]
            for s in sess.exec(stats_q).all():
                if s.id is not None:
                    stats_by_id[s.id] = s

        summaries: list[dict[str, Any]] = []
        for (xid, model, agent), members in groups.items():
            summary: dict[str, Any] = {
                "exp_id": xid,
                "model_name": model,
                "agent_type": agent,
                "n_cases": len(members),
            }

            metric_acc: dict[str, list[float]] = {}
            for m in members:
                em = m.eval_metrics
                if not isinstance(em, dict):
                    continue
                for k, v in em.items():
                    if isinstance(v, bool):
                        metric_acc.setdefault(k, []).append(1.0 if v else 0.0)
                    elif isinstance(v, (int, float)):
                        metric_acc.setdefault(k, []).append(float(v))
            for k, vals in metric_acc.items():
                if vals:
                    summary[f"avg_{k}"] = sum(vals) / len(vals)

            for fld in _ROLLOUT_STATS_FIELDS:
                total = 0
                seen = False
                for m in members:
                    s = stats_by_id.get(m.id) if m.id is not None else None
                    if s is None:
                        continue
                    val = getattr(s, fld, None)
                    if val is not None:
                        total += int(val)
                        seen = True
                if seen:
                    summary[f"total_{fld}"] = total

            summaries.append(summary)
        return summaries
    finally:
        if own_session:
            sess.close()


# ── internals ─────────────────────────────────────────────────────────────


def _find_existing(
    sess: Session,
    *,
    exp_id: str,
    dataset: str,
    dataset_index: int,
    model_name: str | None,
    agent_type: str | None,
) -> EvaluationSample | None:
    q = (
        select(EvaluationSample)
        .where(EvaluationSample.exp_id == exp_id)
        .where(EvaluationSample.dataset == dataset)
        .where(EvaluationSample.dataset_index == dataset_index)
    )
    if model_name is None:
        q = q.where(EvaluationSample.model_name.is_(None))  # type: ignore[union-attr]
    else:
        q = q.where(EvaluationSample.model_name == model_name)
    if agent_type is None:
        q = q.where(EvaluationSample.agent_type.is_(None))  # type: ignore[union-attr]
    else:
        q = q.where(EvaluationSample.agent_type == agent_type)
    return sess.exec(q).first()


def _shallow_merge(
    existing: dict[str, Any] | None | Any,
    incoming: dict[str, Any],
) -> dict[str, Any]:
    base: dict[str, Any] = dict(existing) if isinstance(existing, dict) else {}
    base.update(incoming)
    return base


def _upsert_rollout_stats(sess: Session, row_id: int | None, fields: dict[str, Any]) -> None:
    if row_id is None:
        return
    clean = {k: v for k, v in fields.items() if k in _ROLLOUT_STATS_FIELDS and v is not None}
    if not clean:
        return
    existing = sess.get(EvaluationRolloutStats, row_id)
    if existing is None:
        sess.add(EvaluationRolloutStats(id=row_id, **clean))
    else:
        for k, v in clean.items():
            setattr(existing, k, v)
