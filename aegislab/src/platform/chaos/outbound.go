package chaos

// OutboundBearerEnv names the env-var fallback for the SA-minted
// backend->aegis-chaos bearer token. Both the orchestrator dispatcher and the
// chaossystem candidate enumerator read this same env when SA mint hasn't
// completed; keeping the constant here avoids the two sides drifting.
const OutboundBearerEnv = "CHAOS_OUTBOUND_BEARER"
