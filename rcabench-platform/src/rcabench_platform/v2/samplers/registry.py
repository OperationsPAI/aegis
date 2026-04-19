"""Initialize and register default sampler algorithms."""

from .random_ import RandomSampler
from .spec import register_sampler


def _create_random_sampler():
    """Factory function to create RandomSampler instance."""
    return RandomSampler()


def _register_default_samplers():
    """Register default sampler algorithms."""
    register_sampler("random", _create_random_sampler)


# Automatically register default samplers when module is imported
_register_default_samplers()
