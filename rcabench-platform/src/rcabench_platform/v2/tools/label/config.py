from pathlib import Path

# Application configuration
APP_TITLE = "Fault Injection Data Labeling and Analysis System"
APP_ICON = "🔍"
LAYOUT = "wide"

# Path configuration
BASE_DIR = Path(".")
DATA_DIR = BASE_DIR / "temp" / "label"
STATIC_DIR = BASE_DIR / "static"

# Dataset configuration
DATASET_BASE_PATH = BASE_DIR / "data" / "rcabench_dataset"
DATASET_CONVERTED_SUFFIX = "converted"

# Database configuration
DATABASE_PATH = DATA_DIR / "labels.db"

# Data file configuration
REQUIRED_FILES = [
    "env.json",
    "injection.json",
    "conclusion.parquet",
    "normal_metrics.parquet",
    "abnormal_metrics.parquet",
    "normal_logs.parquet",
    "abnormal_logs.parquet",
    "normal_traces.parquet",
    "abnormal_traces.parquet",
]

OPTIONAL_FILES = [
    "normal_metrics_histogram.parquet",
    "abnormal_metrics_histogram.parquet",
    "normal_metrics_sum.parquet",
    "abnormal_metrics_sum.parquet",
]

# UI configuration
MAX_LOG_ROWS = 1000
DEFAULT_TIME_RANGE = (0, 100)  # percentage
MAX_TRACE_NODES = 500
DEFAULT_LAYOUT_ALGORITHM = "spring"

# Cache configuration
CACHE_TTL = 3600  # seconds
MAX_CACHE_SIZE = 100  # MB
