# rcabench-platform

An experiment framework for Root Cause Analysis (RCA), supporting fast development of RCA algorithms and their evaluation on various datasets.

## Installation

To add this package to another uv-managed project:

```bash
# Install lightweight SDK (for algorithm/sampler development)
uv add rcabench-platform

# Install with platform-internal tools (kubernetes, neo4j, clickhouse, etc.)
uv add "rcabench-platform[internal]"

# Install with analysis/visualization tools (matplotlib, plotly, etc.)
uv add "rcabench-platform[analysis]"

# Install everything (equivalent to the old monolithic install)
uv add "rcabench-platform[all]"
```

The base package includes only the SDK core for developing and evaluating RCA algorithms and trace samplers. Use optional dependency groups to install additional functionality:

- **`[internal]`** — Platform server clients, cloud storage, dataset converters, SDG builder
- **`[analysis]`** — Visualization, statistical analysis, Streamlit dashboards
- **`[all]`** — All of the above

See [Package Restructuring Plan](./docs/package-restructuring-plan.md) for details.

## Documentation

+ [User Guide](./docs/USER_GUIDE.md): Complete guide for using rcabench-platform as both a console command and SDK.
+ [Development Guide](./CONTRIBUTING.md): How to set up the development environment and contribute to this project.
+ [Specifications](./docs/specifications.md): Our design details about RCA algorithms and data formats.
+ [Workflow References](./docs/workflow-references.md): How to use the functionalities of this project.
+ [Maintenance](./docs/maintenance.md): Guidelines for maintaining the project and release procedures.
+ [Package Restructuring Plan](./docs/package-restructuring-plan.md): v3 directory structure, v2→v3 migration guide, and optional dependency groups.

## Related Projects

+ [rcabench](https://github.com/LGU-SE-Internal/rcabench)
+ [rca-algo-contrib](https://github.com/LGU-SE-Internal/rca-algo-contrib)
+ [rca-algo-random](https://github.com/LGU-SE-Internal/rca-algo-random)
