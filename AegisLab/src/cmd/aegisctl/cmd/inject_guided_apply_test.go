package cmd

import (
	"testing"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/stretchr/testify/require"
)

func TestSubmitGuidedApplyRequiresTags(t *testing.T) {
	origPedestalName := guidedApplyPedestalName
	origPedestalTag := guidedApplyPedestalTag
	origBenchmarkName := guidedApplyBenchmarkName
	origBenchmarkTag := guidedApplyBenchmarkTag
	origInterval := guidedApplyInterval
	origPreDuration := guidedApplyPreDuration
	defer func() {
		guidedApplyPedestalName = origPedestalName
		guidedApplyPedestalTag = origPedestalTag
		guidedApplyBenchmarkName = origBenchmarkName
		guidedApplyBenchmarkTag = origBenchmarkTag
		guidedApplyInterval = origInterval
		guidedApplyPreDuration = origPreDuration
	}()

	guidedApplyPedestalName = "ts"
	guidedApplyPedestalTag = ""
	guidedApplyBenchmarkName = "bench"
	guidedApplyBenchmarkTag = ""
	guidedApplyInterval = 10
	guidedApplyPreDuration = 1

	err := submitGuidedApply(guidedcli.GuidedConfig{})
	require.ErrorContains(t, err, "--pedestal-tag")
	require.ErrorContains(t, err, "--benchmark-tag")
}
