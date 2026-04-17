# Cross-Repo Feature Linkage

This workspace index now models three feature parents that cut across the four submodules instead of treating each repository as an isolated code map.

`REQ-900` tracks the fault injection lifecycle:
- `chaos-experiment` owns CRD builders, target discovery, and registration.
- `AegisLab` owns API submission, translation, task orchestration, and callback-driven execution.
- `AegisLab-frontend` owns injection authoring and datapack inspection.

`REQ-901` tracks dataset collection and schema flow:
- `AegisLab` owns datapack build/upload and dataset management.
- `AegisLab-frontend` owns datapack browsing and download UX.
- `rcabench-platform` owns normalized dataset conversion, indexing, and artifact validation.

`REQ-902` tracks RCA evaluation and benchmarking:
- `AegisLab` owns execution submission, persistence, and evaluation endpoints.
- `AegisLab-frontend` owns execution launch, result drill-down, and evaluation configuration.
- `rcabench-platform` owns algorithm registries, benchmark metrics, and report aggregation.

The shared contracts referenced by these features are:
- `openapi-backend-sdk` for backend/frontend typed API coupling
- `chaos-mesh-crd` for backend/chaos library coupling
- `dataset-schema` for datapack production and downstream consumption
