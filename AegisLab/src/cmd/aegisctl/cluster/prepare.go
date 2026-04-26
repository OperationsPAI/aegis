package cluster

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	defaultNamespace          = "exp"
	defaultServiceAccount     = "rcabench-sa"
	defaultExperimentPVC      = "rcabench-juicefs-experiment-storage"
	defaultExperimentPVCClass = "local-path"
	defaultExperimentPVCSize  = "10Gi"
)

type PrepareOutcome string

const (
	PrepareCreate PrepareOutcome = "create"
	PrepareUpdate PrepareOutcome = "update"
	PrepareSkip   PrepareOutcome = "skip"
)

type PrepareResult struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Outcome     PrepareOutcome `json:"outcome"`
	Applied     bool           `json:"applied"`
	Detail      string         `json:"detail"`
}

type PrepareSummary struct {
	Target  string          `json:"target"`
	DryRun  bool            `json:"dry_run"`
	Results []PrepareResult `json:"results"`
}

type PrepareAction struct {
	ID          string
	Description string
	Run         func(ctx context.Context, env CheckEnv, apply bool) (PrepareResult, error)
}

type PrepareRunner struct {
	Actions []PrepareAction
}

func (r *PrepareRunner) Run(ctx context.Context, env CheckEnv, apply bool) ([]PrepareResult, error) {
	results := make([]PrepareResult, 0, len(r.Actions))
	for _, action := range r.Actions {
		res, err := action.Run(ctx, env, apply)
		if err != nil {
			return results, fmt.Errorf("%s: %w", action.ID, err)
		}
		if res.ID == "" {
			res.ID = action.ID
		}
		if res.Description == "" {
			res.Description = action.Description
		}
		results = append(results, res)
	}
	return results, nil
}

func RenderPrepareTable(w io.Writer, results []PrepareResult) {
	const (
		actionHdr  = "ACTION"
		outcomeHdr = "OUTCOME"
		appliedHdr = "APPLIED"
		detailHdr  = "DETAIL"
	)
	actionWidth := len(actionHdr)
	for _, r := range results {
		if l := len(r.ID); l > actionWidth {
			actionWidth = l
		}
	}
	fmt.Fprintf(w, "%-*s  %-7s  %-7s  %s\n", actionWidth, actionHdr, outcomeHdr, appliedHdr, detailHdr)
	fmt.Fprintf(w, "%s  %s  %s  %s\n", strings.Repeat("-", actionWidth), strings.Repeat("-", 7), strings.Repeat("-", 7), strings.Repeat("-", 20))
	for _, r := range results {
		applied := "no"
		if r.Applied {
			applied = "yes"
		}
		fmt.Fprintf(w, "%-*s  %-7s  %-7s  %s\n", actionWidth, r.ID, r.Outcome, applied, r.Detail)
	}
}

func LocalE2EPrepareActions() []PrepareAction {
	return []PrepareAction{
		{
			ID:          "k8s.namespace",
			Description: "ensure the AegisLab namespace exists",
			Run: func(ctx context.Context, env CheckEnv, apply bool) (PrepareResult, error) {
				ns := namespaceName(env.Config())
				exists, err := env.K8s().NamespaceExists(ctx, ns)
				if err != nil {
					return PrepareResult{}, err
				}
				if exists {
					return PrepareResult{Outcome: PrepareSkip, Detail: fmt.Sprintf("namespace %q already present", ns)}, nil
				}
				if apply {
					if err := env.K8s().CreateNamespace(ctx, ns); err != nil {
						return PrepareResult{}, err
					}
				}
				return PrepareResult{Outcome: PrepareCreate, Applied: apply, Detail: fmt.Sprintf("namespace %q", ns)}, nil
			},
		},
		{
			ID:          "k8s.service-account",
			Description: "ensure the job service account exists",
			Run: func(ctx context.Context, env CheckEnv, apply bool) (PrepareResult, error) {
				ns := namespaceName(env.Config())
				sa := serviceAccountName(env.Config())
				exists, err := env.K8s().ServiceAccountExists(ctx, ns, sa)
				if err != nil {
					return PrepareResult{}, err
				}
				if exists {
					return PrepareResult{Outcome: PrepareSkip, Detail: fmt.Sprintf("ServiceAccount %s/%s already present", ns, sa)}, nil
				}
				if apply {
					if err := env.K8s().CreateServiceAccount(ctx, ns, sa); err != nil {
						return PrepareResult{}, err
					}
				}
				return PrepareResult{Outcome: PrepareCreate, Applied: apply, Detail: fmt.Sprintf("ServiceAccount %s/%s", ns, sa)}, nil
			},
		},
		{
			ID:          "k8s.experiment-pvc",
			Description: "ensure the experiment storage PVC exists",
			Run: func(ctx context.Context, env CheckEnv, apply bool) (PrepareResult, error) {
				ns := namespaceName(env.Config())
				pvc := experimentPVCName(env.Config())
				exists, bound, err := env.K8s().PVCBound(ctx, ns, pvc)
				if err != nil {
					return PrepareResult{}, err
				}
				if exists {
					detail := fmt.Sprintf("PVC %s/%s already exists", ns, pvc)
					if bound {
						detail += " and is Bound"
					} else {
						detail += "; waiting for the storage class to bind it"
					}
					return PrepareResult{Outcome: PrepareSkip, Detail: detail}, nil
				}
				if apply {
					if err := env.K8s().CreatePVC(ctx, ns, pvc, PVCSpec{StorageClassName: defaultExperimentPVCClass, Size: defaultExperimentPVCSize}); err != nil {
						return PrepareResult{}, err
					}
				}
				return PrepareResult{Outcome: PrepareCreate, Applied: apply, Detail: fmt.Sprintf("PVC %s/%s using storageClass=%q size=%s", ns, pvc, defaultExperimentPVCClass, defaultExperimentPVCSize)}, nil
			},
		},
	}
}

func LocalE2EEtcdActions() []PrepareAction {
	keys := map[string]string{
		"/rcabench/config/global/algo.detector":                                 "detector",
		"/rcabench/config/consumer/injection.system.otel-demo.count":            "1",
		"/rcabench/config/consumer/injection.system.otel-demo.ns_pattern":       `^otel-demo\d+$`,
		"/rcabench/config/consumer/injection.system.otel-demo.extract_pattern":  `^(otel-demo)(\d+)$`,
		"/rcabench/config/consumer/rate_limiting.max_concurrent_restarts":       "5",
		"/rcabench/config/consumer/rate_limiting.max_concurrent_builds":         "3",
		"/rcabench/config/consumer/rate_limiting.max_concurrent_build_datapack": "8",
		"/rcabench/config/consumer/rate_limiting.max_concurrent_algo_execution": "5",
		"/rcabench/config/consumer/rate_limiting.token_wait_timeout":            "10",
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)

	actions := make([]PrepareAction, 0, len(ordered))
	for _, key := range ordered {
		want := keys[key]
		actionKey := key
		actionWant := want
		actions = append(actions, PrepareAction{
			ID:          "etcd." + strings.TrimPrefix(strings.ReplaceAll(actionKey, "/", "."), "."),
			Description: "ensure required local-e2e etcd config is present",
			Run: func(ctx context.Context, env CheckEnv, apply bool) (PrepareResult, error) {
				got, exists, err := env.Etcd().Get(ctx, actionKey)
				if err != nil {
					return PrepareResult{}, err
				}
				if exists && got == actionWant {
					return PrepareResult{Outcome: PrepareSkip, Detail: fmt.Sprintf("%s already set to %q", actionKey, actionWant)}, nil
				}
				outcome := PrepareCreate
				detail := fmt.Sprintf("set %s to %q", actionKey, actionWant)
				if exists {
					outcome = PrepareUpdate
					detail = fmt.Sprintf("update %s from %q to %q", actionKey, got, actionWant)
				}
				if apply {
					if err := env.Etcd().Put(ctx, actionKey, actionWant); err != nil {
						return PrepareResult{}, err
					}
				}
				return PrepareResult{Outcome: outcome, Applied: apply, Detail: detail}, nil
			},
		})
	}
	return actions
}

func LocalE2EPrepareRunner() *PrepareRunner {
	actions := append([]PrepareAction{}, LocalE2EPrepareActions()...)
	actions = append(actions, LocalE2EEtcdActions()...)
	return &PrepareRunner{Actions: actions}
}

func namespaceName(cfg Config) string {
	if strings.TrimSpace(cfg.K8sNamespace) != "" {
		return strings.TrimSpace(cfg.K8sNamespace)
	}
	return defaultNamespace
}

func serviceAccountName(cfg Config) string {
	if strings.TrimSpace(cfg.ServiceAccount) != "" {
		return strings.TrimSpace(cfg.ServiceAccount)
	}
	return defaultServiceAccount
}

func experimentPVCName(cfg Config) string {
	if strings.TrimSpace(cfg.ExperimentPVC) != "" {
		return strings.TrimSpace(cfg.ExperimentPVC)
	}
	return defaultExperimentPVC
}
