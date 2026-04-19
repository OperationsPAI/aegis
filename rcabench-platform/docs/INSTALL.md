# RCABench Platform Installation Guide

## Quick Installation

### Install from PyPI (Recommended)
```bash
pip install rcabench-platform
```

### Install with uv (Faster)
```bash
uv add rcabench-platform
```

### Install Latest Version from GitHub
```bash
pip install git+https://github.com/LGU-SE-Internal/rcabench-platform.git
```

## Usage

After installation, you will have access to the following command-line tools:

### Online-specific Command `rca`
```bash
# Direct access to online features
rca list-datasets
rca list-algorithms
rca query-injection "mysql-corruption"
rca upload-algorithm-harbor /path/to/algorithm/
```

## Development Installation

If you want to contribute to development, we recommend installing in development mode:

```bash
# Clone repository
git clone https://github.com/LGU-SE-Internal/rcabench-platform.git
cd rcabench-platform

# Development mode installation
pip install -e .

# Or use uv
uv sync
```

## Verify Installation

```bash
# Check version
rca --help

```

## Uninstall

```bash
pip uninstall rcabench-platform
```

## Troubleshooting

### Command Not Found
If you get "command not found" after installation, ensure pip's installation directory is in your PATH:

```bash
# Check pip installation directory
python -m site --user-base

# Add the following directory to PATH
export PATH="$PATH:$(python -m site --user-base)/bin"
```

### Permission Issues
If you encounter permission issues, use user installation:

```bash
pip install --user rcabench-platform
```
