# Fault-kind disambiguation

`fault_kind` labels a root cause by the **observable symptom** in telemetry,
not by the injection mechanism. Naming follows SRE / postmortem vocabulary.
Two kinds that produce indistinguishable signal in our parquets are
collapsed into one equivalence class at evaluation time (see
**Equivalence classes** at the bottom); pick whichever member fits the
data — the evaluator scores them as the same answer.

## Allowed fault kinds — semantic definitions

### Pod / container lifecycle
- **pod_failure** — A service instance briefly stops emitting and then
  recovers on its own within the observation window.
- **pod_unavailable** — A service instance stops emitting and stays gone
  for the rest of the observation window.

### Network (between services)
- **network_delay** — Requests still reach the peer, but in-flight time
  grows.
- **network_loss** — Packets are silently dropped on the wire, causing
  retransmits or timeouts on top of an otherwise-live connection.
- **network_partition** — Communication between a pair of endpoints is cut
  for a sustained period.
- **network_corrupt** — Packets arrive but their bytes are altered,
  tripping upper-layer checksums or deserialization.
- **network_duplicate** — The same packet is delivered more than once.
- **network_bandwidth_throttled** — Link throughput between two endpoints
  is limited, so large messages back up.

### HTTP layer
- **http_aborted** — An HTTP request or response is cut, surfacing as an
  application-level error rather than a TCP-level disconnect.
- **http_slow** — An HTTP request or response is held back at the
  application layer while the underlying connection stays healthy.
- **http_response_body_corrupted** — The HTTP body, path, or method is
  rewritten; the message still parses but its content diverges from
  intent.
- **http_wrong_status_code** — Only the HTTP status code is wrong; the
  response body itself is unchanged.

### Resource exhaustion
- **container_cpu_saturated** — Container-level CPU is pinned at the
  cgroup limit; every thread inside slows down.
- **process_cpu_saturated** — A single process inside the container
  (e.g. the JVM) saturates its CPU share while the container as a whole
  is not exhausted.
- **container_memory_saturated** — Container memory saturates, triggering
  swap, OOM, or cgroup pressure.
- **jvm_heap_exhausted** — JVM heap occupancy approaches its limit,
  producing GC churn or in-process OOM regardless of container memory
  headroom.
- **jvm_gc_thrashing** — GC pauses become long enough to stall
  application threads even when heap occupancy itself does not look
  extreme.

### Code-level (application / DB driver)
- **application_exception** — Application code throws an unhandled
  exception that escapes to the request handler.
- **application_method_slow** — A specific application method's
  self-reported execution time grows.
- **database_call_failed** — A database call throws (driver or SQL
  layer).
- **database_call_slow** — A database call's self-reported execution
  time grows.
- **wrong_return_value** — Application code returns a value or behaves in
  a way that diverges from its real implementation, before anything is
  serialized to the wire.

### DNS / time
- **dns_lookup_failed** — Name lookup returns an error or times out; no
  connection to the intended target is attempted.
- **dns_returned_wrong_address** — Name lookup returns a wrong address;
  a connection happens but to the unintended peer.
- **clock_skew** — A process's wall clock drifts away from real time,
  scrambling cross-process timestamp ordering.

## Boundary rules (use when symptoms look similar)

### "Things are slow" — `network_delay` vs `http_slow` vs `*_method_slow` / `database_call_slow`
- `network_delay` shows extra time **on the wire**: peer-to-peer
  transport latency grows while in-process self-reported timings stay
  flat.
- `http_slow` shows extra time **at the HTTP layer**: request/response
  handling is delayed even though transport RTT is normal.
- `application_method_slow` / `database_call_slow` show extra time
  **inside the process**: a specific method or DB call's self-time grows
  while the rest of the call stack and the network are unaffected.

### "Things are broken" — `network_loss/partition` vs `http_aborted` vs `dns_lookup_failed`
- `network_loss` and `network_partition` are **transport-layer failures**
  (and are scored as the same answer — see Equivalence classes); HTTP and
  DB layers see passive socket errors.
- `http_aborted` is an **application-layer cut**: the transport
  connection may still exist, but the HTTP exchange ends abnormally.
- `dns_lookup_failed` is a **pre-connection failure**: no useful
  connection to the target is ever made.

### "Wrong data" — `network_corrupt` vs `http_response_body_corrupted` vs `http_wrong_status_code` vs `wrong_return_value`
- `network_corrupt` damages bytes **in transit**; failures appear as
  deserialization / checksum errors below the application.
- `http_response_body_corrupted` rewrites a syntactically valid but
  semantically wrong message at the HTTP layer.
- `http_wrong_status_code` keeps the body intact and only flips the
  status code — downstream logic that branches on status misbehaves.
- `wrong_return_value` distorts a method's return value or behavior
  **inside the process** before anything is serialized.

### CPU exhaustion — `container_cpu_saturated` vs `process_cpu_saturated`
- `container_cpu_saturated` saturates the **whole container**; all
  processes inside it feel it.
- `process_cpu_saturated` saturates **a single process** (the JVM
  worker threads in our datasets); container-level CPU headroom may
  still exist.

### Memory pressure — `container_memory_saturated` vs `jvm_heap_exhausted` vs `jvm_gc_thrashing`
- `container_memory_saturated` is a **container-level** memory squeeze
  (cgroup / OS view).
- `jvm_heap_exhausted` is **heap-bound**: heap occupancy climbs toward
  the limit, often accompanied by GC churn.
- `jvm_gc_thrashing` is **pause-bound**: GC pauses get long enough to
  stall the application, even when heap occupancy itself does not look
  extreme.

### Failures originating in the DB call — `application_exception` vs `database_call_failed`
- `database_call_failed` is reserved for throws originating in the
  database driver / SQL layer.
- `application_exception` covers application-code throws elsewhere.

## Equivalence classes

These pairs are scored as the **same answer** by the evaluator. Either
member is accepted; do not waste effort distinguishing them — the
distinguishing signal is not reliably present in our telemetry.

| Class | Members | Why collapsed |
|---|---|---|
| Pod down | `pod_failure`, `pod_unavailable` | Difference is "did the instance recover inside the window"; the cutoff is GT-side knowledge with no stable telemetry surface. |
| Network blocking | `network_loss`, `network_partition` | Mechanically distinct (probabilistic drop vs deterministic DROP), but their signatures merge in our parquets above ~60% loss rate — success-count, JDBC error text, and duration distributions all overlap. See `docs/openrca-2-lite.md` §3.5 for the empirical evidence. |
