# Phase 2 Family F — DNS / Time

**Owner**: 1 agent (`mfst-F`)
**Worktree**: yes
**Reads first**: `phase2_template.md`, then `SCHEMA.md`

## Fault types in scope (3)

| fault_type_name | seed_tier | target_kind | mechanism |
|---|---|---|---|
| `DNSError` | silent | pod | DNS resolution returns NXDOMAIN/SERVFAIL; outbound flows fail at lookup |
| `DNSRandom` | silent | pod | DNS returns random IP; connections go to wrong target or fail |
| `TimeSkew` | degraded | pod | Container clock shifted; affects timestamp-sensitive logic |

## Sample injection directories

```bash
ls /home/ddq/AoyangSpace/dataset/rca/ | grep -iE 'dns|time' | head -10
```

These are RARE in the dataset (~5–15 cases each by name pattern). If you cannot find ≥3 cases for any fault type, mark all bands as `magnitude_source: theoretical` and document.

## Family-specific guidance

### DNS faults are outbound-isolation faults (Class E)

- Both `DNSError` and `DNSRandom` block outbound traffic at lookup time.
- Caller spans show `silent: true` or specific DNS error codes.
- The injecting pod itself doesn't fail; only its outbound flows do.

Entry signature:
- `pod.silent` is too coarse (the pod still serves inbound traffic).
- Use **caller-side observation**: in the entry window, spans where the injecting pod is the callee but is NOT being called (silent), OR caller spans show specific DNS errors.
- This is awkward. Document the ambiguity.
- Alternative: feature `dns_failure_rate` on caller spans. Add to vocabulary if missing; flag to orchestrator.

### DNSError vs DNSRandom

- `DNSError`: NXDOMAIN — caller fails fast, observable as `dns_failure_rate` or `error_rate` on outbound calls.
- `DNSRandom`: random IP returned — caller may connect to wrong target (timeout) or fail (connection refused). More variable.

### TimeSkew is the WEIRD one

- Clock shift affects logic that uses timestamps (token expiry, cache TTL, scheduling).
- May cause `error_rate` spike (auth fails because token "expired") OR be entirely silent (no timestamp-dependent code path taken).
- Entry signature: very loose. Use `error_rate >= 0.05` AND require concurrent injection record (`do(v_root, TimeSkew)`).
- Many TimeSkew injections may end up `ineffective` outcome — that's expected.

### Cascade pattern

- DNS faults: layer 1 = caller spans show `dns_failure_rate` or `error_rate`. Layer 2 = transitive callers (limited; usually retries paper over).
- TimeSkew: cascade is very application-specific; usually layer 1 only, document as low-confidence.

### Hand-off candidates

- DNSError → NetworkPartition (both produce silent + caller error patterns) — within-family-ish (both are outbound-isolation faults).
- TimeSkew → JVMException (auth/token errors throw) — only if data supports.

### Vocabulary requests

Almost certainly need a new feature: `dns_failure_rate` on span kind. Add to vocabulary request to orchestrator if not present in Phase 1 bootstrap (it IS in the bootstrap list — verify before requesting).

## Acceptance bar (family F)

- 3 manifests, with explicit acknowledgment in evidence MD that this family is the most ambiguous.
- DNS faults' "outbound-only" nature reflected in entry signature (NOT `pod.silent`).
- TimeSkew documented as low-confidence with rationale.
- Any new vocabulary needs flagged to orchestrator BEFORE writing manifests; do not silently invent feature names.
