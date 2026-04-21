package k8s

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// DeletedCRD represents a successfully-issued CRD deletion for reporting to
// callers (e.g. trace cancel).
type DeletedCRD struct {
	Namespace string
	Name      string
	Resource  string
}

// DeleteChaosCRDsByLabel lists chaos CRDs across all namespaces (via the
// cluster-scoped dynamic client) that match `labelKey=labelValue`, then
// issues a best-effort delete on each one. The returned slice contains the
// CRDs for which a delete was successfully issued; the error (if any) is
// only set on a fatal listing failure — per-object delete failures are
// collected in warnings.
//
// The chaos GVRs are sourced from chaos-experiment's CRD mapping so any new
// chaos kinds registered there are automatically covered.
func DeleteChaosCRDsByLabel(ctx context.Context, chaosGVRs []schema.GroupVersionResource, labelKey, labelValue string) ([]DeletedCRD, []error) {
	if labelKey == "" || labelValue == "" {
		return nil, []error{fmt.Errorf("label selector key/value must be non-empty")}
	}
	labelSelector := fmt.Sprintf("%s=%s", labelKey, labelValue)

	var (
		deleted  []DeletedCRD
		warnings []error
	)

	dyn := getK8sDynamicClient()
	for i := range chaosGVRs {
		gvr := chaosGVRs[i]
		list, err := dyn.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			warnings = append(warnings, fmt.Errorf("list %s by %s: %w", gvr.Resource, labelSelector, err))
			continue
		}
		for idx := range list.Items {
			item := list.Items[idx]
			if derr := deleteCRD(ctx, &gvr, item.GetNamespace(), item.GetName()); derr != nil {
				warnings = append(warnings, fmt.Errorf("delete %s/%s: %w", item.GetNamespace(), item.GetName(), derr))
				continue
			}
			deleted = append(deleted, DeletedCRD{
				Namespace: item.GetNamespace(),
				Name:      item.GetName(),
				Resource:  gvr.Resource,
			})
		}
	}
	return deleted, warnings
}

func deleteCRD(ctx context.Context, gvr *schema.GroupVersionResource, namespace, name string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}

	logEntry := logrus.WithFields(logrus.Fields{
		"namespace": namespace,
		"name":      name,
	})

	// 1. Check if resource exists
	obj, err := getK8sDynamicClient().Resource(*gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get CRD: %v", err)
	}

	// 2. Check if already being deleted
	if !obj.GetDeletionTimestamp().IsZero() {
		logEntry.Info("CRD is already being deleted")
		return nil
	}

	// 3. Execute deletion (idempotent operation)
	_, err = getK8sDynamicClient().Resource(*gvr).Namespace(namespace).Patch(
		timeoutCtx,
		name,
		types.MergePatchType,
		[]byte(`{"metadata":{"finalizers":[]}}`),
		metav1.PatchOptions{},
	)
	if err != nil && !errors.IsNotFound(err) {
		if timeoutCtx.Err() != nil {
			return fmt.Errorf("timeout while patching resource %s/%s: %v", namespace, name, timeoutCtx.Err())
		}

		return fmt.Errorf("failed to patch finalizers: %v", err)
	}

	logEntry.Info("Successfully cleared finalizers")

	err = getK8sDynamicClient().Resource(*gvr).Namespace(namespace).Delete(ctx, name, deleteOptions)
	if err != nil && !errors.IsNotFound(err) {
		if timeoutCtx.Err() != nil {
			return fmt.Errorf("timeout while deleting CRD %s/%s: %v", namespace, name, timeoutCtx.Err())
		}

		return fmt.Errorf("failed to delete CRD: %v", err)
	}

	return nil
}
