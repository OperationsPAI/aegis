package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// StalePodChaosLister is the minimal chaos-mesh PodChaos surface the stale-CRD
// warning path needs. Tests inject a fake; production uses the dynamic-client
// backed implementation.
type StalePodChaosLister interface {
	ListPodChaosTraceIDs(ctx context.Context, namespace string) ([]string, error)
}

// guidedStalePodChaosListerHook is the test injection seam. nil => build a
// real dynamic-client-backed lister.
var guidedStalePodChaosListerHook StalePodChaosLister

// podChaosGVR is the chaos-mesh v1alpha1 PodChaos resource.
var podChaosGVR = schema.GroupVersionResource{
	Group:    "chaos-mesh.org",
	Version:  "v1alpha1",
	Resource: "podchaos",
}

// warnStalePodChaos lists PodChaos CRs in `namespace` and, if any carry a
// non-empty `trace_id` label, emits a single WARN block to `stderr`. If the
// cluster is unreachable an `info:` line is emitted and nil returned — the
// submit must proceed.
//
// This is intentionally non-blocking: orphaned CRs do not prevent a new
// submit, they only confuse `kubectl get podchaos` until the owning trace is
// cancelled.
func warnStalePodChaos(ctx context.Context, namespace string, lister StalePodChaosLister, stderr io.Writer) error {
	if namespace == "" {
		return nil
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if lister == nil {
		l, err := newLiveStalePodChaosLister()
		if err != nil {
			fmt.Fprintf(stderr, "info: skipped stale-CRD check (k8s unreachable: %v)\n", err)
			return nil
		}
		lister = l
	}
	traces, err := lister.ListPodChaosTraceIDs(ctx, namespace)
	if err != nil {
		fmt.Fprintf(stderr, "info: skipped stale-CRD check (k8s unreachable: %v)\n", err)
		return nil
	}
	// Filter empties and dedupe.
	seen := make(map[string]struct{})
	uniq := make([]string, 0, len(traces))
	for _, t := range traces {
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		uniq = append(uniq, t)
	}
	if len(uniq) == 0 {
		return nil
	}
	sort.Strings(uniq)

	total := len(uniq)
	display := uniq
	extra := 0
	if total > 10 {
		display = uniq[:10]
		extra = total - 10
	}
	listStr := joinTraceList(display)
	if extra > 0 {
		listStr = fmt.Sprintf("%s, … and %d more", listStr, extra)
	}
	fmt.Fprintf(stderr, "WARN: %d PodChaos CR(s) in ns=%s from prior trace(s): %s\n", total, namespace, listStr)
	fmt.Fprintf(stderr, "      These will not block the new submit but may confuse kubectl get podchaos.\n")
	fmt.Fprintf(stderr, "      Run `aegisctl trace cancel <id>` to clean up.\n")
	return nil
}

func joinTraceList(ts []string) string {
	out := ""
	for i, t := range ts {
		if i > 0 {
			out += ", "
		}
		out += t
	}
	return out
}

// liveStalePodChaosLister is the real dynamic-client-backed PodChaos lister,
// mirroring newLivePodLister's in-cluster-or-kubeconfig resolution pattern.
type liveStalePodChaosLister struct {
	dyn dynamic.Interface
}

func newLiveStalePodChaosLister() (*liveStalePodChaosLister, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, ".kube", "config")
		cfg, err = clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, err
		}
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &liveStalePodChaosLister{dyn: dyn}, nil
}

func (l *liveStalePodChaosLister) ListPodChaosTraceIDs(ctx context.Context, namespace string) ([]string, error) {
	list, err := l.dyn.Resource(podChaosGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list.Items))
	for i := range list.Items {
		labels := list.Items[i].GetLabels()
		if labels == nil {
			continue
		}
		if tid := labels["trace_id"]; tid != "" {
			out = append(out, tid)
		}
	}
	return out, nil
}
