package chaos

import (
	chaosHandler "github.com/OperationsPAI/chaos-experiment/handler"
)

type (
	SystemType    = chaosHandler.SystemType
	ChaosType     = chaosHandler.ChaosType
	Groundtruth   = chaosHandler.Groundtruth
	InjectionConf = chaosHandler.InjectionConf
	MetadataStore = chaosHandler.MetadataStore
	SystemConfig  = chaosHandler.SystemConfig
)

var (
	ChaosTypeMap        = chaosHandler.ChaosTypeMap
	ChaosNameMap        = chaosHandler.ChaosNameMap
	IsSystemRegistered  = chaosHandler.IsSystemRegistered
	SetMetadataStore    = chaosHandler.SetMetadataStore
)
