package cmd

import "testing"

func TestShouldApplyClusterPrepareLocalE2E(t *testing.T) {
	t.Cleanup(func() {
		clusterPrepareApply = false
		flagNonInteractive = false
		flagDryRun = false
	})

	cases := []struct {
		name           string
		clusterApply   bool
		nonInteractive bool
		dryRun         bool
		wantApply      bool
	}{
		{name: "default", clusterApply: false, nonInteractive: false, dryRun: false, wantApply: false},
		{name: "explicit apply", clusterApply: true, nonInteractive: false, dryRun: false, wantApply: true},
		{name: "non-interactive", clusterApply: false, nonInteractive: true, dryRun: false, wantApply: true},
		{name: "explicit apply + non-interactive", clusterApply: true, nonInteractive: true, dryRun: false, wantApply: true},
		{name: "dry-run suppresses default", clusterApply: false, nonInteractive: false, dryRun: true, wantApply: false},
		{name: "dry-run suppresses non-interactive", clusterApply: false, nonInteractive: true, dryRun: true, wantApply: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clusterPrepareApply = tc.clusterApply
			flagNonInteractive = tc.nonInteractive
			flagDryRun = tc.dryRun
			if got := shouldApplyClusterPrepareLocalE2E(); got != tc.wantApply {
				t.Fatalf("shouldApplyClusterPrepareLocalE2E() = %v, want %v", got, tc.wantApply)
			}
		})
	}
}
