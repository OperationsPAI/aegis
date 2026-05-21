package chaos

import (
	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
)

type (
	GuidedConfig   = guidedcli.GuidedConfig
	GuidedResponse = guidedcli.GuidedResponse
	GuidedSession  = guidedcli.GuidedSession
	FieldSpec      = guidedcli.FieldSpec
	FieldOption    = guidedcli.FieldOption
	Preview        = guidedcli.Preview
)

var (
	ApplyNextSelection     = guidedcli.ApplyNextSelection
	BuildInjection         = guidedcli.BuildInjection
	EnumerateAllCandidates = guidedcli.EnumerateAllCandidates
	LoadConfig             = guidedcli.LoadConfig
	MergeConfig            = guidedcli.MergeConfig
	Resolve                = guidedcli.Resolve
	SaveConfig             = guidedcli.SaveConfig
)
