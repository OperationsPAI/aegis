import json
from datetime import datetime
from pathlib import Path
from typing import Any

import polars as pl
import streamlit as st


def validate_dataset_folder(folder_path: Path) -> tuple[bool, list[str]]:
    from rcabench_platform.v2.tools.label.config import REQUIRED_FILES

    missing_files = []
    if not folder_path.exists():
        return False, ["Folder does not exist"]

    for required_file in REQUIRED_FILES:
        file_path = folder_path / required_file
        if not file_path.exists():
            missing_files.append(required_file)

    return len(missing_files) == 0, missing_files


def format_timestamp(timestamp: int, timezone: str = "Asia/Shanghai") -> str:
    try:
        dt = datetime.fromtimestamp(timestamp)
        return dt.strftime("%Y-%m-%d %H:%M:%S")
    except Exception:
        return str(timestamp)


def get_injection_time_markers(env_data: dict) -> dict[str, datetime]:
    try:
        markers = {}
        for key, env_key in [
            ("normal_start", "NORMAL_START"),
            ("normal_end", "NORMAL_END"),
            ("abnormal_start", "ABNORMAL_START"),
            ("abnormal_end", "ABNORMAL_END"),
        ]:
            if env_key in env_data:
                timestamp = float(env_data[env_key])
                markers[key] = datetime.fromtimestamp(timestamp)
        return markers
    except Exception as e:
        print(f"Error parsing time markers: {e}")
        return {}


def truncate_string(s: str, max_length: int = 100) -> str:
    if len(s) <= max_length:
        return s
    return s[: max_length - 3] + "..."


@st.cache_data(ttl=3600)
def cached_load_parquet(file_path: str) -> pl.DataFrame:
    df = pl.read_parquet(file_path)
    return df


@st.cache_data(ttl=3600)
def cached_load_json(file_path: str) -> dict[str, Any]:
    try:
        with open(file_path, encoding="utf-8") as f:
            return json.load(f)
    except Exception as e:
        st.error(f"Failed to load JSON file: {file_path}, error: {str(e)}")
        return {}


_data_loader = None
_label_manager = None


def get_data_loader():
    global _data_loader
    if _data_loader is None:
        from .data_loader import DataLoader

        _data_loader = DataLoader()
    return _data_loader


def get_label_manager():
    global _label_manager
    if _label_manager is None:
        from .label_manager import LabelManager

        _label_manager = LabelManager()
    return _label_manager
