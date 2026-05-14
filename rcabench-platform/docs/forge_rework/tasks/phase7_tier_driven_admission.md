# Phase 7: Tier-driven layer admission

## Principle

> **A fault's physical type determines its cascade semantics.
> Observation is downstream evidence, not a precondition for the
> causal claim.**

This is the FORGE first principle restated for layer admission. The
chaos-tool fault catalog is an authoritative contract: each
`fault_type` declares a `seed_tier` (`unavailable` / `silent` / `slow`
/ `erroring` / `degraded`) which encodes **how the fault deterministically
cascades through callers**. Layer admission should mirror that
determinism, not reverse-engineer it from the metrics.

The path-builder's `_dst_features_match` was originally strict:
admit a layer-N child iff at least one of `expected_features` matched
its band. That is the right policy when caller observability is
faithful to the causal effect — but for some tiers, **observability
gaps are systemic**:

* The caller retried successfully and the error vanished;
* The caller catches the exception and returns a stub;
* The caller is itself silent (it never made the failed call);
* Real abnormal traffic in the window is too low for a 5% band to fire;
* Spans of the affected service are missing from the abnormal trace
  bundle entirely.

For the tiers where the cascade is **structurally guaranteed by the
fault type**, those gaps are noise, not absence of effect.

## Tier-by-tier semantics

| `seed_tier`     | Fault is destructive? | Caller observability | Layer admission |
|-----------------|-----------------------|----------------------|-----------------|
| `unavailable`   | Yes — pod/container is gone | Caller WILL fail (RST/timeout/missing span). Form varies by upstream code. | **Structural** — admit any structurally connected dst. |
| `silent`        | Yes — outbound/inbound flow blackholed | Caller WILL fail or go silent. | **Structural** — same as `unavailable`. |
| `slow`          | No — latency added | Caller's p99 transitively rises **iff** retry/timeout doesn't mask it. Cascade depth is observability-bounded. | **Strict** — feature bands required. |
| `erroring`      | No — bad response | Caller observes 4xx/5xx **iff** it doesn't catch + stub the response. Cascade depth observability-bounded. | **Strict** — feature bands required. |
| `degraded`      | No — partial pressure | Caller's metrics may or may not deviate. | **Strict** — feature bands required. |

Concretely: for `unavailable` and `silent`, `_dst_features_match`
returns `True` regardless of the layer's `expected_features`. For
the other tiers, the existing OR-of-bands check stays.

## What this is NOT

* **Not** a permissive cascade for all faults. Strict tiers stay
  strict — their cascade depth is genuinely tied to caller
  observability.
* **Not** a system-specific patch. The check is per-tier, not per-
  service-name or per-namespace.
* **Not** a pass on FP. The structural admission is bounded above by
  (a) per-layer `max_fanout`, (b) the alarm-terminate filter at the
  propagator level, and (c) the manifest's `derivation_layers` length.
  False positives would require the alarm node to sit in the
  structural reach of v_root through a manifest-allowed edge sequence,
  which is rare for the types actually relaxed.
* **Not** a replacement for `expected_features`. Magnitude evidence
  still flows through `ManifestLayerGate.evidence` for audit and
  paper-side reporting (`tier_relaxed: true` annotation per edge).

## Forward generalization (roadmap)

Other tiers exhibit looser-than-strict cascade physics in some
configurations. Each deserves its own analysis from the **fault-
physics perspective**, not by ad-hoc band tweaking:

1. **`erroring` tier (HTTP* / JVM*Exception / JVMReturn / JVMMySQLException
   / JVMRuntimeMutator)** — when does the immediate caller's
   `error_rate` not visibly rise? Hypothesis: language-specific
   exception handling (`try/except` in Java spring services
   converting RPC error to default value). Generalization: should
   the cascade be structural here too once a **sibling channel**
   (latency, log error) corroborates? Define the corroboration rule
   per-tier.

2. **`slow` tier (HTTPDelay / NetworkDelay / JVMLatency /
   JVMGarbageCollector / JVMMySQLLatency / NetworkBandwidth)** —
   when does `latency_p99_ratio` not propagate to the caller?
   Hypothesis: caller's own pacing (timeout < injected latency)
   chops the cascade. Generalization: pair `latency_p99_ratio` with
   `timeout_rate` band as OR.

3. **`degraded` tier (CPUStress / MemoryStress / JVMCPUStress /
   JVMMemoryStress / NetworkLoss / NetworkCorrupt / NetworkDuplicate
   / TimeSkew)** — what's the deterministic cascade signal?
   Hypothesis: container/JVM resource exhaustion always shows up
   as `cpu_throttle_ratio` / `gc_pressure_ratio` at the v_root
   plane, but propagation outward is conditional. Network-degraded
   classes may need TCP-level features (retransmits) that the
   current adapters don't surface.

These are **agent-explorable hypotheses** — one agent per tier,
each in its own worktree off `forge-rework-integration`, with the
same exit criterion: improved attribution on both the legacy
`rca/` 500-case dataset and the aegislab `detector_success_last13h`
522-case dataset, **without** introducing per-system or per-fault
hacks. Validation re-runs both datasets and reports the per-fault
delta.

## Implementation pointers

* `algorithms/manifest_path_builder.py::_dst_features_match` —
  tier check at the top, returns True for unavailable/silent.
* `algorithms/gates/manifest_layer.py::ManifestLayerGate.evaluate` —
  mirrors the relaxation so the defensive gate doesn't reject what
  the builder admitted; per-edge evidence carries
  `tier_relaxed: bool` for audit.
* No changes to manifest YAML files. The relaxation is policy-level,
  driven by `seed_tier` which the manifests already declare.
