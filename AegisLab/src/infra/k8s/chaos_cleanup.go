package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

// ChaosMeshAPIGroup is the API group owning every chaos resource we care
// about (HTTPChaos, NetworkChaos, PodHttpChaos, …). Used as the scope filter
// when stripping finalizers — we must NEVER patch a resource outside this
// group.
const ChaosMeshAPIGroup = "chaos-mesh.org"

// chaos-controller-manager reconciles every ~12s and re-adds the
// `chaos-mesh/records` finalizer; a single strip+delete pass can lose that
// race when there are many zombies (observed on byte-cluster with 367 stuck
// HTTPChaos from old regression runs). Retry the per-CR cleanup a few times
// to give the strip+delete a fighting chance against a hot reconciler.
//
// These knobs are deliberately internal — bumping them is a one-line edit.
const (
	chaosCleanupMaxAttempts = 3
	chaosCleanupRetryDelay  = 200 * time.Millisecond
)

// chaosResourceLister abstracts the discovery surface so unit tests can stub
// the namespaced GVR list without standing up an apiserver.
type chaosResourceLister interface {
	NamespacedChaosGVRs(ctx context.Context) ([]schema.GroupVersionResource, error)
}

// discoveryChaosLister discovers namespaced chaos-mesh.org resources via the
// kube discovery API. Subresources (containing "/" in Name) and cluster-
// scoped kinds (Schedule/Workflow/RemoteCluster as of v2.7) are skipped.
type discoveryChaosLister struct {
	client discovery.DiscoveryInterface
}

func (d *discoveryChaosLister) NamespacedChaosGVRs(ctx context.Context) ([]schema.GroupVersionResource, error) {
	if d == nil || d.client == nil {
		return nil, fmt.Errorf("discovery client not available")
	}
	groups, err := d.client.ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("list api groups: %w", err)
	}

	var versions []string
	for i := range groups.Groups {
		g := groups.Groups[i]
		if g.Name != ChaosMeshAPIGroup {
			continue
		}
		for _, v := range g.Versions {
			versions = append(versions, v.GroupVersion)
		}
	}
	if len(versions) == 0 {
		return nil, nil
	}

	seen := make(map[schema.GroupVersionResource]struct{})
	var out []schema.GroupVersionResource
	for _, gv := range versions {
		parsed, perr := schema.ParseGroupVersion(gv)
		if perr != nil {
			continue
		}
		rl, err := d.client.ServerResourcesForGroupVersion(gv)
		if err != nil {
			// One bad version shouldn't bring the whole cleanup down — chaos
			// CRDs are sometimes mid-rollout when discovery races install.
			logrus.WithError(err).Warnf("chaos-cleanup: discovery for %s failed; skipping", gv)
			continue
		}
		for _, r := range rl.APIResources {
			if !r.Namespaced {
				continue
			}
			// Skip subresources like "httpchaos/status" — those are not
			// directly listable/deletable through the dynamic client.
			if strings.Contains(r.Name, "/") {
				continue
			}
			if !verbsContain(r.Verbs, "list") || !verbsContain(r.Verbs, "delete") {
				continue
			}
			gvr := schema.GroupVersionResource{
				Group:    parsed.Group,
				Version:  parsed.Version,
				Resource: r.Name,
			}
			if _, ok := seen[gvr]; ok {
				continue
			}
			seen[gvr] = struct{}{}
			out = append(out, gvr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out, nil
}

func verbsContain(verbs metav1.Verbs, target string) bool {
	for _, v := range verbs {
		if v == target {
			return true
		}
	}
	return false
}

// CleanupNamespaceChaosResources reaps every chaos-mesh.org namespaced CR
// that lives in `namespace`. For each instance: finalizers are merge-patched
// to [] (so a stuck reconciler can never block deletion) and the object is
// then force-deleted with GracePeriodSeconds=0.
//
// This is the namespace-scoped pre-helm cleanup invoked from RestartPedestal:
// any zombie HTTPChaos / PodHttpChaos / etc. left behind by an earlier round
// would otherwise re-attach to (or interfere with) freshly-created pods. The
// implementation is deliberately scoped to the chaos-mesh.org API group so
// that no other CRD's finalizers are ever touched.
//
// Returns a per-resource reap count and a (possibly empty) slice of warnings
// — callers should treat warnings as best-effort: chaos-CR cleanup MUST NOT
// block helm restart.
func CleanupNamespaceChaosResources(ctx context.Context, namespace string) (map[string]int, []error) {
	client, err := getK8sClient()
	if err != nil {
		return nil, []error{k8sClientNotAvailableErr(err)}
	}
	dyn, err := getK8sDynamicClient()
	if err != nil {
		return nil, []error{fmt.Errorf("kubernetes dynamic client not available: %w", err)}
	}
	return cleanupNamespaceChaosResourcesWith(ctx, &discoveryChaosLister{client: client.Discovery()}, dyn, namespace)
}

// cleanupNamespaceChaosResourcesWith is the test-injectable core. The lister
// and dynamic client come in as parameters so tests can drive the logic
// against an in-memory fake; the production path wires them from the lazy
// gateway state.
func cleanupNamespaceChaosResourcesWith(
	ctx context.Context,
	lister chaosResourceLister,
	dyn dynamic.Interface,
	namespace string,
) (map[string]int, []error) {
	if namespace == "" {
		return nil, []error{fmt.Errorf("namespace must be non-empty")}
	}
	if lister == nil || dyn == nil {
		return nil, []error{fmt.Errorf("chaos cleanup misconfigured: lister or dynamic client nil")}
	}

	gvrs, err := lister.NamespacedChaosGVRs(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("discover chaos-mesh CRDs: %w", err)}
	}
	if len(gvrs) == 0 {
		// chaos-mesh not installed (or not yet discovered) — nothing to do.
		return map[string]int{}, nil
	}

	summary := make(map[string]int, len(gvrs))
	var warnings []error
	for _, gvr := range gvrs {
		// Defense-in-depth: refuse to touch anything outside chaos-mesh.org.
		if gvr.Group != ChaosMeshAPIGroup {
			warnings = append(warnings, fmt.Errorf("refusing to clean non-chaos GVR %s", gvr.String()))
			continue
		}

		list, err := dyn.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			warnings = append(warnings, fmt.Errorf("list %s in %s: %w", gvr.Resource, namespace, err))
			continue
		}

		for idx := range list.Items {
			item := list.Items[idx]
			if err := cleanupChaosInstanceWithRetry(ctx, dyn, gvr, namespace, item.GetName(), item.GetFinalizers()); err != nil {
				warnings = append(warnings, fmt.Errorf("clean %s/%s/%s: %w", gvr.Resource, namespace, item.GetName(), err))
				continue
			}
			summary[gvr.Resource]++
		}
	}
	return summary, warnings
}

// cleanupChaosInstanceWithRetry runs strip-finalizers + delete in a short
// retry loop, re-checking via GET each iteration. Necessary because
// chaos-controller-manager reconciles ~every 12s and re-adds
// `chaos-mesh/records` to live CRs; a single pass loses that race when
// many zombies are queued. Best-effort: after maxAttempts we log a warn and
// move on rather than blocking the caller (helm restart MUST proceed).
//
// The retry is scoped to a single CR — the outer GVR/instance loop is
// unchanged. The chaos-mesh.org guard inside stripFinalizersAndDelete still
// fires every iteration as a defense-in-depth check.
func cleanupChaosInstanceWithRetry(
	ctx context.Context,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, name string,
	initialFinalizers []string,
) error {
	if gvr.Group != ChaosMeshAPIGroup {
		return fmt.Errorf("cleanupChaosInstanceWithRetry: refusing GVR outside %s: %s", ChaosMeshAPIGroup, gvr.String())
	}

	finalizers := initialFinalizers
	for attempt := 0; attempt < chaosCleanupMaxAttempts; attempt++ {
		if err := stripFinalizersAndDelete(ctx, dyn, gvr, namespace, name, finalizers); err != nil {
			// Best-effort: log and keep iterating — apiserver hiccups, racing
			// reconciler patches, etc. are exactly what the retry is for.
			logrus.WithError(err).Warnf("chaos-cleanup: %s/%s/%s attempt %d/%d failed",
				gvr.Resource, namespace, name, attempt+1, chaosCleanupMaxAttempts)
		}

		got, getErr := dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			if k8serrors.IsNotFound(getErr) {
				return nil
			}
			logrus.WithError(getErr).Warnf("chaos-cleanup: %s/%s/%s post-delete GET failed (attempt %d/%d)",
				gvr.Resource, namespace, name, attempt+1, chaosCleanupMaxAttempts)
		} else {
			// Still alive — controller almost certainly re-added the
			// finalizer. Refresh the slice for the next strip pass.
			finalizers = got.GetFinalizers()
		}

		if attempt == chaosCleanupMaxAttempts-1 {
			logrus.Warnf("chaos-cleanup: giving up on %s/%s/%s after %d attempts (zombie remains; controller-manager re-adding finalizers?)",
				gvr.Resource, namespace, name, chaosCleanupMaxAttempts)
			return nil
		}
		time.Sleep(chaosCleanupRetryDelay)
	}
	return nil
}

// stripFinalizersAndDelete patches finalizers to [] (only when non-empty —
// avoids unnecessary writes against a healthy CR) and then issues a
// foreground-propagation Delete with GracePeriodSeconds=0. Both calls
// tolerate NotFound so concurrent reconciler-driven deletion is harmless.
func stripFinalizersAndDelete(
	ctx context.Context,
	dyn dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, name string,
	existingFinalizers []string,
) error {
	if gvr.Group != ChaosMeshAPIGroup {
		return fmt.Errorf("stripFinalizersAndDelete: refusing GVR outside %s: %s", ChaosMeshAPIGroup, gvr.String())
	}

	if len(existingFinalizers) > 0 {
		_, err := dyn.Resource(gvr).Namespace(namespace).Patch(
			ctx,
			name,
			types.MergePatchType,
			[]byte(`{"metadata":{"finalizers":[]}}`),
			metav1.PatchOptions{},
		)
		if err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("strip finalizers: %w", err)
		}
	}

	zero := int64(0)
	prop := metav1.DeletePropagationForeground
	delErr := dyn.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
		PropagationPolicy:  &prop,
	})
	if delErr != nil && !k8serrors.IsNotFound(delErr) {
		return fmt.Errorf("delete: %w", delErr)
	}
	return nil
}

// SummarizeChaosCleanup renders a single-line, alphabetised description of
// what was reaped, suitable for a logrus.Info call. Empty summary returns
// the empty string so the caller can short-circuit logging.
func SummarizeChaosCleanup(summary map[string]int) string {
	if len(summary) == 0 {
		return ""
	}
	keys := make([]string, 0, len(summary))
	for k := range summary {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", summary[k], k))
	}
	return strings.Join(parts, ", ")
}
