package chaos

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"
)

func init() {
	for _, action := range httpActions {
		RegisterRenderer(httpRenderer{action: action})
	}
}

// httpActions enumerates the 9 §11 step-2 HTTP capabilities. The first
// four target the Request side of the proxy, the rest target Response.
var httpActions = []string{
	"request_abort",
	"request_delay",
	"request_replace_method",
	"request_replace_path",
	"response_abort",
	"response_delay",
	"response_patch_body",
	"response_replace_body",
	"response_replace_code",
}

var httpChaosGVR = schema.GroupVersionResource{
	Group: ChaosMeshGroup, Version: ChaosMeshVersion, Resource: "httpchaos",
}

func ChaosMeshGroupVersionResourceForHTTPChaos() schema.GroupVersionResource {
	return httpChaosGVR
}

type httpRenderer struct {
	action string
}

func (r httpRenderer) Capability() string { return "http_" + r.action }

func (r httpRenderer) Maturity() string { return CapExperimental }

func (r httpRenderer) HandlePrefix() string {
	switch r.action {
	case "request_abort":
		return "aegis-httpreqabort"
	case "request_delay":
		return "aegis-httpreqdelay"
	case "request_replace_method":
		return "aegis-httpreqmethod"
	case "request_replace_path":
		return "aegis-httpreqpath"
	case "response_abort":
		return "aegis-httprespabort"
	case "response_delay":
		return "aegis-httprespdelay"
	case "response_patch_body":
		return "aegis-httprespatch"
	case "response_replace_body":
		return "aegis-httprespbody"
	case "response_replace_code":
		return "aegis-httprespcode"
	}
	return "aegis-http"
}

func (r httpRenderer) GVR() schema.GroupVersionResource { return httpChaosGVR }

func (r httpRenderer) ValidateForHandle(target map[string]any) error {
	if ns, _ := target["namespace"].(string); ns == "" {
		return fmt.Errorf("chaos-mesh %s: target.namespace is required", r.Capability())
	}
	return nil
}

func (r httpRenderer) ValidateTarget(target map[string]any) error {
	cap := r.Capability()
	for _, k := range []string{"namespace", "app", "method", "path"} {
		v, _ := target[k].(string)
		if v == "" {
			return fmt.Errorf("chaos-mesh %s: target.%s is required", cap, k)
		}
	}
	// chaos-mesh HTTPChaosSpec.Port is non-optional (no omitempty on
	// int32); the webhook rejects port=0 as invalid.
	port, err := getInt(target, "port")
	if err != nil {
		return fmt.Errorf("chaos-mesh %s: target.port is required: %w", cap, err)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("chaos-mesh %s: target.port must be 1..65535, got %d", cap, port)
	}
	return nil
}

func (r httpRenderer) ValidateParams(params map[string]any) error {
	cap := r.Capability()
	switch r.action {
	case "request_delay", "response_delay":
		if _, err := getInt(params, "delay_ms"); err != nil {
			return fmt.Errorf("chaos-mesh %s: params.delay_ms is required: %w", cap, err)
		}
	case "request_replace_method":
		if v, _ := params["new_method"].(string); v == "" {
			return fmt.Errorf("chaos-mesh %s: params.new_method is required", cap)
		}
	case "request_replace_path":
		if v, _ := params["new_path"].(string); v == "" {
			return fmt.Errorf("chaos-mesh %s: params.new_path is required", cap)
		}
	case "response_replace_code":
		code, err := getInt(params, "status_code")
		if err != nil {
			return fmt.Errorf("chaos-mesh %s: params.status_code is required: %w", cap, err)
		}
		if code < 100 || code > 599 {
			return fmt.Errorf("chaos-mesh %s: params.status_code must be 100..599, got %d", cap, code)
		}
	case "response_replace_body":
		if v, ok := params["body_type"]; ok {
			s, _ := v.(string)
			if s != "empty" && s != "random" {
				return fmt.Errorf("chaos-mesh %s: params.body_type must be \"empty\" or \"random\", got %q", cap, s)
			}
		}
	case "response_patch_body":
		if v, ok := params["patch_json"]; ok {
			s, _ := v.(string)
			if !json.Valid([]byte(s)) {
				return fmt.Errorf("chaos-mesh %s: params.patch_json must be valid JSON", cap)
			}
		}
	case "request_abort", "response_abort":
		// no required params
	}
	return nil
}

func (r httpRenderer) RenderCR(sysCtx SystemContext, name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error) {
	app, _ := target["app"].(string)
	method, _ := target["method"].(string)
	path, _ := target["path"].(string)
	port, _ := getInt(target, "port")

	spec := map[string]any{
		"mode": "all",
		"selector": map[string]any{
			"namespaces": []any{namespace},
			"labelSelectors": map[string]any{
				sysCtx.LabelKey(): app,
			},
		},
		"target": r.specTarget(),
		"port":   int64(port),
		"method": method,
		"path":   path,
	}

	if d := durationFromParams(params); d != "" {
		spec["duration"] = d
	}

	if err := r.attachAction(spec, params); err != nil {
		return nil, err
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ChaosMeshGroup + "/" + ChaosMeshVersion,
		"kind":       "HTTPChaos",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "aegis-chaos",
				"aegis-chaos/capability":       r.Capability(),
			},
		},
		"spec": spec,
	}}, nil
}

func (r httpRenderer) specTarget() string {
	if r.action == "request_abort" || r.action == "request_delay" ||
		r.action == "request_replace_method" || r.action == "request_replace_path" {
		return "Request"
	}
	return "Response"
}

// attachAction writes the PodHttpChaosActions sub-field onto spec. The
// PodHttpChaosActions struct is JSON-inlined into HTTPChaosSpec, so
// abort/delay/replace/patch sit at spec top level (not under "actions").
func (r httpRenderer) attachAction(spec map[string]any, params map[string]any) error {
	switch r.action {
	case "request_abort", "response_abort":
		spec["abort"] = true
	case "request_delay", "response_delay":
		ms, _ := getInt(params, "delay_ms")
		spec["delay"] = fmt.Sprintf("%dms", ms)
	case "request_replace_method":
		m, _ := params["new_method"].(string)
		spec["replace"] = map[string]any{"method": m}
	case "request_replace_path":
		p, _ := params["new_path"].(string)
		spec["replace"] = map[string]any{"path": p}
	case "response_replace_code":
		code, _ := getInt(params, "status_code")
		spec["replace"] = map[string]any{"code": int64(code)}
	case "response_replace_body":
		bodyType, _ := params["body_type"].(string)
		if bodyType == "" {
			bodyType = "empty"
		}
		var body []byte
		if bodyType == "random" {
			body = []byte(rand.String(6))
		} else {
			body = []byte{}
		}
		spec["replace"] = map[string]any{"body": body}
	case "response_patch_body":
		patch, _ := params["patch_json"].(string)
		if patch == "" {
			patch = `{"foo":"bar"}`
		}
		spec["patch"] = map[string]any{
			"body": map[string]any{
				"type":  "JSON",
				"value": patch,
			},
		}
	}
	return nil
}
