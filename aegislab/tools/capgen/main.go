// capgen regenerates the aegis-chaos Capability seed list, a markdown
// summary, and conformance-harness probe stubs from a hand-maintained
// table that mirrors chaos-experiment/handler/handler.go:ChaosTypeMap.
//
// Why a table and not AST/reflection: ChaosTypeMap is ~30 entries and
// stable across releases; the per-capability spec fields, units, and
// observable contracts cannot be derived mechanically from the renderer
// (durations are minutes-int in upstream but seconds-int in the
// inter-lingua per §3; observable assertions require domain judgement).
// A typed in-file table is auditable and diffable; an AST walker would
// either still need a hand table for the contract or would hallucinate.
//
// Source of truth for the enum list: chaos-experiment/handler/handler.go
// `ChaosTypeMap`. If upstream adds/removes entries, the
// ChaosTypeMapInvariant test below will fail.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type capability struct {
	Name               string         `json:"name"`
	ChaosType          string         `json:"chaos_type"`
	CRDKind            string         `json:"crd_kind"`
	Status             string         `json:"status"`
	OneLine            string         `json:"one_line"`
	TargetSchema       map[string]any `json:"target_schema"`
	ParamSchema        map[string]any `json:"param_schema"`
	ObservableContract map[string]any `json:"observable_contract"`
	ConformanceProbe   map[string]any `json:"conformance_probe"`
}

const schemaDraft = "https://json-schema.org/draft/2020-12/schema"

func obj(props map[string]any, required ...string) map[string]any {
	req := make([]any, 0, len(required))
	for _, r := range required {
		req = append(req, r)
	}
	return map[string]any{
		"$schema":              schemaDraft,
		"type":                 "object",
		"additionalProperties": false,
		"required":             req,
		"properties":           props,
	}
}

func intRange(min, max, dflt int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max, "default": dflt}
}

func intMin(min int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min}
}

func str(minLen int) map[string]any {
	return map[string]any{"type": "string", "minLength": minLen}
}

// duration_s in seconds matches §3 inter-lingua; upstream renderer
// converts to minute-string at the chaos-mesh boundary.
func durationS() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": 3600, "default": 60}
}

// Pod selector shared across most capabilities: kubernetes namespace
// plus app-label value. Container-scoped capabilities add `container`.
func podTarget(extra map[string]any, extraRequired ...string) map[string]any {
	props := map[string]any{
		"namespace": str(1),
		"app":       str(1),
	}
	required := []string{"namespace", "app"}
	for k, v := range extra {
		props[k] = v
	}
	required = append(required, extraRequired...)
	return obj(props, required...)
}

// Network capabilities target a (source pod, dest service) pair.
func networkTarget(extra map[string]any, extraRequired ...string) map[string]any {
	props := map[string]any{
		"namespace":      str(1),
		"source_app":     str(1),
		"target_service": str(1),
		"direction":      map[string]any{"type": "string", "enum": []any{"to", "from", "both"}, "default": "to"},
	}
	required := []string{"namespace", "source_app", "target_service"}
	for k, v := range extra {
		props[k] = v
	}
	required = append(required, extraRequired...)
	return obj(props, required...)
}

// HTTP capabilities target a specific endpoint exposed by a pod.
func httpTarget(extra map[string]any, extraRequired ...string) map[string]any {
	props := map[string]any{
		"namespace": str(1),
		"app":       str(1),
		"port":      map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
		"method":    map[string]any{"type": "string", "enum": []any{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE"}},
		"path":      str(1),
	}
	required := []string{"namespace", "app", "port", "method", "path"}
	for k, v := range extra {
		props[k] = v
	}
	required = append(required, extraRequired...)
	return obj(props, required...)
}

// JVM method-level capabilities target a class+method inside a pod.
func jvmMethodTarget(extra map[string]any) map[string]any {
	props := map[string]any{
		"namespace": str(1),
		"app":       str(1),
		"class":     str(1),
		"method":    str(1),
	}
	required := []string{"namespace", "app", "class", "method"}
	for k, v := range extra {
		props[k] = v
	}
	return obj(props, required...)
}

func capabilities() []capability {
	return []capability{
		// ---------- PodChaos ----------
		{
			Name:      "pod_kill",
			ChaosType: "PodKill",
			CRDKind:   "PodChaos",
			Status:    "stable",
			OneLine:   "delete target pods so they restart",
			TargetSchema: podTarget(nil),
			ParamSchema:  obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name": "pod_kill",
				"contract": map[string]any{
					"k8s_assertions": []any{
						map[string]any{"assertion": "target_pod.restart_count increases by >= 1 within injection_window_s"},
					},
					"injection_window_s": 60,
					"tolerance":          map[string]any{"false_positive_rate": 0.05},
				},
			},
			ConformanceProbe: map[string]any{
				"probe":            "k8s.pod.restart_count",
				"expect":           "delta >= 1",
				"window_s":         60,
				"baseline_needed":  false,
			},
		},
		{
			Name:      "pod_failure",
			ChaosType: "PodFailure",
			CRDKind:   "PodChaos",
			Status:    "experimental",
			OneLine:   "force pod into not-ready state for duration",
			TargetSchema: podTarget(nil),
			ParamSchema:  obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name": "pod_failure",
				"contract": map[string]any{
					"k8s_assertions": []any{
						map[string]any{"assertion": "target_pod.ready_condition == false for >= duration_s * 0.9"},
					},
					"injection_window_s": 60,
					"tolerance":          map[string]any{"false_positive_rate": 0.05},
				},
			},
			ConformanceProbe: map[string]any{"probe": "k8s.pod.ready_condition", "expect": "false for >= duration_s * 0.9"},
		},
		{
			Name:      "container_kill",
			ChaosType: "ContainerKill",
			CRDKind:   "PodChaos",
			Status:    "experimental",
			OneLine:   "kill a specific container inside the pod",
			TargetSchema: podTarget(map[string]any{"container": str(1)}, "container"),
			ParamSchema:  obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name": "container_kill",
				"contract": map[string]any{
					"k8s_assertions": []any{
						map[string]any{"assertion": "target_pod.container[name=target.container].restart_count increases by >= 1 within injection_window_s"},
					},
					"injection_window_s": 60,
					"tolerance":          map[string]any{"false_positive_rate": 0.05},
				},
			},
			ConformanceProbe: map[string]any{"probe": "k8s.container.restart_count", "expect": "delta >= 1", "scope": "target.container"},
		},

		// ---------- StressChaos ----------
		{
			Name:      "cpu_stress",
			ChaosType: "CPUStress",
			CRDKind:   "StressChaos",
			Status:    "experimental",
			OneLine:   "burn CPU inside container",
			TargetSchema: podTarget(map[string]any{"container": str(1)}, "container"),
			ParamSchema: obj(map[string]any{
				"duration_s":  durationS(),
				"load_pct":    map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				"workers":     intRange(1, 8, 1),
			}, "load_pct"),
			ObservableContract: map[string]any{
				"name": "cpu_stress",
				"contract": map[string]any{
					"metric_assertions": []any{
						map[string]any{"assertion": "container_cpu_usage_seconds_total rate increases by >= load_pct * 0.5 over baseline"},
					},
					"baseline_window_s":  60,
					"injection_window_s": 60,
					"tolerance":          map[string]any{"false_positive_rate": 0.1},
				},
			},
			ConformanceProbe: map[string]any{"probe": "container.cpu.utilization", "expect": "rate >= baseline + load_pct * 0.5"},
		},
		{
			Name:      "memory_stress",
			ChaosType: "MemoryStress",
			CRDKind:   "StressChaos",
			Status:    "experimental",
			OneLine:   "consume container memory up to size_mib",
			TargetSchema: podTarget(map[string]any{"container": str(1)}, "container"),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"size_mib":   map[string]any{"type": "integer", "minimum": 1, "maximum": 65536},
				"workers":    intRange(1, 8, 1),
			}, "size_mib"),
			ObservableContract: map[string]any{
				"name": "memory_stress",
				"contract": map[string]any{
					"metric_assertions": []any{
						map[string]any{"assertion": "container_memory_working_set_bytes increases by >= size_mib * 0.7 * MiB over baseline"},
					},
					"baseline_window_s":  60,
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "container.memory.working_set", "expect": "delta >= size_mib * 0.7 MiB"},
		},

		// ---------- HTTPChaos ----------
		{
			Name:      "http_request_abort",
			ChaosType: "HTTPRequestAbort",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "abort incoming HTTP requests at the target endpoint",
			TargetSchema: httpTarget(nil),
			ParamSchema:  obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name": "http_request_abort",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.error_rate on target endpoint > 0.9 during injection_window_s"},
					},
					"baseline_window_s":  60,
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.error_rate", "expect": "> 0.9", "scope": "target.endpoint"},
		},
		{
			Name:      "http_response_abort",
			ChaosType: "HTTPResponseAbort",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "abort outgoing HTTP responses at the target endpoint",
			TargetSchema: httpTarget(nil),
			ParamSchema:  obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name": "http_response_abort",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.error_rate on target endpoint > 0.9 during injection_window_s"},
					},
					"baseline_window_s":  60,
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.error_rate", "expect": "> 0.9", "scope": "target.endpoint"},
		},
		{
			Name:      "http_request_delay",
			ChaosType: "HTTPRequestDelay",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "delay incoming HTTP requests at the target endpoint",
			TargetSchema: httpTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"delay_ms":   map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
			}, "delay_ms"),
			ObservableContract: map[string]any{
				"name": "http_request_delay",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.duration on target endpoint >= delay_ms * 0.9"},
						map[string]any{"assertion": "span.status_code unchanged from baseline distribution"},
					},
					"baseline_window_s":  60,
					"injection_window_s": 60,
					"tolerance":          map[string]any{"false_positive_rate": 0.05},
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.duration_ms", "expect": ">= delay_ms * 0.9"},
		},
		{
			Name:      "http_response_delay",
			ChaosType: "HTTPResponseDelay",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "delay outgoing HTTP responses at the target endpoint",
			TargetSchema: httpTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"delay_ms":   map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
			}, "delay_ms"),
			ObservableContract: map[string]any{
				"name": "http_response_delay",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.duration on target endpoint >= delay_ms * 0.9"},
					},
					"baseline_window_s":  60,
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.duration_ms", "expect": ">= delay_ms * 0.9"},
		},
		{
			Name:      "http_response_replace_body",
			ChaosType: "HTTPResponseReplaceBody",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "replace HTTP response body with empty or random bytes",
			TargetSchema: httpTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"body_type":  map[string]any{"type": "string", "enum": []any{"empty", "random"}, "default": "empty"},
			}),
			ObservableContract: map[string]any{
				"name": "http_response_replace_body",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "client_side parse_error_rate on target endpoint > baseline + 0.5"},
					},
					"injection_window_s": 60,
				},
				"note": "TODO: contract is heuristic — parse-error visibility depends on client instrumentation; some clients accept empty body silently",
			},
			ConformanceProbe: map[string]any{"probe": "client.parse_error_rate", "expect": "> baseline + 0.5", "note": "TODO: requires client instrumentation"},
		},
		{
			Name:      "http_response_patch_body",
			ChaosType: "HTTPResponsePatchBody",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "patch HTTP response body with a JSON merge patch",
			TargetSchema: httpTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"patch_json": map[string]any{"type": "string", "minLength": 2, "default": "{\"foo\":\"bar\"}"},
			}),
			ObservableContract: map[string]any{
				"name":     "http_response_patch_body",
				"contract": "TODO: body-patch effect on traces depends on client behaviour; manifestation typically client-side schema error, not directly observable in server spans",
			},
			ConformanceProbe: map[string]any{"probe": "TODO", "note": "body diff is not visible in spans by default; needs response-body capture or downstream error correlation"},
		},
		{
			Name:      "http_request_replace_path",
			ChaosType: "HTTPRequestReplacePath",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "rewrite HTTP request path before it reaches the handler",
			TargetSchema: httpTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"new_path":   str(1),
			}, "new_path"),
			ObservableContract: map[string]any{
				"name": "http_request_replace_path",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.http.route on target endpoint changes to new_path OR error_rate > baseline + 0.5 (handler not found)"},
					},
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.http.route", "expect": "== new_path OR span.error_rate > baseline + 0.5"},
		},
		{
			Name:      "http_request_replace_method",
			ChaosType: "HTTPRequestReplaceMethod",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "rewrite HTTP request method before it reaches the handler",
			TargetSchema: httpTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"new_method": map[string]any{"type": "string", "enum": []any{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}},
			}, "new_method"),
			ObservableContract: map[string]any{
				"name": "http_request_replace_method",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.http.method on target endpoint == new_method OR error_rate > baseline + 0.5 (method not allowed)"},
					},
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.http.method", "expect": "== new_method OR span.status_code in {405, 404}"},
		},
		{
			Name:      "http_response_replace_code",
			ChaosType: "HTTPResponseReplaceCode",
			CRDKind:   "HTTPChaos",
			Status:    "experimental",
			OneLine:   "force HTTP response status code on target endpoint",
			TargetSchema: httpTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s":  durationS(),
				"status_code": map[string]any{"type": "integer", "minimum": 100, "maximum": 599},
			}, "status_code"),
			ObservableContract: map[string]any{
				"name": "http_response_replace_code",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.http.status_code on target endpoint == status_code for >= 90% of spans during injection_window_s"},
					},
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.http.status_code", "expect": "== status_code for >= 0.9 of target-endpoint spans"},
		},

		// ---------- DNSChaos ----------
		{
			Name:      "dns_error",
			ChaosType: "DNSError",
			CRDKind:   "DNSChaos",
			Status:    "experimental",
			OneLine:   "force DNS resolution failures for matching domains",
			TargetSchema: podTarget(map[string]any{
				"domain_patterns": map[string]any{"type": "array", "items": str(1), "minItems": 1},
			}, "domain_patterns"),
			ParamSchema: obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name": "dns_error",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.error_rate on egress to matched domain > baseline + 0.5 (DNS lookup failure)"},
					},
					"injection_window_s": 60,
				},
				"note": "TODO: gRPC-only callers are filtered upstream; DNS resolver caches can mask the effect for >= TTL",
			},
			ConformanceProbe: map[string]any{"probe": "trace.egress.error_rate", "expect": "> baseline + 0.5", "note": "TODO: resolver TTL caveat"},
		},
		{
			Name:      "dns_random",
			ChaosType: "DNSRandom",
			CRDKind:   "DNSChaos",
			Status:    "experimental",
			OneLine:   "return random IPs for matching DNS queries",
			TargetSchema: podTarget(map[string]any{
				"domain_patterns": map[string]any{"type": "array", "items": str(1), "minItems": 1},
			}, "domain_patterns"),
			ParamSchema: obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name":     "dns_random",
				"contract": "TODO: random-IP effect manifests as connection timeouts or wrong-host TLS errors; depends entirely on caller's retry/timeout config and TLS settings",
			},
			ConformanceProbe: map[string]any{"probe": "TODO", "note": "manifestation depends on caller retry/timeout; no single observable holds across stacks"},
		},

		// ---------- TimeChaos ----------
		{
			Name:      "time_skew",
			ChaosType: "TimeSkew",
			CRDKind:   "TimeChaos",
			Status:    "experimental",
			OneLine:   "shift the container's wall clock by offset_s",
			TargetSchema: podTarget(map[string]any{"container": str(1)}, "container"),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"offset_s":   map[string]any{"type": "integer", "minimum": -3600, "maximum": 3600},
			}, "offset_s"),
			ObservableContract: map[string]any{
				"name":     "time_skew",
				"contract": "TODO: time-skew has no universal trace signature; manifestation depends on app's timestamp use (JWT expiry, log skew, scheduled jobs). Best probe is process-level clock comparison via debug exec, not telemetry.",
			},
			ConformanceProbe: map[string]any{"probe": "TODO", "note": "no universal telemetry signal; consider in-container `date` probe or app-specific assertion"},
		},

		// ---------- NetworkChaos ----------
		{
			Name:      "network_delay",
			ChaosType: "NetworkDelay",
			CRDKind:   "NetworkChaos",
			Status:    "experimental",
			OneLine:   "inject network latency between source pod and target service",
			TargetSchema: networkTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s":      durationS(),
				"latency_ms":      map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
				"jitter_ms":       intMin(0),
				"correlation_pct": intRange(0, 100, 0),
			}, "latency_ms"),
			ObservableContract: map[string]any{
				"name": "network_delay",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.duration on cross-service call from source_app to target_service >= latency_ms * 0.5"},
					},
					"baseline_window_s":  60,
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.duration_ms", "expect": ">= latency_ms * 0.5", "scope": "edge(source_app -> target_service)"},
		},
		{
			Name:      "network_loss",
			ChaosType: "NetworkLoss",
			CRDKind:   "NetworkChaos",
			Status:    "experimental",
			OneLine:   "drop packets between source pod and target service",
			TargetSchema: networkTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s":      durationS(),
				"loss_pct":        map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				"correlation_pct": intRange(0, 100, 0),
			}, "loss_pct"),
			ObservableContract: map[string]any{
				"name": "network_loss",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.error_rate or retry_count on edge(source_app -> target_service) > baseline + min(loss_pct/200, 0.3)"},
					},
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.edge.error_or_retry_rate", "expect": "> baseline + loss_pct/200"},
		},
		{
			Name:      "network_duplicate",
			ChaosType: "NetworkDuplicate",
			CRDKind:   "NetworkChaos",
			Status:    "experimental",
			OneLine:   "duplicate packets between source pod and target service",
			TargetSchema: networkTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s":      durationS(),
				"duplicate_pct":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				"correlation_pct": intRange(0, 100, 0),
			}, "duplicate_pct"),
			ObservableContract: map[string]any{
				"name":     "network_duplicate",
				"contract": "TODO: TCP duplicate-ACK handling absorbs most duplicates without symptoms; UDP-heavy traffic shows reorder/dup but spans rarely encode it",
			},
			ConformanceProbe: map[string]any{"probe": "TODO", "note": "no robust trace signal; consider node netdev_tx counters as out-of-band probe"},
		},
		{
			Name:      "network_corrupt",
			ChaosType: "NetworkCorrupt",
			CRDKind:   "NetworkChaos",
			Status:    "experimental",
			OneLine:   "corrupt packet contents between source pod and target service",
			TargetSchema: networkTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s":      durationS(),
				"corrupt_pct":     map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				"correlation_pct": intRange(0, 100, 0),
			}, "corrupt_pct"),
			ObservableContract: map[string]any{
				"name": "network_corrupt",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.error_rate on edge(source_app -> target_service) > baseline + corrupt_pct/300 (TCP checksum-fail retransmits)"},
					},
					"injection_window_s": 60,
				},
				"note": "TODO: TLS-wrapped flows treat corruption as connection reset; raw-TCP flows surface as application-level retry",
			},
			ConformanceProbe: map[string]any{"probe": "trace.edge.error_or_retry_rate", "expect": "> baseline + corrupt_pct/300", "note": "TODO: TLS vs plain TCP"},
		},
		{
			Name:      "network_bandwidth",
			ChaosType: "NetworkBandwidth",
			CRDKind:   "NetworkChaos",
			Status:    "experimental",
			OneLine:   "cap bandwidth on the edge from source pod to target service",
			TargetSchema: networkTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"rate_kbps":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000},
				"limit":      intMin(1),
				"buffer":     intMin(1),
			}, "rate_kbps"),
			ObservableContract: map[string]any{
				"name": "network_bandwidth",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.duration on cross-service call increases proportional to payload_bytes / rate_kbps"},
					},
					"injection_window_s": 60,
				},
				"note": "TODO: only payloads larger than buffer size will exhibit the cap; small RPC bodies pass through unaffected",
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.duration_ms", "expect": ">= 8 * payload_bytes / rate_kbps", "note": "TODO: large-payload-only"},
		},
		{
			Name:      "network_partition",
			ChaosType: "NetworkPartition",
			CRDKind:   "NetworkChaos",
			Status:    "experimental",
			OneLine:   "cut all packets between source pod and target service",
			TargetSchema: networkTarget(nil),
			ParamSchema:  obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name": "network_partition",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span.error_rate on edge(source_app -> target_service) > 0.9 during injection_window_s"},
					},
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.edge.error_rate", "expect": "> 0.9"},
		},

		// ---------- JVMChaos ----------
		{
			Name:      "jvm_method_latency",
			ChaosType: "JVMLatency",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "delay JVM method invocation",
			TargetSchema: jvmMethodTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"delay_ms":   map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
			}, "delay_ms"),
			ObservableContract: map[string]any{
				"name": "jvm_method_latency",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span representing method invocation has duration >= delay_ms * 0.9"},
					},
					"injection_window_s": 60,
				},
				"note": "TODO: only methods that produce or are wrapped by an OTel/Jaeger span are observable; many internal helpers won't surface",
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.duration_ms", "expect": ">= delay_ms * 0.9", "scope": "span(class.method)", "note": "TODO: needs span coverage on target method"},
		},
		{
			Name:      "jvm_method_return",
			ChaosType: "JVMReturn",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "override JVM method return value",
			TargetSchema: jvmMethodTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s":  durationS(),
				"return_type": map[string]any{"type": "string", "enum": []any{"string", "int"}},
				"value_mode":  map[string]any{"type": "string", "enum": []any{"default", "random"}, "default": "default"},
			}, "return_type"),
			ObservableContract: map[string]any{
				"name":     "jvm_method_return",
				"contract": "TODO: return-value override has no universal signature; downstream effect depends on whether callers validate the value (NPE, business error, silent corruption)",
			},
			ConformanceProbe: map[string]any{"probe": "TODO", "note": "no universal signal; needs app-specific assertion (e.g. order_status downstream change)"},
		},
		{
			Name:      "jvm_method_exception",
			ChaosType: "JVMException",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "throw exception from JVM method",
			TargetSchema: jvmMethodTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s":     durationS(),
				"exception_mode": map[string]any{"type": "string", "enum": []any{"default", "random"}, "default": "default"},
			}),
			ObservableContract: map[string]any{
				"name": "jvm_method_exception",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "span(class.method) error_rate > baseline + 0.5 during injection_window_s"},
					},
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.span.error_rate", "expect": "> baseline + 0.5", "scope": "span(class.method)"},
		},
		{
			Name:      "jvm_gc",
			ChaosType: "JVMGarbageCollector",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "force JVM full GC at the target pod",
			TargetSchema: podTarget(nil),
			ParamSchema:  obj(map[string]any{"duration_s": durationS()}),
			ObservableContract: map[string]any{
				"name": "jvm_gc",
				"contract": map[string]any{
					"metric_assertions": []any{
						map[string]any{"assertion": "jvm_gc_collection_seconds_count increases by >= 1 within injection_window_s"},
					},
					"injection_window_s": 60,
				},
				"note": "TODO: requires JVM metrics exporter (Micrometer/JMX); without it, fall back to latency spike on co-located spans",
			},
			ConformanceProbe: map[string]any{"probe": "jvm.gc.collection_count", "expect": "delta >= 1", "note": "TODO: needs JVM metrics exporter"},
		},
		{
			Name:      "jvm_cpu_stress",
			ChaosType: "JVMCPUStress",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "spawn CPU-bound threads inside the JVM at a method",
			TargetSchema: jvmMethodTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s": durationS(),
				"cpu_count":  intRange(1, 16, 1),
			}, "cpu_count"),
			ObservableContract: map[string]any{
				"name": "jvm_cpu_stress",
				"contract": map[string]any{
					"metric_assertions": []any{
						map[string]any{"assertion": "container_cpu_usage_seconds_total rate increases by >= cpu_count * 0.5 cores"},
					},
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "container.cpu.utilization", "expect": "delta >= cpu_count * 0.5 cores"},
		},
		{
			Name:      "jvm_memory_stress",
			ChaosType: "JVMMemoryStress",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "consume JVM heap or stack memory at a method",
			TargetSchema: jvmMethodTarget(nil),
			ParamSchema: obj(map[string]any{
				"duration_s":  durationS(),
				"memory_type": map[string]any{"type": "string", "enum": []any{"heap", "stack"}, "default": "heap"},
			}),
			ObservableContract: map[string]any{
				"name": "jvm_memory_stress",
				"contract": map[string]any{
					"metric_assertions": []any{
						map[string]any{"assertion": "jvm_memory_used_bytes{area=heap|nonheap} increases substantially over baseline"},
					},
					"injection_window_s": 60,
				},
				"note": "TODO: stack-memory case typically surfaces as StackOverflowError; covered by span error_rate, not memory metric",
			},
			ConformanceProbe: map[string]any{"probe": "jvm.memory.used_bytes", "expect": "delta > 0.5 * baseline", "note": "TODO: stack mode → span error_rate instead"},
		},
		{
			Name:      "jvm_mysql_latency",
			ChaosType: "JVMMySQLLatency",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "delay MySQL queries from JVM (Connector/J interceptor)",
			TargetSchema: jvmMethodTarget(map[string]any{
				"db_name":   str(1),
				"table":     str(1),
				"sql_type":  map[string]any{"type": "string", "enum": []any{"all", "select", "insert", "update", "delete", "replace"}, "default": "all"},
			}),
			ParamSchema: obj(map[string]any{
				"duration_s":      durationS(),
				"delay_ms":        map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
				"mysql_connector": map[string]any{"type": "string", "enum": []any{"5", "8"}, "default": "8"},
			}, "delay_ms"),
			ObservableContract: map[string]any{
				"name": "jvm_mysql_latency",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "db span on (db_name, table, sql_type) duration >= delay_ms * 0.9"},
					},
					"injection_window_s": 60,
				},
				"note": "TODO: only effective on Connector/J-instrumented apps; depends on db.statement attribute coverage in traces",
			},
			ConformanceProbe: map[string]any{"probe": "trace.db.span.duration_ms", "expect": ">= delay_ms * 0.9", "scope": "db.statement matches sql_type"},
		},
		{
			Name:      "jvm_mysql_exception",
			ChaosType: "JVMMySQLException",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "throw SQLException from MySQL queries (Connector/J interceptor)",
			TargetSchema: jvmMethodTarget(map[string]any{
				"db_name":  str(1),
				"table":    str(1),
				"sql_type": map[string]any{"type": "string", "enum": []any{"all", "select", "insert", "update", "delete", "replace"}, "default": "all"},
			}),
			ParamSchema: obj(map[string]any{
				"duration_s":      durationS(),
				"mysql_connector": map[string]any{"type": "string", "enum": []any{"5", "8"}, "default": "8"},
			}),
			ObservableContract: map[string]any{
				"name": "jvm_mysql_exception",
				"contract": map[string]any{
					"trace_assertions": []any{
						map[string]any{"assertion": "db span on (db_name, table, sql_type) error_rate > baseline + 0.8"},
					},
					"injection_window_s": 60,
				},
			},
			ConformanceProbe: map[string]any{"probe": "trace.db.span.error_rate", "expect": "> baseline + 0.8"},
		},
		{
			Name:      "jvm_runtime_mutator",
			ChaosType: "JVMRuntimeMutator",
			CRDKind:   "JVMChaos",
			Status:    "experimental",
			OneLine:   "mutate JVM method body (constant/operator/string rewrite)",
			TargetSchema: jvmMethodTarget(map[string]any{
				"mutation_type": map[string]any{"type": "string", "enum": []any{"constant", "operator", "string"}},
			}),
			ParamSchema: obj(map[string]any{
				"duration_s":        durationS(),
				"mutation_from":     map[string]any{"type": "string"},
				"mutation_to":       map[string]any{"type": "string"},
				"mutation_strategy": map[string]any{"type": "string"},
			}),
			ObservableContract: map[string]any{
				"name":     "jvm_runtime_mutator",
				"contract": "TODO: mutator effect is by definition app-semantic (changed constant, flipped operator, swapped string); no generic trace assertion holds. Use sn/ts/teastore-specific functional checks.",
			},
			ConformanceProbe: map[string]any{"probe": "TODO", "note": "app-semantic mutation; no universal probe — needs per-mutation contract"},
		},
	}
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func writeMarkdown(path string, caps []capability) error {
	var sb strings.Builder
	sb.WriteString("# aegis-chaos Capability Set\n\n")
	sb.WriteString("Generated by `aegislab/tools/capgen`. Do not edit by hand — re-run `just capgen`.\n\n")
	sb.WriteString("Source: `chaos-experiment/handler/handler.go` `ChaosTypeMap`.\n\n")
	sb.WriteString(fmt.Sprintf("Total capabilities: **%d**.\n\n", len(caps)))
	sb.WriteString("| name | chaos_type | crd_kind | status | summary |\n")
	sb.WriteString("|------|-----------|----------|--------|---------|\n")
	for _, c := range caps {
		todo := ""
		if isTODO(c) {
			todo = " *(TODO contract)*"
		}
		sb.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | %s | %s%s |\n",
			c.Name, c.ChaosType, c.CRDKind, c.Status, c.OneLine, todo))
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

func isTODO(c capability) bool {
	if s, ok := c.ObservableContract["contract"].(string); ok && strings.HasPrefix(s, "TODO") {
		return true
	}
	if _, ok := c.ObservableContract["note"].(string); ok {
		if s, _ := c.ObservableContract["note"].(string); strings.HasPrefix(s, "TODO") {
			return true
		}
	}
	if s, ok := c.ConformanceProbe["probe"].(string); ok && s == "TODO" {
		return true
	}
	return false
}

func main() {
	outDir := "output"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}

	caps := capabilities()
	sort.Slice(caps, func(i, j int) bool { return caps[i].Name < caps[j].Name })

	// Slim type for capabilities.json — drop the table-only OneLine + ConformanceProbe.
	type capDTO struct {
		Name               string         `json:"name"`
		ChaosType          string         `json:"chaos_type"`
		CRDKind            string         `json:"crd_kind"`
		Status             string         `json:"status"`
		TargetSchema       map[string]any `json:"target_schema"`
		ParamSchema        map[string]any `json:"param_schema"`
		ObservableContract map[string]any `json:"observable_contract"`
	}
	dtos := make([]capDTO, len(caps))
	for i, c := range caps {
		dtos[i] = capDTO{
			Name:               c.Name,
			ChaosType:          c.ChaosType,
			CRDKind:            c.CRDKind,
			Status:             c.Status,
			TargetSchema:       c.TargetSchema,
			ParamSchema:        c.ParamSchema,
			ObservableContract: c.ObservableContract,
		}
	}

	type probeDTO struct {
		Name             string         `json:"name"`
		ChaosType        string         `json:"chaos_type"`
		ConformanceProbe map[string]any `json:"conformance_probe"`
	}
	probes := make([]probeDTO, len(caps))
	for i, c := range caps {
		probes[i] = probeDTO{Name: c.Name, ChaosType: c.ChaosType, ConformanceProbe: c.ConformanceProbe}
	}

	if err := writeJSON(filepath.Join(outDir, "capabilities.json"), dtos); err != nil {
		fmt.Fprintln(os.Stderr, "capabilities.json:", err)
		os.Exit(1)
	}
	if err := writeJSON(filepath.Join(outDir, "conformance_cases.json"), probes); err != nil {
		fmt.Fprintln(os.Stderr, "conformance_cases.json:", err)
		os.Exit(1)
	}
	if err := writeMarkdown(filepath.Join(outDir, "capabilities.md"), caps); err != nil {
		fmt.Fprintln(os.Stderr, "capabilities.md:", err)
		os.Exit(1)
	}

	todoCount := 0
	for _, c := range caps {
		if isTODO(c) {
			todoCount++
		}
	}
	fmt.Printf("capgen: wrote %d capabilities (%d with TODO observable contract) to %s/\n",
		len(caps), todoCount, outDir)
}
