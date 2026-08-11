package k8s

import (
	"context"
	"fmt"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ListChaosCRs returns every namespaced chaos-mesh.org CR (across all
// namespaces if `namespace` is empty, otherwise scoped to that ns), projected
// to the narrow ChaosCRRef shape the orphan predicate consumes.
//
// `includeKinds`, when non-empty, restricts the returned set to GVR.Resource
// values matching one of the given lowercased plurals (e.g. "podchaos",
// "networkchaos"). Mismatches are silently skipped. Pass nil/empty to list
// every discovered chaos-mesh.org namespaced GVR.
//
// Per-GVR list errors are surfaced as warnings; the call returns whatever it
// could enumerate. This mirrors ListNamespaceChaosResources' contract — chaos
// CRD presence is environment-dependent and one bad kind must not abort the
// whole sweep.
func ListChaosCRs(ctx context.Context, namespace string, includeKinds []string) ([]ChaosCRRef, []error) {
	client, err := getK8sClient()
	if err != nil {
		return nil, []error{k8sClientNotAvailableErr(err)}
	}
	dyn, err := getK8sDynamicClient()
	if err != nil {
		return nil, []error{fmt.Errorf("kubernetes dynamic client not available: %w", err)}
	}
	return listChaosCRsWith(ctx, &discoveryChaosLister{client: client.Discovery()}, dyn, namespace, includeKinds)
}

func listChaosCRsWith(
	ctx context.Context,
	lister chaosResourceLister,
	dyn dynamic.Interface,
	namespace string,
	includeKinds []string,
) ([]ChaosCRRef, []error) {
	if lister == nil || dyn == nil {
		return nil, []error{fmt.Errorf("chaos list misconfigured: lister or dynamic client nil")}
	}
	gvrs, err := lister.NamespacedChaosGVRs(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("discover chaos-mesh CRDs: %w", err)}
	}
	if len(gvrs) == 0 {
		return nil, nil
	}

	wanted := map[string]struct{}{}
	for _, k := range includeKinds {
		wanted[strings.ToLower(strings.TrimSpace(k))] = struct{}{}
	}

	var (
		out      []ChaosCRRef
		warnings []error
	)
	for _, gvr := range gvrs {
		if gvr.Group != ChaosMeshAPIGroup {
			warnings = append(warnings, fmt.Errorf("refusing to list non-chaos GVR %s", gvr.String()))
			continue
		}
		if len(wanted) > 0 {
			if _, ok := wanted[gvr.Resource]; !ok {
				continue
			}
		}
		raw, lerr := dyn.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if lerr != nil {
			if k8serrors.IsNotFound(lerr) {
				continue
			}
			warnings = append(warnings, fmt.Errorf("list %s in %q: %w", gvr.Resource, namespace, lerr))
			continue
		}
		for idx := range raw.Items {
			item := raw.Items[idx]
			out = append(out, ChaosCRRef{
				Kind:              item.GetKind(),
				Resource:          gvr.Resource,
				Namespace:         item.GetNamespace(),
				Name:              item.GetName(),
				Labels:            item.GetLabels(),
				CreationTimestamp: item.GetCreationTimestamp().Time,
			})
		}
	}
	return out, warnings
}

// DeleteChaosCR force-deletes a single chaos-mesh.org CR identified by its
// GVR + namespace + name, stripping finalizers first so a stuck reconciler
// can't keep the object alive. NotFound is treated as success — concurrent
// reaping is harmless. Refuses any GVR outside chaos-mesh.org as a
// defense-in-depth guard.
func DeleteChaosCR(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) error {
	if gvr.Group != ChaosMeshAPIGroup {
		return fmt.Errorf("refusing to delete non-chaos GVR %s", gvr.String())
	}
	dyn, err := getK8sDynamicClient()
	if err != nil {
		return fmt.Errorf("kubernetes dynamic client not available: %w", err)
	}
	got, gerr := dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if gerr != nil {
		if k8serrors.IsNotFound(gerr) {
			return nil
		}
		return fmt.Errorf("get %s/%s/%s: %w", gvr.Resource, namespace, name, gerr)
	}
	return cleanupChaosInstanceWithRetry(ctx, dyn, gvr, namespace, name, got.GetFinalizers())
}

// ChaosResourceGVR resolves a chaos-mesh.org resource plural (e.g. "podchaos")
// to its discovered GVR. Returns (gvr, true) on hit, zero-value + false when
// the resource is not registered in the cluster.
func ChaosResourceGVR(ctx context.Context, resource string) (schema.GroupVersionResource, bool, error) {
	client, err := getK8sClient()
	if err != nil {
		return schema.GroupVersionResource{}, false, k8sClientNotAvailableErr(err)
	}
	gvrs, err := (&discoveryChaosLister{client: client.Discovery()}).NamespacedChaosGVRs(ctx)
	if err != nil {
		return schema.GroupVersionResource{}, false, err
	}
	resource = strings.ToLower(strings.TrimSpace(resource))
	for _, g := range gvrs {
		if g.Resource == resource {
			return g, true, nil
		}
	}
	return schema.GroupVersionResource{}, false, nil
}

// ListChaosCRs is the Gateway-method form of the top-level ListChaosCRs. See
// that function's doc for semantics.
func (g *Gateway) ListChaosCRs(ctx context.Context, namespace string, includeKinds []string) ([]ChaosCRRef, []error) {
	return ListChaosCRs(ctx, namespace, includeKinds)
}

// DeleteChaosCR is the Gateway-method form of the top-level DeleteChaosCR.
func (g *Gateway) DeleteChaosCR(ctx context.Context, resource, namespace, name string) error {
	gvr, ok, err := ChaosResourceGVR(ctx, resource)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("chaos resource %q not registered in cluster", resource)
	}
	return DeleteChaosCR(ctx, gvr, namespace, name)
}
