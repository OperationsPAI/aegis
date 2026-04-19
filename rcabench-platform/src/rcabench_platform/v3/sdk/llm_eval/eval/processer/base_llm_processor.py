import re

from openai import AsyncOpenAI

from ...config import EvalConfig
from ...utils import get_logger
from ..data import EvaluationSample
from .base_processor import BaseProcesser
from .prompts import AUGMENTATION_PROMPTS, JUDGE_PROMPTS
from .utils import MetricsUtils

logger = get_logger(__name__)


JUDGE_PROMPT_MAP = {
    "default": JUDGE_PROMPTS["default"],
    "RCABench": JUDGE_PROMPTS["default"],
}


class BaseLLMJudgeProcesser(BaseProcesser):
    """Base class for processers that use LLM for judging."""

    name = "default"

    def __init__(self, config: EvalConfig) -> None:
        super().__init__(config)
        self._judge_client: AsyncOpenAI | None = None
        provider_config = config.judge_model.model_provider.model_dump()
        self._judge_provider_config = provider_config
        self.judge_model = str(provider_config.get("model") or "gpt-4o-mini")

    @property
    def judge_client(self) -> AsyncOpenAI:
        """Lazily initialize the OpenAI client (only when LLM judging is needed)."""
        if self._judge_client is None:
            self._judge_client = AsyncOpenAI(
                base_url=self._judge_provider_config.get("base_url") or None,
                api_key=self._judge_provider_config.get("api_key") or None,
            )
        return self._judge_client

    def preprocess_one(self, sample: EvaluationSample) -> EvaluationSample:
        """Preprocess a single sample."""
        question = sample.raw_question
        name = self.name or "default"
        template = AUGMENTATION_PROMPTS.get(name, AUGMENTATION_PROMPTS["default"])
        augmented_question = template.format(question=question)
        sample.update(
            augmented_question=augmented_question,
        )
        return sample

    async def judge_one(self, sample: EvaluationSample) -> EvaluationSample:
        """Judge a single sample."""
        data = sample
        question = data.raw_question
        response = data.response or ""
        correct_answer = data.correct_answer or "unknown"

        if correct_answer == "unknown":
            # if correct answer is unknown, we cannot judge
            data.update(judged_response="invalid", correct=False, reward=0.0)
            return data

        # if exact match, return directly(maybe extract exact answer from response first)
        if self._extract_exact_answer(response) == correct_answer:
            data.update(judged_response="Exact match", correct=True, reward=1.0)
            return data

        messages = self._get_judge_messages(question=question or "", response=response, correct_answer=correct_answer)
        assert self.config is not None
        result = await self.judge_client.chat.completions.create(
            model=self.judge_model,
            messages=messages,
            **(self.config.judge_model.model_params.model_dump()),
        )
        content = result.choices[0].message.content or ""
        parsed_content = self._parse_judge_response(content)

        data.judged_response = content
        # update the return data with parsed content
        data.update(**parsed_content)
        return data

    def calculate_metrics(self, samples: list[EvaluationSample]) -> dict:
        """Caculate metrics from the judged data."""
        pass_k = self.config.pass_k if self.config else 1
        return {
            **MetricsUtils.calculate_pass_at_k_metrics(samples, k=pass_k),
            **MetricsUtils.calculate_level_pass_at_k_metrics(samples, k=pass_k),
        }

    def _get_judge_messages(self, question: str, response: str, correct_answer: str) -> list:
        if self.name not in JUDGE_PROMPT_MAP:
            logger.warning(f"Judge prompt for {self.name} is not implemented! Using default judge prompt.")
        name = self.name or "default"
        template = JUDGE_PROMPT_MAP.get(name, JUDGE_PROMPT_MAP["default"])
        input = template.format(question=question, response=response, correct_answer=correct_answer)
        return [{"role": "system", "content": "You are a helpful assistant."}, {"role": "user", "content": input}]

    def _parse_judge_response(self, response: str) -> dict:
        """Parse the judge response into a structured format."""
        pattern = re.compile(
            r"(?=.*?extracted_final_answer:\s*(?P<extracted_final_answer>.*?)(?=\n\s*\w+:|$))?"
            r"(?=.*?reasoning:\s*(?P<reasoning>.*?)(?=\n\s*\w+:|$))?"
            r"(?=.*?correct:\s*(?P<correct>.*?)(?=\n\s*\w+:|$))?"
            r"(?=.*?confidence:\s*(?P<confidence>\d+)\s*%?(?=\n\s*\w+:|$))?",
            re.DOTALL,
        )
        # remove the bold formatting
        response = response.replace("**", "")
        # search for the pattern in the response
        match = pattern.search(response)
        if not match:
            raise ValueError("Invalid judge response format.")

        return {
            "extracted_final_answer": match.group("extracted_final_answer").strip()
            if match.group("extracted_final_answer")
            else "",
            "reasoning": match.group("reasoning").strip() if match.group("reasoning") else "",
            "correct": match.group("correct").strip().lower() == "yes" if match.group("correct") else False,
            "confidence": int(match.group("confidence")) if match.group("confidence") else None,
        }

    def _extract_exact_answer(self, response: str) -> str:
        """Extract the exact answer from the response."""
        return response.strip() if response else ""
