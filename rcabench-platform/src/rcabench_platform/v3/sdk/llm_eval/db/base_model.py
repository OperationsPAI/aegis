from typing import Any

from pydantic import BaseModel


class EvalBaseModel(BaseModel):
    def update(self, **kwargs: Any) -> None:
        """
        Update the evaluation sample with the given keyword arguments.
        """
        for key, value in kwargs.items():
            if hasattr(self, key):
                setattr(self, key, value)

    def get(self, key: str, default: Any = None) -> Any:
        """
        Get the value of the specified key, or return default if not found.
        """
        return getattr(self, key, default)

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "EvalBaseModel":
        """
        Create an EvaluationSample from a dictionary.
        """
        return cls(**data)

    def as_dict(self) -> dict[str, Any]:
        # only contain fields that are not None
        return {k: v for k, v in self.model_dump().items() if v is not None}
