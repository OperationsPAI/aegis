# Aegis

A platform for root-cause-analysis research on microservice systems: inject a
fault into a running benchmark, collect the telemetry it produced, run RCA
algorithms against that window, and score them against the known ground truth.

Two components share this repository.

| Path | What it is |
| --- | --- |
| [`aegislab/`](aegislab/) | The platform. A Go modular monolith that owns chaos systems, projects, datasets, fault injections, executions, and the async runtime driving the build → inject → collect → detect pipeline. Ships `aegisctl`, the operator CLI. |
| [`rcabench-platform/`](rcabench-platform/) | The evaluation framework. A Python SDK for writing RCA algorithms and trace samplers, plus the harness that evaluates them across datasets. |

## What it does

Aegis deploys a benchmark microservice system (a *pedestal*) into a Kubernetes
namespace, injects a fault through Chaos Mesh, and captures the traces, metrics,
and logs from the blast window into a *datapack* — a self-contained parquet
bundle carrying the fault's ground truth. RCA algorithms then run against
datapacks and are scored on whether they name the right root cause.

Eight benchmark systems are integrated: train-ticket, sock-shop, TeaStore,
OpenTelemetry Demo, Online Boutique, and three DeathStarBench suites
(hotel-reservation, social-network, media-microservices).

The fault catalog covers pod, network, HTTP, DNS, JVM, stress, and time chaos,
resolved down to individual injection points (a specific container, a specific
service endpoint, a specific JVM method) rather than whole-service faults.

## Getting started

- **Run it on a fresh kind cluster** — [`docs/deployment/cold-start-kind.md`](docs/deployment/cold-start-kind.md)
  is the validated end-to-end walkthrough, from empty cluster to a built datapack.
- **Drive it** — [`aegislab/src/cli/README.md`](aegislab/src/cli/README.md) documents `aegisctl`.
- **Write an RCA algorithm** — [`rcabench-platform/docs/USER_GUIDE.md`](rcabench-platform/docs/USER_GUIDE.md).
- **Work on the platform** — [`aegislab/CONTRIBUTING.md`](aegislab/CONTRIBUTING.md) covers
  the module layout, the five `fx` plugin points, and how to add a module.

## Architecture

`aegislab` is one Go module with one schema and one Helm chart. The
`cmd/aegis-*` binaries each link only the `boot/<role>` options they need, so
the same code runs as a single developer process or as a split gateway +
worker + sso + blob deployment. [`aegislab/README.md`](aegislab/README.md)
has the layer rules and the dependency graph;
[`docs/code-topology/`](docs/code-topology/) has the wiring details.

## Relationship to the development repository

This repository is the public release view. Internal deployment overlays,
fault-injection campaign artifacts, and agent tooling are not mirrored here.

## License

Apache 2.0 — see [LICENSE](LICENSE).
