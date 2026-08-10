# Webhook context: OTel standard for tracing, opaque blob for caller fields

The chaos webhook contract splits caller-supplied context into two
disjoint channels: OTel trace propagation rides W3C `traceparent`
headers on both submit and webhook requests, while every other caller
field (task_id, group_id, project_id, user_id, parent_task_id,
task_type, …) travels as an opaque JSON `caller_metadata` blob that
aegis-chaos stores verbatim and echoes unchanged in the webhook.

Today's `HandleCRDSucceeded` (`src/core/orchestrator/k8s_handler.go:230`)
reconstructs OTel propagators from CRD annotations
(`taskCarrier`/`traceCarrier`) because CRDs lack HTTP-style headers.
HTTP services do not — so the right thing is to let aegis-chaos
participate in the trace as a normal HTTP middleware participant, not
to invent a `caller_trace_context` field. The campaign-side fields
(taskID etc.) are equally invisible to aegis-chaos: they belong to
backend's task DAG and only backend needs to interpret them. Making
them a typed schema would force aegis-chaos to evolve every time
backend adds a context field; making them opaque keeps the two
services' release cycles independent.

## Consequences

- aegis-chaos installs standard OTel HTTP middleware on inbound + outbound
  edges; CRD annotations no longer carry trace propagators.
- `caller_metadata` is a JSON column with no schema validation on the
  aegis-chaos side; backend round-trips its own shape and is solely
  responsible for handling its own evolutions.
- The legacy `IsHybrid` label and `batchID` label encoded in CRDs
  disappear; "is this Injection part of a Batch" is a typed
  `batch_id` FK on the `injections` row.
- Backend's `/hooks/chaos-batch` handler must implement W3C trace
  continuation (one line with the OTel SDK) and idempotency on
  `(batch_id, new_status)`; the rest is just walking `child_results`
  and recovering its own task linkage from each child's
  `caller_metadata`.
