package chaos

// ComputeAggregatedStatus folds a batch's child status set into the
// `aggregated_status` column per ADR-0002 / ADR-0006.
//
// Rules:
//   - any non-terminal child → "running" (or "pending" if no child has started)
//   - all children "succeeded" → "succeeded"
//   - all terminal, all "cancelled" → "cancelled"
//   - all terminal, some "cancelled" + others (mixed) → "cancelled"
//     (operator intent dominates; ADR-0006 stickiness handles the race)
//   - all terminal, no "cancelled", all "failed" → "failed"
//   - all terminal, no "cancelled", mix of succeeded/failed → "partial"
//
// The stickiness rule from ADR-0006 is enforced by the caller — once a
// terminal aggregated_status is persisted, this function is not called
// again for that batch.
func ComputeAggregatedStatus(children []string) string {
	if len(children) == 0 {
		return AggPending
	}
	var nPending, nRunning, nSucceeded, nFailed, nCancelled int
	for _, s := range children {
		switch s {
		case StatusPending:
			nPending++
		case StatusRunning:
			nRunning++
		case StatusSucceeded:
			nSucceeded++
		case StatusFailed:
			nFailed++
		case StatusCancelled:
			nCancelled++
		}
	}
	nonTerminal := nPending + nRunning
	if nonTerminal > 0 {
		if nSucceeded+nFailed+nCancelled == 0 && nRunning == 0 {
			return AggPending
		}
		return AggRunning
	}
	if nCancelled > 0 {
		return AggCancelled
	}
	if nFailed == 0 {
		return AggSucceeded
	}
	if nSucceeded == 0 {
		return AggFailed
	}
	return AggPartial
}
