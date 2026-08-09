# aegislab

The aegis control plane: catalog of microservice benchmarks, fault-injection
lifecycle, RCA evaluation. This glossary covers the chaos / injection
sub-domain currently being redesigned around `aegis-chaos`. See
`docs/aegis-chaos-design.md` for the design under discussion.

## Language

### Catalog terms

**System**:
A benchmark workload (ts, otel-demo, sockshop, ...). Contains many Services.
_Avoid_: benchmark (overloaded — also means "evaluation run"), application.

**Service**:
A microservice within a **System**, identified by the 4-tuple
(system, name, instance, chart_version). Two simultaneously-deployed
instances of the same chart (e.g. `hs0` and `hs1`) are distinct
Service rows even though the chart_version matches.
_Avoid_: app, deployment (too k8s-specific), component.

**Instance**:
The helm release name (or a normalised derivative) that distinguishes
multiple concurrent deployments of the same chart_version of the same
service. Defaults to `default` for single-instance charts.

**Point**:
A single injection target on a **Service** version, content-addressed.
Carries a **Capability** + a target descriptor.
_Avoid_: injection point (when context is clear "point" suffices), fault site.

**Point Manifest**:
The complete catalog of **Points** for a single **Service** version, shipped
inside that Service's helm chart and applied at install time. Replaces
the old notion of runtime "discovery": the Point Manifest is the
authoritative upstream of what is injectable on a deployed Service.
_Avoid_: discovery output, point list (too generic).

**Capability**:
A backend-agnostic perturbation kind (`http_latency`, `pod_kill`,
`jvm_method_delay`, ...). The inter-lingua between user requests and
backend surfaces.
_Avoid_: fault type (overloaded with the historical `chaos_type` enum),
chaos type.

**Executor**:
A registered backend that implements some subset of **Capabilities**
(`chaos-mesh`, `chaos-blade`, `istio`, ...). Renders a Capability call into
its own backend's surface.
_Avoid_: provider, driver.

### Lifecycle terms

**Injection**:
One live application of a (**Point**, params) tuple. Has a status lifecycle
and an opaque executor handle.
_Avoid_: fault (means the abstract perturbation, not the live application).

**Batch**:
A set of **Injections** submitted together that must complete as a unit
before downstream campaign steps fire. Owned by aegis-chaos, not the caller.
Replaces today's in-memory `FaultBatchManager` in aegis-backend.
_Avoid_: hybrid injection (legacy name; "hybrid" was the label flag that
distinguished batched from singleton submissions).

**Campaign**:
A scheduled sequence of work that *uses* Injections — typically
inject → BuildDatapack → RunAlgorithm → CollectResult. Owned by
aegis-backend, **not** by aegis-chaos.
_Avoid_: experiment (overloaded with research-paper "experiment"),
workflow (overloaded with Chaos-Mesh Workflow CRD).

## Relationships

- A **System** contains many **Services**.
- A **Service** has many **Points**.
- A **Point** is bound to one **Capability** and is content-addressed by
  (system, service, version, capability, target).
- One **Capability** is supported by zero or more **Executors**.
- A **Batch** contains one or more **Injections**.
- An **Injection** references exactly one **Point** and (when applicable)
  one **Batch**.
- A **Campaign** schedules **Injections** (singletons) or **Batches** but
  is invisible to aegis-chaos.

## Flagged ambiguities

- "Workflow" was previously used for two distinct things — Chaos-Mesh's
  `Workflow` CRD (multi-CR atomic application, executor-internal) and
  campaign-level orchestration (inject → BuildDatapack → ...). The latter
  is now called **Campaign** and lives in backend; the former is an
  Executor implementation detail and never surfaces in the API.
- "Hybrid" injection / `IsHybrid` label is the legacy name for what is
  now a **Batch**. The label exists in current code at
  `aegislab/src/core/orchestrator/k8s_handler.go:123` but should disappear
  during migration step 5.
- "Fault type" / `chaos_type` enum is the legacy name for what is now a
  **Capability**. The `ChaosTypeMap` at
  `chaos-experiment/handler/handler.go:90-134` is the authoritative
  starting list; §14 of the design doc must be regenerated from it.
- "Discovery" used to mean "aegis-chaos scans cluster/trace/OpenAPI
  to derive Points." That meaning is retired — aegis-chaos does not
  discover anything. Where "discovery" appears now, it refers to
  whatever external script produced a **Point Manifest**, which is
  external to aegis-chaos. The word is best avoided inside the
  aegis-chaos domain.

## Example dialogue

> **Dev:** "When a research operator wants to inject latency on `/api/login`
> in ts v3.2, what gets created?"
>
> **Designer:** "One **Point** identified by content hash of
> (`ts`, `frontend`, `v3.2`, `http_latency`, `{endpoint:/api/login}`).
> Submitting against it creates one **Injection**, which references that
> Point and gets routed to a healthy **Executor** that supports
> `http_latency` — today that's `chaos-mesh`, tomorrow possibly also
> `istio` or `chaos-blade`."
>
> **Dev:** "And what about 'inject 5 different faults at once for a hybrid
> eval round'?"
>
> **Designer:** "That's a **Batch** of 5 Injections. aegis-chaos tracks
> N-of-N completion and fires one webhook to backend when the whole
> Batch finishes. Backend doesn't reconstruct the batch from individual
> injection webhooks anymore — the **Campaign** in backend just waits on
> the Batch completion."
