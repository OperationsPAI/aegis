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

	ChaosResourceMapping = chaosHandler.ChaosResourceMapping
	Node                 = chaosHandler.Node

	ServiceEndpointData      = chaosHandler.ServiceEndpointData
	DatabaseOperationData    = chaosHandler.DatabaseOperationData
	GRPCOperationData        = chaosHandler.GRPCOperationData
	JavaClassMethodData      = chaosHandler.JavaClassMethodData
	RuntimeMutatorTargetData = chaosHandler.RuntimeMutatorTargetData
	NetworkPairData          = chaosHandler.NetworkPairData
)

var (
	ChaosTypeMap       = chaosHandler.ChaosTypeMap
	ChaosNameMap       = chaosHandler.ChaosNameMap
	IsSystemRegistered = chaosHandler.IsSystemRegistered
	SetMetadataStore   = chaosHandler.SetMetadataStore

	RegisterSystem    = chaosHandler.RegisterSystem
	UnregisterSystem  = chaosHandler.UnregisterSystem
	UpdateSystem      = chaosHandler.UpdateSystem
	GetAllSystemTypes = chaosHandler.GetAllSystemTypes
)
