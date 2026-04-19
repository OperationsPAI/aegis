# Microservice Kubernetes Skeleton

Post phase-2 collapse, AegisLab ships as two binaries:

- `api-gateway` — single API process (HTTP + runtime-intake gRPC)
- `runtime-worker-service` — task/controller/receiver loop

Current file:

- `aegislab-microservices.yaml`

Purpose:

- Deployment/Service skeletons for `api-gateway` and `runtime-worker-service`
- Explicit ports, start commands, probe conventions, config mounts

Ports:

- api-gateway: 8082 (HTTP), 9096 (runtime-intake gRPC, worker -> gateway)
- runtime-worker-service: 9094 (query gRPC, gateway -> worker)

Notes:

- This is a skeleton, not a production deployment.
- Assumes MySQL / Redis / Etcd / Jaeger / BuildKit are provided separately.
- `ConfigMap/aegislab-config` must already contain `config.toml`.
