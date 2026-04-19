from typing import Any


def flatten_dict(d: dict[str, Any]) -> dict[str, Any]:
    flat_dict = {}

    def _flatten(cur: dict[str, Any], parent_key: str | None):
        for k, v in cur.items():
            new_key = f"{parent_key}.{k}" if parent_key is not None else k
            if isinstance(v, dict):
                _flatten(v, new_key)
            else:
                flat_dict[new_key] = v

    _flatten(d, None)
    return flat_dict
