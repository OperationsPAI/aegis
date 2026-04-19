import abc
from typing import Any, Literal

from sqlalchemy.orm import defer
from sqlmodel import select

from ...config import EvalConfig
from ...db import DatasetSample, EvaluationSample
from ...utils import SQLModelUtils, get_logger

logger = get_logger(__name__)


class BaseDataManager(abc.ABC):
    """Base data manager for loading and saving data."""

    data: list[EvaluationSample]

    def __init__(self, config: EvalConfig) -> None:
        self.config = config

    @abc.abstractmethod
    def load(self) -> list[EvaluationSample]:
        """Load the dataset."""
        raise NotImplementedError

    @abc.abstractmethod
    def save(self, **kwargs: Any) -> None:
        """Save the dataset."""
        raise NotImplementedError

    @abc.abstractmethod
    def get_samples(self, stage: Literal["init", "rollout", "judged"] | None = None) -> list[EvaluationSample]:
        """Get samples of specified stage from the dataset."""
        raise NotImplementedError


class DBDataManager(BaseDataManager):
    """Database data manager for loading and saving data."""

    def __init__(self, config: EvalConfig) -> None:
        self.config = config

    def load(self) -> list[EvaluationSample]:
        assert self.config.data is not None
        agent_type = self.config.agent_type
        model_name = self.config.model_name

        # Load all DatasetSamples from source data table
        with SQLModelUtils.create_session() as session:
            datapoints = session.exec(
                select(DatasetSample).where(DatasetSample.dataset == self.config.data.dataset)
            ).all()
            logger.info(f"Loaded {len(datapoints)} samples from {self.config.data.dataset}.")

        # Filter by tags if specified (OR logic)
        filter_tags = self.config.data.tags
        if filter_tags:
            datapoints = [dp for dp in datapoints if dp.tags and any(tag in dp.tags for tag in filter_tags)]
            logger.info(f"Filtered to {len(datapoints)} samples with tags: {filter_tags}")

        # Get existing dataset_index values for this exp_id + agent_type + model_name
        # Only query the dataset_index column to avoid loading heavy fields like trajectories
        existing_indices: set[int] = set()
        if self._check_exp_id():
            existing_indices = self._get_existing_indices()
            logger.info(
                f"exp_id {self.config.exp_id} already exists with {len(existing_indices)} samples. "
                f"Checking for new samples to add..."
            )

        # Create EvaluationSamples only for new datapoints
        new_samples = []
        for dp in datapoints:
            if dp.index in existing_indices:
                continue  # Skip already existing samples
            sample = EvaluationSample(
                dataset=dp.dataset,
                dataset_index=dp.index,
                source=dp.source,
                raw_question=dp.question,
                level=dp.level,
                correct_answer=dp.answer,
                file_name=dp.file_name,
                meta=dp.meta,
                exp_id=self.config.exp_id,
                agent_type=agent_type,
                model_name=model_name,
            )  # type: ignore[call-arg]
            new_samples.append(sample)

        if new_samples:
            logger.info(f"Adding {len(new_samples)} new samples to evaluation_data.")
            self.save(new_samples)
        else:
            logger.info("No new samples to add.")

        # Return all samples for this exp (filtered by agent_type, model_name, and tags)
        # Exclude trajectories during load — they are only needed during judge phase
        self.data = self.get_samples(
            agent_type=agent_type, model_name=model_name, tags=filter_tags, exclude_trajectories=True
        )
        return self.data

    def get_samples(
        self,
        stage: Literal["init", "rollout", "judged"] | None = None,
        limit: int | None = None,
        agent_type: str | None = None,
        model_name: str | None = None,
        tags: list[str] | None = None,
        exclude_trajectories: bool = False,
    ) -> list[EvaluationSample]:
        """Get samples from exp_id with specified stage and optional agent_type/model_name filter.

        Args:
            exclude_trajectories: If True, defer loading of the ``trajectories``
                column to avoid transferring large JSON payloads from the DB.
                The attribute is set to ``None`` on the returned objects so that
                downstream code can safely check it without triggering a lazy load.
        """
        with SQLModelUtils.create_session() as session:
            stmt = select(EvaluationSample).where(EvaluationSample.exp_id == self.config.exp_id)
            if exclude_trajectories:
                stmt = stmt.options(defer(EvaluationSample.trajectories))  # type: ignore[arg-type]
            if stage:
                stmt = stmt.where(EvaluationSample.stage == stage)
            if agent_type is not None:
                stmt = stmt.where(EvaluationSample.agent_type == agent_type)
            if model_name is not None:
                stmt = stmt.where(EvaluationSample.model_name == model_name)
            stmt = stmt.order_by(EvaluationSample.dataset_index).limit(limit)  # type: ignore[arg-type]
            samples = list(session.exec(stmt).all())

            # Neutralise deferred trajectories before the session closes so that
            # accessing .trajectories on a detached object returns None instead
            # of raising DetachedInstanceError.
            if exclude_trajectories:
                for s in samples:
                    s.trajectories = None

            # Filter by tags if specified (OR logic - query tags from DatasetSample)
            if tags and samples:
                # Get all DatasetSamples for this dataset to retrieve tags
                dataset_name = samples[0].dataset
                dataset_samples = session.exec(select(DatasetSample).where(DatasetSample.dataset == dataset_name)).all()

                # Create mapping: dataset_index -> tags
                tags_map = {ds.index: ds.tags for ds in dataset_samples}

                # Filter samples by tags
                samples = [
                    s
                    for s in samples
                    if s.dataset_index in tags_map
                    and tags_map[s.dataset_index] is not None
                    and any(tag in tags_map[s.dataset_index] for tag in tags)  # type: ignore[operator]
                ]

            return samples

    def save(self, samples: list[EvaluationSample] | EvaluationSample | None = None, **kwargs: Any) -> None:
        """Update or add sample(s) to db.

        After commit, all objects are refreshed so that their attributes
        remain accessible even after the session closes. Without this,
        accessing lazy-loaded or expired attributes on the detached
        objects raises ``DetachedInstanceError``.
        """
        if samples is None:
            return
        if isinstance(samples, list):
            with SQLModelUtils.create_session() as session:
                session.add_all(samples)
                session.commit()
                for s in samples:
                    session.refresh(s)
        else:
            with SQLModelUtils.create_session() as session:
                session.add(samples)
                session.commit()
                session.refresh(samples)

    def delete_samples(self, samples: list[EvaluationSample] | EvaluationSample) -> None:
        """Delete sample(s) from db."""
        if isinstance(samples, list):
            with SQLModelUtils.create_session() as session:
                for sample in samples:
                    session.delete(sample)
                session.commit()
        else:
            with SQLModelUtils.create_session() as session:
                session.delete(samples)
                session.commit()

    def load_with_trajectories(self, sample: EvaluationSample) -> EvaluationSample:
        """Reload a single sample from DB with its trajectories column.

        Used when trajectories were deferred during a bulk query to avoid
        loading large JSON payloads for all rows at once.

        The returned object is refreshed before the session closes so
        that all attributes are eagerly loaded and accessible after detach.
        """
        with SQLModelUtils.create_session() as session:
            full = session.get(EvaluationSample, sample.id)
            if full is not None:
                session.refresh(full)
                return full
        return sample

    def _get_existing_indices(self) -> set[int]:
        """Return the set of dataset_index values already stored for this exp_id.

        Only queries the ``dataset_index`` column to avoid transferring heavy
        payload columns like ``trajectories``.
        """
        with SQLModelUtils.create_session() as session:
            stmt = select(EvaluationSample.dataset_index).where(
                EvaluationSample.exp_id == self.config.exp_id
            )
            return {idx for idx in session.exec(stmt).all() if idx is not None}

    def _check_exp_id(self) -> bool:
        """Check if any record has the same exp_id, agent_type, and model_name."""
        from sqlmodel import func

        agent_type = self.config.agent_type
        model_name = self.config.model_name
        with SQLModelUtils.create_session() as session:
            stmt = select(func.count()).select_from(EvaluationSample).where(  # type: ignore[call-overload]
                EvaluationSample.exp_id == self.config.exp_id
            )
            if agent_type is not None:
                stmt = stmt.where(EvaluationSample.agent_type == agent_type)
            if model_name is not None:
                stmt = stmt.where(EvaluationSample.model_name == model_name)
            count = session.exec(stmt).one()
        return count > 0
