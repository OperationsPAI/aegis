# Sockshop loop — PAUSED after R6 (2026-04-25)

After 6 rounds of broad chaos sweep (60+ candidates spanning Pod/Network/JVM/CPU/Mem chaos types), sockshop's RCA detector has produced only 3/60 +1 outcomes (R3 1, R4 2, R5+R6 0). All R4 winning edges (catalog→front-end NetworkLoss80, orders→front-end NetworkDelay2s) failed to repeat in R6 with identical params.

**Hypothesis:** sockshop detector is bound to specific loadgen-operation timing/state windows, not chaos magnitude or service-graph topology. Tweaking chaos knobs cannot move the needle.

**Next step (out of loop scope):** investigate detector code path for sockshop — likely needs a different signal source than the OTLP traces currently captured.

**Resume condition:** after detector path is reworked OR a new chaos surface is added (e.g. sockshop-specific Coherence cluster operations).
