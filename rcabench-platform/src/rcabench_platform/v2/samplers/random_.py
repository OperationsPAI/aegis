"""Random trace sampling algorithm implementation."""

import random
from pathlib import Path

import polars as pl

from .spec import SamplerArgs, SampleResult, SamplingMode, TraceSampler


class RandomSampler(TraceSampler):
    """Random trace sampling algorithm that randomly samples traces."""

    def __init__(self, seed: int | None = None):
        """
        Initialize the random sampler.

        Args:
            seed: Random seed for reproducible results. If None, uses random seed.
        """
        self.seed = seed

    def needs_cpu_count(self) -> int | None:
        return 4

    def __call__(self, args: SamplerArgs) -> list[SampleResult]:
        """
        Execute random trace sampling.

        Args:
            args: Sampler arguments

        Returns:
            List of SampleResult with random scores for traces.

            Online mode: Each trace is independently sampled based on threshold.
            Offline mode: Fixed number of traces selected by highest scores.
        """
        # Set random seed if provided
        if self.seed is not None:
            random.seed(self.seed)

        # Load traces data from both normal and abnormal files
        normal_traces_file = args.input_folder / "normal_traces.parquet"
        abnormal_traces_file = args.input_folder / "abnormal_traces.parquet"

        if not normal_traces_file.exists():
            raise FileNotFoundError(f"Normal traces file not found: {normal_traces_file}")
        if not abnormal_traces_file.exists():
            raise FileNotFoundError(f"Abnormal traces file not found: {abnormal_traces_file}")

        # Read unique trace_ids from both normal and abnormal traces
        normal_traces_lf = pl.scan_parquet(normal_traces_file)
        abnormal_traces_lf = pl.scan_parquet(abnormal_traces_file)

        # Combine and get unique trace_ids
        combined_traces_lf = pl.concat([normal_traces_lf.select("trace_id"), abnormal_traces_lf.select("trace_id")])
        unique_traces = combined_traces_lf.unique().collect()
        trace_ids = unique_traces["trace_id"].to_list()

        # Generate random scores for all traces
        all_results = []
        for trace_id in trace_ids:
            sample_score = random.random()  # Random score between 0.0 and 1.0
            all_results.append(SampleResult(trace_id=trace_id, sample_score=sample_score))

        # Apply sampling mode
        if args.mode == SamplingMode.ONLINE:
            # Online mode: threshold-based sampling
            # Each trace is independently decided whether to sample based on its score vs threshold
            threshold = 1.0 - args.sampling_rate  # Higher sampling_rate = lower threshold
            sampled_results = []
            for result in all_results:
                if result.sample_score > threshold:
                    sampled_results.append(result)
            # Sort by score (higher scores first) for consistency
            sampled_results.sort(key=lambda x: x.sample_score, reverse=True)
            return sampled_results
        elif args.mode == SamplingMode.OFFLINE:
            # Offline mode: strict sampling rate limit
            # Sort by score and take top N traces
            all_results.sort(key=lambda x: x.sample_score, reverse=True)
            total_traces = len(all_results)
            target_count = int(total_traces * args.sampling_rate)
            return all_results[:target_count]
        else:
            raise ValueError(f"Unknown sampling mode: {args.mode}")


def create_random_sampler(seed: int | None = None) -> RandomSampler:
    """Factory function to create a RandomSampler instance."""
    return RandomSampler(seed=seed)
