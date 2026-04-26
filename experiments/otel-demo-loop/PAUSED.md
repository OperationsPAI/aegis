# otel-demo loop — PAUSED after R16 (2026-04-25)

After 16 rounds across 100+ candidates spanning every major chaos type (PodFailure, PodKill, NetworkLoss/Delay/Partition/Bandwidth/Corrupt, MemoryStress, CPUStress, ContainerKill, JVMException, DNSError) and every primary service, the otel-demo RCA detector has produced highly variable +1 rates:

- **Solo PodFailure 5-12min on any service**: ~5-15% hit rate, indistinguishable from noise floor
- **NetworkLoss/Delay/Partition on observed pairs**: same ~10-15%, even with R1/R2 exact replays
- **Specific 2-service paired chaos**: 1-shot wins that did not repeat (R5 frontend+checkout, R7 fraud-detection×image-provider)
- **R1 winner exact replay** (ad/NetworkLoss30 flagd 5min): -1 in R15

**Conclusion:** otel-demo detector signal is fundamentally stochastic, not param-deterministic. Hit rate at noise floor (~10%) regardless of chaos type/magnitude/timing. Tweaking chaos knobs cannot move the needle.

**Stable winners across all rounds (ordered by reproducibility):**
1. currency solo PodFailure: 5/8 attempts +1 (~63%, but heavy variance)
2. recommendation/NetworkPartition→flagd direction:to: R2 + R14 winners, R15 -1
3. fraud-detection×image-provider PAIR: R7 +1, R10 -1 (single-shot)

**Next step (out of loop scope):** investigate `aegis/internal/datapack/builder` and detector implementation for otel-demo's anomaly-detection logic. The signal source needs rework before further chaos sweeps will yield clean signal.

**Resume condition:** after detector path investigated and reworked, OR if a different observability signal (e.g. ClickHouse RUM events, browser session breakage) is added.
