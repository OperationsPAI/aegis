# JVMException — Evidence

## Mechanism summary

JVM method-level exception injection: a specific Java method on the
affected container is rewritten to unconditionally throw an exception
(`display_config.exception_opt = 0` default RuntimeException, `1` random
checked/unchecked). Inbound spans crossing the patched method are tagged
ERROR; sibling methods are unaffected.

## Granularity gap (method vs service) — explicit

Ground truth is method-scoped, but the FORGE manifest matches at service
granularity because the IR rolls span metrics per service. In TrainTicket
the chaos tooling tends to target controller/service-layer methods on hot
paths, so service-level error_rate is a strong proxy in practice. Cases
where the patched method is rarely called would silently miss; we did not
encounter such cases in the 5-sample empirical set.

## Entry signature

`required: span.error_rate in [0.30, 1.0]` per Family-E task spec
("entry = span.error_rate >= 0.3").

Empirically, across 5 JVMException cases, GT-service error_rate was
{0.667, 0.660, 0.635, 0.667, 0.667} — extremely tight at ~0.65. The
threshold 0.30 sits a full p5/2 below the empirical floor and excludes
typical background error noise (which sits below 0.05 in fault-free
windows). Tight enough to keep false-positive entries down, loose enough
to catch retry-storm-mitigated cases.

## Derivation layers

- **Layer 1** (callers, `calls backward` + `includes backward`): expects
  error_rate >= 0.10 — most Java callers wrap-and-rethrow rather than
  swallow, so the cascade mostly preserves error visibility.
- **Layer 2** (transitive callers): error_rate >= 0.05. Per Family-E
  guidance, Java services often catch exceptions at boundaries, so 3+ hop
  cascades are rare; we cap at 2.

## Hand-offs

- `HTTPResponseAbort` at error_rate >= 0.5 on layer 1 (universal cross-
  family trigger). Threshold raised from 0.3 to 0.5 because at-the-source
  values empirically saturate at ~0.65, so 0.5 cleanly distinguishes
  "fully cascading" from "partially cascading" before handing off.

## Augmentation labels (flagged)

The Family-E task notes that IR adapters may emit `frequent_gc`,
`high_heap_pressure`, `oom_killed` for some JVM faults. These are NOT in
the bootstrap feature vocabulary (`manifests/features.py`); they are
relevant to JVMGarbageCollector / JVMMemoryStress, not to this manifest.
Flagged for orchestrator awareness; not used here.

## Sample cases

- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-consign-price-service-exception-8b6tng`
  (`getPriceByWeightAndRegion`; error_rate=0.667)
- `/home/ddq/AoyangSpace/dataset/rca/ts0-ts-route-service-exception-5hzgms`
  (`queryAll`; error_rate=0.660)
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-train-service-exception-l5v8n7`
  (`query`; error_rate=0.635)
- `/home/ddq/AoyangSpace/dataset/rca/ts1-ts-food-service-exception-ch2v8l`
  (error_rate=0.667)
- `/home/ddq/AoyangSpace/dataset/rca/ts2-ts-payment-service-exception-jglltg`
  (error_rate=0.667)
