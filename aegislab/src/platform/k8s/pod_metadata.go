package k8s

import "aegis/platform/consts"

// InjectAegisPodMetadata stamps the trace identity onto a Pod template's
// annotation + label maps so the node-local OTel collector's k8sattributes
// processor can copy them onto every log line the Pod emits to stdout/stderr.
//
// The maps are mutated in place; nil maps are reallocated. Existing keys are
// preserved on collision — the orchestrator already injects W3C trace
// carriers under `consts.TaskCarrier` / `consts.TraceCarrier`, and those
// must stay intact for the receiver-side parser in
// `core/orchestrator/logreceiver`.
//
// Pass an empty `component` to skip the component label (useful for tests).
func InjectAegisPodMetadata(annotations, labels map[string]string, traceID, component string) (map[string]string, map[string]string) {
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	if labels == nil {
		labels = make(map[string]string, 1)
	}
	if traceID != "" {
		if _, ok := annotations[consts.AegisPodAnnotationTraceID]; !ok {
			annotations[consts.AegisPodAnnotationTraceID] = traceID
		}
	}
	if component != "" {
		if _, ok := labels[consts.AegisPodLabelComponent]; !ok {
			labels[consts.AegisPodLabelComponent] = component
		}
	}
	return annotations, labels
}
