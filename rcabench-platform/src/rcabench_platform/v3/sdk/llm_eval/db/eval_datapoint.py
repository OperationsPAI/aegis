import datetime
from typing import Any, ClassVar

from sqlalchemy import UniqueConstraint
from sqlmodel import JSON, Column, Field, SQLModel

from .base_model import EvalBaseModel


class DatasetSample(SQLModel, table=True):
    __tablename__: ClassVar[str] = "data"  # type: ignore[assignment]
    __table_args__ = (UniqueConstraint("dataset", "source", name="uq_dataset_source"),)

    id: int | None = Field(default=None, primary_key=True)
    dataset: str = ""  # dataset name, for exp
    index: int | None = Field(default=None)  # The index of the datapoint in the dataset, starting from 1
    source: str = ""  # dataset name for mixed dataset
    source_index: int | None = Field(default=None)  # The index of the datapoint in the source dataset, if available

    question: str = ""
    answer: str | None = ""
    topic: str | None = ""
    level: int | None = 0  # hardness level of the question, if applicable
    file_name: str | None = ""  # for file attachments if applicable

    meta: Any | None = Field(default=None, sa_column=Column(JSON))  # additional metadata for the dataset
    tags: list[str] | None = Field(default=None, sa_column=Column(JSON))  # tags for filtering samples


class EvaluationSample(EvalBaseModel, SQLModel, table=True):
    __tablename__: ClassVar[str] = "evaluation_data"  # type: ignore[assignment]

    id: int | None = Field(default=None, primary_key=True)
    created_at: datetime.datetime | None = Field(default_factory=datetime.datetime.now)
    updated_at: datetime.datetime | None = Field(default_factory=datetime.datetime.now)

    # 1) base info
    dataset: str = ""  # dataset name
    dataset_index: int | None = Field(default=None)
    source: str = ""
    raw_question: str = ""
    level: int | None = 0  # hardness level of the question, if applicable
    augmented_question: str | None = ""
    correct_answer: str | None = ""
    file_name: str | None = ""  # for file attachments if applicable
    meta: Any | None = Field(default=None, sa_column=Column(JSON))
    # 2) rollout
    trace_id: str | None = Field(default=None)
    trace_url: str | None = Field(default=None)
    response: str | None = Field(default=None)
    time_cost: float | None = Field(default=None)  # time cost in seconds
    trajectories: Any | None = Field(default=None, sa_column=Column(JSON))
    # 3) judgement
    extracted_final_answer: str | None = Field(default=None)
    judged_response: str | None = Field(default=None)
    reasoning: str | None = Field(default=None)
    correct: bool | None = Field(default=None)
    confidence: float | None = Field(default=None)
    # id
    exp_id: str = Field(default="default", index=True)
    agent_type: str | None = Field(default=None, index=True)  # agent type: simple, orchestra, orchestrator, etc.
    model_name: str | None = Field(default=None, index=True)  # LLM model name for differentiation
    stage: str = Field(default="init", index=True)  # Literal["init", "rollout", "judged]

    def model_dump(self, *args, **kwargs):
        keys = [
            "exp_id",
            "agent_type",
            "model_name",
            "dataset",
            "dataset_index",
            "source",
            "level",
            "raw_question",
            "correct_answer",
            "file_name",
            "stage",
            "trace_id",
            "response",
            "time_cost",
            "trajectories",
            "judged_response",
            "correct",
            "confidence",
        ]
        return {k: getattr(self, k) for k in keys if getattr(self, k) is not None}
