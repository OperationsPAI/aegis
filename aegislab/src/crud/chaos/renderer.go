package chaos

import (
	"fmt"
	"sort"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SystemContext carries the per-system knobs a Renderer needs at apply
// time. Today only AppLabelKey — chaos-mesh selector labels must use the
// real key in the target cluster (commonly app.kubernetes.io/name)
// rather than a hardcoded "app".
type SystemContext struct {
	Name        string
	AppLabelKey string
}

// LabelKey returns AppLabelKey or the historical "app" default.
func (s SystemContext) LabelKey() string {
	if s.AppLabelKey != "" {
		return s.AppLabelKey
	}
	return "app"
}

// Renderer turns one Capability into a Chaos-Mesh CR. Lives behind a
// registry so executor_chaosmesh.go can dispatch by capability name
// without growing a switch for every new family.
type Renderer interface {
	Capability() string
	Maturity() string
	HandlePrefix() string
	GVR() schema.GroupVersionResource
	// ValidateForHandle checks only the target fields DeriveHandle needs
	// (namespace — the CR name encodes nothing else). Kept distinct from
	// ValidateTarget so the service can derive + persist the handle
	// BEFORE the full Apply-time validation runs (ADR-0004's
	// "handle exists ⇒ row is recoverable" guarantee).
	ValidateForHandle(target map[string]any) error
	ValidateTarget(target map[string]any) error
	ValidateParams(params map[string]any) error
	RenderCR(sysCtx SystemContext, name, namespace string, target, params map[string]any) (*unstructured.Unstructured, error)
}

var (
	rendererMu sync.RWMutex
	rendererRegistry = map[string]Renderer{}
)

// RegisterRenderer wires a Renderer in at init() time. Panics on
// duplicate registration — the registry is process-global state and a
// silent overwrite would mask a real bug.
func RegisterRenderer(r Renderer) {
	rendererMu.Lock()
	defer rendererMu.Unlock()
	cap := r.Capability()
	if cap == "" {
		panic("chaos: RegisterRenderer with empty capability")
	}
	if _, dup := rendererRegistry[cap]; dup {
		panic(fmt.Sprintf("chaos: duplicate renderer for capability %q", cap))
	}
	rendererRegistry[cap] = r
}

func lookupRenderer(capability string) (Renderer, error) {
	rendererMu.RLock()
	defer rendererMu.RUnlock()
	r, ok := rendererRegistry[capability]
	if !ok {
		return nil, fmt.Errorf("chaos-mesh executor: unsupported capability %q", capability)
	}
	return r, nil
}

func registeredCapabilities() []CapabilitySupport {
	rendererMu.RLock()
	defer rendererMu.RUnlock()
	out := make([]CapabilitySupport, 0, len(rendererRegistry))
	for _, r := range rendererRegistry {
		out = append(out, CapabilitySupport{Capability: r.Capability(), Maturity: r.Maturity()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Capability < out[j].Capability })
	return out
}
