# Cross-Repo Feature Linkage

This workspace index now models three feature parents that cut across the submodules instead of treating each repository as an isolated code map.

`REQ-900` tracks the fault injection lifecycle:
- `chaos-experiment` owns CRD builders, target discovery, and registration.
- `AegisLab` owns API submission, translation, task orchestration, and callback-driven execution.

`REQ-901` tracks dataset collection and schema flow:
- `AegisLab` owns datapack build/upload and dataset management.
- `rcabench-platform` owns normalized dataset conversion, indexing, and artifact validation.

`REQ-902` tracks RCA evaluation and benchmarking:
- `AegisLab` owns execution submission, persistence, and evaluation endpoints.
- `rcabench-platform` owns algorithm registries, benchmark metrics, and report aggregation.

The shared contracts referenced by these features are:
- `chaos-mesh-crd` for backend/chaos library coupling
- `dataset-schema` for datapack production and downstream consumption

Deployment discovery added one cross-repo constraint worth making explicit:
- the local end-to-end path currently depends on parent-repo orchestration, but each submodule still carries internal-only defaults of its own: `AegisLab` for cluster/storage/runtime config, `chaos-experiment` for Chaos Mesh API compatibility, and `rcabench-platform` for downstream base URLs and JuiceFS-backed datasets.
