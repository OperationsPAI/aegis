package chaos

// ChaosTypeToCapability mirrors tools/capgen/output/capabilities.json's
// `chaos_type` -> `name` columns. Hand-maintained: capgen emits JSON, not Go.
// Lives here (not under cli/) so both aegisctl and the backend pre-flight
// catalog validator (§11 step 4.5) can address the same Point identity.
var ChaosTypeToCapability = map[string]string{
	"ContainerKill":            "container_kill",
	"CPUStress":                "cpu_stress",
	"DNSError":                 "dns_error",
	"DNSRandom":                "dns_random",
	"HTTPRequestAbort":         "http_request_abort",
	"HTTPRequestDelay":         "http_request_delay",
	"HTTPRequestReplaceMethod": "http_request_replace_method",
	"HTTPRequestReplacePath":   "http_request_replace_path",
	"HTTPResponseAbort":        "http_response_abort",
	"HTTPResponseDelay":        "http_response_delay",
	"HTTPResponsePatchBody":    "http_response_patch_body",
	"HTTPResponseReplaceBody":  "http_response_replace_body",
	"HTTPResponseReplaceCode":  "http_response_replace_code",
	"JVMCPUStress":             "jvm_cpu_stress",
	"JVMGarbageCollector":      "jvm_gc",
	"JVMMemoryStress":          "jvm_memory_stress",
	"JVMException":             "jvm_method_exception",
	"JVMLatency":               "jvm_method_latency",
	"JVMReturn":                "jvm_method_return",
	"JVMMySQLException":        "jvm_mysql_exception",
	"JVMMySQLLatency":          "jvm_mysql_latency",
	"JVMRuntimeMutator":        "jvm_runtime_mutator",
	"MemoryStress":             "memory_stress",
	"NetworkBandwidth":         "network_bandwidth",
	"NetworkCorrupt":           "network_corrupt",
	"NetworkDelay":             "network_delay",
	"NetworkDuplicate":         "network_duplicate",
	"NetworkLoss":              "network_loss",
	"NetworkPartition":         "network_partition",
	"PodFailure":               "pod_failure",
	"PodKill":                  "pod_kill",
	"TimeSkew":                 "time_skew",
}
