from typing import Literal

from pydantic import Field

from ..utils import EnvUtils
from .base_config import ConfigBaseModel


class RateLimitConfig(ConfigBaseModel):
    """Configuration for API rate limiting and retry behavior."""

    rpm: int | None = None
    """Requests per minute limit. None means unlimited."""
    tpm: int | None = None
    """Tokens per minute limit. None means unlimited."""
    max_retries: int = 5
    """Maximum number of retry attempts for failed API calls."""
    retry_min_wait: float = 1.0
    """Minimum wait time between retries (seconds)."""
    retry_max_wait: float = 60.0
    """Maximum wait time between retries (seconds)."""
    retry_jitter: float = 1.0
    """Random jitter added to retry wait time (seconds)."""


class ModelProviderConfig(ConfigBaseModel):
    """config for model provider"""

    type: Literal["chat.completions", "responses", "litellm"] = "chat.completions"
    """model type, supported types: chat.completions, responses"""
    model: str | None = EnvUtils.get_env("LLM_EVAL_MODEL", None) or EnvUtils.get_env("UTU_LLM_MODEL", None)
    """model name"""
    base_url: str | None = None
    """model provider base url"""
    api_key: str | None = None
    """model provider api key"""
    api_format: Literal["openai", "anthropic", "google"] = "openai"
    """API format: openai (default), anthropic, or google. Used by LangGraph agent to select appropriate SDK."""


class ModelParamsConfig(ConfigBaseModel):
    """Basic params shared in chat.completions and responses"""

    temperature: float | None = None
    top_p: float | None = None
    parallel_tool_calls: bool | None = None


class ModelConfigs(ConfigBaseModel):
    """Overall model config"""

    model_provider: ModelProviderConfig = Field(default_factory=ModelProviderConfig)
    """config for model provider"""
    model_params: ModelParamsConfig = Field(default_factory=ModelParamsConfig)
    """config for basic model usage, e.g. `query_one` in tools / judger"""
    rate_limit: RateLimitConfig = Field(default_factory=RateLimitConfig)
    """config for rate limiting and retry behavior"""
