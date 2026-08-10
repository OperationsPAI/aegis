package chaos

import "testing"

func TestComputeAggregatedStatus(t *testing.T) {
	cases := []struct {
		name     string
		children []string
		want     string
	}{
		{"empty batch", nil, AggPending},
		{"all pending", []string{StatusPending, StatusPending}, AggPending},
		{"one running rest pending", []string{StatusRunning, StatusPending}, AggRunning},
		{"some terminal some running", []string{StatusSucceeded, StatusRunning}, AggRunning},
		{"all succeeded", []string{StatusSucceeded, StatusSucceeded}, AggSucceeded},
		{"all failed", []string{StatusFailed, StatusFailed}, AggFailed},
		{"all cancelled", []string{StatusCancelled, StatusCancelled}, AggCancelled},
		{"mix succ + failed → partial", []string{StatusSucceeded, StatusFailed}, AggPartial},
		// ADR-0006: cancel of a mixed-result batch must resolve to cancelled,
		// not partial — operator intent is the load-bearing signal.
		{"cancel wins over partial", []string{StatusSucceeded, StatusFailed, StatusCancelled}, AggCancelled},
		{"cancel + succ → cancelled", []string{StatusSucceeded, StatusCancelled}, AggCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputeAggregatedStatus(tc.children); got != tc.want {
				t.Fatalf("children=%v: want %q got %q", tc.children, tc.want, got)
			}
		})
	}
}
