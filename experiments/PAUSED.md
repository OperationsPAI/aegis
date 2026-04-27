# Loop paused 2026-04-27

Reason: PR #222 (BuildDatapack CH freshness probe) regressed — every probe fails with `unexpected packet [72] from server` (native driver hitting HTTP port 8123, expects 9000). All BuildDatapack tasks aborted before sending data to CH → 0 % +1 yield.

Backend hot-rolled back to `loop-20260426b` (pre-#222) so the env isn't stuck, but **no new candidates submitted** until #226 fix PR lands + image rebuilt + redeployed.

Refs:
- Issue #226 — regression report
- Agent in flight building fix + new image `loop-20260427b`

Resume protocol once fix is merged + redeployed:
1. Verify runtime-worker pod image == `loop-20260427b` (or later)
2. Submit a 1-candidate canary on TT to confirm freshness probe runs without abort
3. If canary +1, resume normal multi-system rounds
