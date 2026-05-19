package chaosprune

import (
	"context"
	"fmt"
	"time"

	"aegis/platform/consts"
	k8s "aegis/platform/k8s"
	"aegis/platform/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// defaultAgeSeconds is the minimum CR age (in seconds) required for a
// terminal-state task before its CR is eligible for reaping. Chosen to be
// larger than the chaos-mesh reconcile period (~12s) plus the orchestrator
// callback timeout so the normal cleanup path always wins under contention.
const defaultAgeSeconds = 300

// Service runs the chaos-CR prune sweep.
type Service struct {
	gw *k8s.Gateway
	db *gorm.DB
}

func NewService(gw *k8s.Gateway, db *gorm.DB) *Service {
	return &Service{gw: gw, db: db}
}

// Prune classifies and (when !DryRun) deletes orphaned chaos-mesh CRs. See
// k8s.ClassifyChaosOrphans for the predicate.
func (s *Service) Prune(ctx context.Context, req *PruneReq) (*PruneResp, error) {
	return prune(ctx, s.gw, s.db, req, time.Now())
}

// prune is the testable seam. `now` is injected so tests can pin the cutoff.
func prune(
	ctx context.Context,
	gw *k8s.Gateway,
	db *gorm.DB,
	req *PruneReq,
	now time.Time,
) (*PruneResp, error) {
	if req == nil {
		req = &PruneReq{}
	}
	age := req.AgeSeconds
	if age <= 0 {
		age = defaultAgeSeconds
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}

	resp := &PruneResp{
		DryRun:     dryRun,
		Namespace:  req.Namespace,
		AgeSeconds: age,
		Candidates: []PruneCandidate{},
	}

	crs, warns := gw.ListChaosCRs(ctx, req.Namespace, req.IncludeKinds)
	for _, w := range warns {
		resp.Warnings = append(resp.Warnings, w.Error())
	}
	if len(crs) == 0 {
		return resp, nil
	}

	lookup := newTaskStateLookup(ctx, db)
	orphans, errs := k8s.ClassifyChaosOrphans(crs, lookup, now, time.Duration(age)*time.Second)
	for _, e := range errs {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("task-state lookup: %v", e))
	}

	resp.Candidates = make([]PruneCandidate, 0, len(orphans))
	for _, o := range orphans {
		cand := PruneCandidate{
			Kind:       o.Kind,
			Resource:   o.Resource,
			Namespace:  o.Namespace,
			Name:       o.Name,
			TaskID:     o.TaskID,
			TaskState:  o.TaskState,
			Reason:     string(o.Reason),
			AgeSeconds: o.AgeSeconds,
		}
		if !dryRun {
			if err := gw.DeleteChaosCR(ctx, o.Resource, o.Namespace, o.Name); err != nil {
				cand.DeleteError = err.Error()
				logrus.WithError(err).WithFields(logrus.Fields{
					"resource": o.Resource, "namespace": o.Namespace, "name": o.Name,
				}).Warn("chaos prune: delete failed")
			} else {
				cand.Deleted = true
				logrus.WithFields(logrus.Fields{
					"resource": o.Resource, "namespace": o.Namespace,
					"name": o.Name, "reason": cand.Reason,
				}).Info("chaos prune: reaped orphan CR")
			}
		}
		resp.Candidates = append(resp.Candidates, cand)
	}
	return resp, nil
}

// newTaskStateLookup builds a k8s.TaskStateLookup with a small per-call cache
// so repeated task-id lookups (same task across multiple CRs in a hybrid
// batch) don't hammer the DB.
func newTaskStateLookup(ctx context.Context, db *gorm.DB) k8s.TaskStateLookup {
	type entry struct {
		state consts.TaskState
		found bool
	}
	cache := map[string]entry{}
	return func(taskID string) (consts.TaskState, bool, error) {
		if hit, ok := cache[taskID]; ok {
			return hit.state, hit.found, nil
		}
		var task model.Task
		err := db.WithContext(ctx).Select("state").Where("id = ?", taskID).First(&task).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				cache[taskID] = entry{0, false}
				return 0, false, nil
			}
			return 0, false, err
		}
		cache[taskID] = entry{task.State, true}
		return task.State, true, nil
	}
}
