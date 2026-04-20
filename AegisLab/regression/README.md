# Regression Cases

Repo-tracked `aegisctl regression` cases live here.

Each YAML file is the canonical contract for one regression scenario. The case
owns both the submit payload and the pass/fail expectations so reviewers can see
what local/e2e validation is asserting without digging through shell scripts or
Go conditionals.

## File format

```yaml
name: otel-demo-guided
description: Short human-readable summary
project_name: pair_diagnosis
submit:
  pedestal:
    name: otel-demo
    version: "1.0.0"
  benchmark:
    name: clickhouse
    version: "1.0.0"
  interval: 2
  pre_duration: 1
  algorithms:
    - name: random
      version: "1.0.0"
  specs:
    - - system: otel-demo
        system_type: otel-demo
        namespace: otel-demo
        app: frontend
        chaos_type: PodKill
        duration: 1
validation:
  timeout_seconds: 3000
  min_events: 6
  expected_final_event: datapack.no_anomaly
  required_events:
    - restart.pedestal.started
    - fault.injection.started
    - datapack.build.started
    - algorithm.run.started
    - datapack.no_anomaly
  required_task_chain:
    - RestartPedestal
    - FaultInjection
    - BuildDatapack
    - RunAlgorithm
    - CollectResult
```

## Field notes

- `name`: Stable case identifier. `aegisctl regression run <name>` resolves
  `<name>.yaml` from this directory.
- `project_name`: Default project to resolve before submission. A CLI
  `--project` override still wins when needed.
- `submit`: Exact JSON/YAML payload sent to `POST /api/v2/projects/{id}/injections/inject`.
  The current canonical shape is the guided submit format described in
  `docs/inject-pipeline-design.md`.
- `validation.timeout_seconds`: Max wall-clock wait for the trace SSE stream to
  reach the terminal `end` event.
- `validation.min_events`: Minimum number of trace update events that must be
  observed before the case passes.
- `validation.expected_final_event`: Last trace `event_name` expected before the
  terminal `end` frame.
- `validation.required_events`: Ordered subsequence that must appear in the
  observed trace events.
- `validation.required_task_chain`: Ordered subsequence of task types that must
  be present on the fetched trace detail.

## Running a case

From the repo root:

```bash
just build-aegisctl output=./aegisctl
./aegisctl regression run otel-demo-guided --output json
```

If you need to point at a different checkout location, use `--cases-dir` or
`--file`.
