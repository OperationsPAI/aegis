package chaos

// SystemType is the aegislab identifier for a target microservice system
// (e.g. "ts", "otel-demo"). Values are forwarded across the chaos-service
// HTTP boundary as strings, so this type stays a string alias.
type SystemType string

func (s SystemType) String() string { return string(s) }

// IsSystemRegistered reports whether a given system short-code is a known
// chaos-system. The boot path wires this to the live etcd/Viper config
// manager; callers depending on this for validation must run after seed.
// Defaults to "always true" so unit tests that don't boot the config
// manager don't reject valid synthetic system names.
var IsSystemRegistered = func(name string) bool { return true }

// IsValid is a thin wrapper around IsSystemRegistered. Kept as a method on
// SystemType for ergonomics in existing call sites.
func (s SystemType) IsValid() bool { return IsSystemRegistered(string(s)) }

// ChaosType enumerates the supported fault categories. Numeric values are
// persisted in MySQL (column fault_type) so any reordering breaks history.
type ChaosType int

const (
	PodKill ChaosType = iota
	PodFailure
	ContainerKill

	MemoryStress
	CPUStress

	HTTPRequestAbort
	HTTPResponseAbort
	HTTPRequestDelay
	HTTPResponseDelay
	HTTPResponseReplaceBody
	HTTPResponsePatchBody
	HTTPRequestReplacePath
	HTTPRequestReplaceMethod
	HTTPResponseReplaceCode

	DNSError
	DNSRandom

	TimeSkew

	NetworkDelay
	NetworkLoss
	NetworkDuplicate
	NetworkCorrupt
	NetworkBandwidth
	NetworkPartition

	JVMLatency
	JVMReturn
	JVMException
	JVMGarbageCollector
	JVMCPUStress
	JVMMemoryStress
	JVMMySQLLatency
	JVMMySQLException
	JVMRuntimeMutator
)

var ChaosTypeMap = map[ChaosType]string{
	PodKill:                  "PodKill",
	PodFailure:               "PodFailure",
	ContainerKill:            "ContainerKill",
	MemoryStress:             "MemoryStress",
	CPUStress:                "CPUStress",
	HTTPRequestAbort:         "HTTPRequestAbort",
	HTTPResponseAbort:        "HTTPResponseAbort",
	HTTPRequestDelay:         "HTTPRequestDelay",
	HTTPResponseDelay:        "HTTPResponseDelay",
	HTTPResponseReplaceBody:  "HTTPResponseReplaceBody",
	HTTPResponsePatchBody:    "HTTPResponsePatchBody",
	HTTPRequestReplacePath:   "HTTPRequestReplacePath",
	HTTPRequestReplaceMethod: "HTTPRequestReplaceMethod",
	HTTPResponseReplaceCode:  "HTTPResponseReplaceCode",
	DNSError:                 "DNSError",
	DNSRandom:                "DNSRandom",
	TimeSkew:                 "TimeSkew",
	NetworkDelay:             "NetworkDelay",
	NetworkLoss:              "NetworkLoss",
	NetworkDuplicate:         "NetworkDuplicate",
	NetworkCorrupt:           "NetworkCorrupt",
	NetworkBandwidth:         "NetworkBandwidth",
	NetworkPartition:         "NetworkPartition",
	JVMLatency:               "JVMLatency",
	JVMReturn:                "JVMReturn",
	JVMException:             "JVMException",
	JVMGarbageCollector:      "JVMGarbageCollector",
	JVMCPUStress:             "JVMCPUStress",
	JVMMemoryStress:          "JVMMemoryStress",
	JVMMySQLLatency:          "JVMMySQLLatency",
	JVMMySQLException:        "JVMMySQLException",
	JVMRuntimeMutator:        "JVMRuntimeMutator",
}

var ChaosNameMap = func() map[string]ChaosType {
	m := make(map[string]ChaosType, len(ChaosTypeMap))
	for k, v := range ChaosTypeMap {
		m[v] = k
	}
	return m
}()

// Groundtruth is the expected impact of a single chaos experiment.
type Groundtruth struct {
	Service   []string `json:"service,omitempty"`
	Pod       []string `json:"pod,omitempty"`
	Container []string `json:"container,omitempty"`
	Metric    []string `json:"metric,omitempty"`
	Function  []string `json:"function,omitempty"`
	Span      []string `json:"span,omitempty"`
}

// Node is the legacy engine-config payload shape. aegislab now rejects raw
// Node submissions (see core/domain/injection/api_types.go), but old
// FaultInjection rows still carry serialized Node JSON in engine_config.
type Node struct {
	Name        string           `json:"name"`
	Range       []int            `json:"range"`
	Children    map[string]*Node `json:"children"`
	Description string           `json:"description"`
	Value       int              `json:"value"`
}

// ChaosResourceMapping describes how a chaos spec field indexes into the
// per-system resource catalog. Retained as the declared type of
// InjectionResp.FaultResourceMap; currently never populated by aegislab.
type ChaosResourceMapping struct {
	IndexFieldName string `json:"index_field_name"`
	ResourceType   string `json:"resource_type"`
}
