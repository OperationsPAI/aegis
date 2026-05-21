package chaos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type CreateBatchChild struct {
	PointID        string
	Namespace      string
	Params         map[string]any
	IdempotencyKey string
	CallerMetadata map[string]any
	ExecutorPin    string
}

type CreateBatchInput struct {
	BatchIdempotencyKey string
	BatchCallerMetadata map[string]any
	Children            []CreateBatchChild
}

// BatchWithChildren bundles the persisted batch row with every child Injection
// row at read time. Callers render this directly into the API response.
type BatchWithChildren struct {
	Batch    *InjectionBatch
	Children []Injection
}

// CreateInjectionBatch persists a parent batch row, then runs sync Apply per
// child, matching the singleton CreateInjection ADR-0004 pattern. Failures
// land in the child row only; siblings continue.
func (s *Manager) CreateInjectionBatch(ctx context.Context, in CreateBatchInput) (*BatchWithChildren, error) {
	if in.BatchIdempotencyKey == "" {
		return nil, fmt.Errorf("chaos: batch_idempotency_key is required")
	}
	if len(in.Children) == 0 {
		return nil, ErrBatchEmpty
	}

	var existing InjectionBatch
	err := s.DB.WithContext(ctx).Where("idempotency_key = ?", in.BatchIdempotencyKey).Take(&existing).Error
	if err == nil {
		return s.loadBatchWithChildren(ctx, existing.ID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	batch := InjectionBatch{
		ID:                  ulid.Make().String(),
		IdempotencyKey:      in.BatchIdempotencyKey,
		AggregatedStatus:    AggPending,
		BatchCallerMetadata: JSONMap(in.BatchCallerMetadata),
		TaskID:              taskIDFromMeta(in.BatchCallerMetadata),
		Ts:                  now,
		StartedAt:           &now,
	}
	if err := s.DB.WithContext(ctx).Create(&batch).Error; err != nil {
		return nil, err
	}

	childRows := make([]Injection, 0, len(in.Children))
	for _, c := range in.Children {
		if c.IdempotencyKey == "" {
			return nil, fmt.Errorf("chaos: child idempotency_key is required")
		}
		if c.Namespace == "" {
			return nil, fmt.Errorf("chaos: child namespace is required")
		}
		row, applyErr := s.createBatchChild(ctx, batch.ID, c)
		if row != nil {
			childRows = append(childRows, *row)
		}
		if applyErr != nil {
			logrus.WithError(applyErr).WithField("batch_id", batch.ID).
				WithField("point_id", c.PointID).Warn("chaos: batch child apply failed")
		}
	}

	// Pre-compute aggregated status so callers can see e.g. all-failed batches
	// before the reconciler ticks. Reconciler still owns terminal stickiness.
	statuses := make([]string, 0, len(childRows))
	for _, r := range childRows {
		statuses = append(statuses, r.Status)
	}
	agg := ComputeAggregatedStatus(statuses)
	updates := map[string]any{"aggregated_status": agg}
	if isAggTerminal(agg) {
		fin := time.Now().UTC()
		updates["finished_at"] = &fin
		batch.FinishedAt = &fin
	}
	if err := s.DB.WithContext(ctx).Model(&InjectionBatch{}).Where("id = ?", batch.ID).
		Updates(updates).Error; err != nil {
		return nil, err
	}
	batch.AggregatedStatus = agg
	return &BatchWithChildren{Batch: &batch, Children: childRows}, nil
}

// createBatchChild mirrors CreateInjection but stamps batch_id and never
// returns an error to the caller — failed children are persisted as
// status=failed and the batch as a whole accepts mixed outcomes.
func (s *Manager) createBatchChild(ctx context.Context, batchID string, c CreateBatchChild) (*Injection, error) {
	var existing Injection
	err := s.DB.WithContext(ctx).Where("idempotency_key = ?", c.IdempotencyKey).Take(&existing).Error
	if err == nil {
		if existing.PointID != c.PointID {
			return nil, ErrIdempotencyMismatch
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var point Point
	if err := s.DB.WithContext(ctx).Where("id = ?", c.PointID).Take(&point).Error; err != nil {
		row := s.persistFailedChild(ctx, batchID, c, fmt.Errorf("point %s: %w", c.PointID, err))
		return row, err
	}
	if point.Status != PointActive {
		row := s.persistFailedChild(ctx, batchID, c, ErrPointNotActive)
		return row, ErrPointNotActive
	}

	var capRow Capability
	if err := s.DB.WithContext(ctx).Where("name = ?", point.CapabilityName).Take(&capRow).Error; err != nil {
		row := s.persistFailedChild(ctx, batchID, c, err)
		return row, err
	}
	if capRow.Status == CapDeprecated {
		row := s.persistFailedChild(ctx, batchID, c, ErrCapabilityUnsupported)
		return row, ErrCapabilityUnsupported
	}
	supported := false
	for _, sc := range s.Executor.SupportedCapabilities() {
		if sc.Capability == capRow.Name {
			supported = true
			break
		}
	}
	if !supported {
		row := s.persistFailedChild(ctx, batchID, c, ErrCapabilityUnsupported)
		return row, ErrCapabilityUnsupported
	}

	sys, err := s.GetSystem(ctx, point.SystemName)
	if err != nil {
		row := s.persistFailedChild(ctx, batchID, c, err)
		return row, err
	}
	if !sys.Enabled {
		row := s.persistFailedChild(ctx, batchID, c, ErrSystemDisabled)
		return row, ErrSystemDisabled
	}
	if cerr := s.checkSystemCapacity(ctx, sys); cerr != nil {
		row := s.persistFailedChild(ctx, batchID, c, cerr)
		return row, cerr
	}
	sysCtx := SystemContext{Name: sys.Name, AppLabelKey: sys.AppLabelKey}

	target := map[string]any(point.Target)
	handle, err := s.Executor.DeriveHandle(capRow.Name, c.IdempotencyKey, c.Namespace, target)
	if err != nil {
		row := s.persistFailedChild(ctx, batchID, c, err)
		return row, err
	}

	params := c.Params
	if params == nil {
		params = map[string]any{}
	}
	now := time.Now().UTC()
	bid := batchID
	row := Injection{
		ID:             ulid.Make().String(),
		BatchID:        &bid,
		PointID:        point.ID,
		Params:         JSONMap(params),
		IdempotencyKey: c.IdempotencyKey,
		ExecutorName:   s.Executor.Name(),
		ExecutorHandle: handle,
		Status:         StatusPending,
		CallerMetadata: JSONMap(c.CallerMetadata),
		TaskID:         taskIDFromMeta(c.CallerMetadata),
		Ts:             now,
	}
	if err := s.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}

	applyAt := time.Now().UTC()
	if applyErr := s.Executor.Apply(ctx, sysCtx, capRow.Name, handle, target, c.Params); applyErr != nil {
		fin := applyAt
		updates := map[string]any{
			"status":      StatusFailed,
			"diagnostics": JSONMap{"error": applyErr.Error()},
			"started_at":  &applyAt,
			"finished_at": &fin,
		}
		_ = s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", row.ID).Updates(updates).Error
		row.Status = StatusFailed
		row.Diagnostics = JSONMap{"error": applyErr.Error()}
		row.StartedAt = &applyAt
		row.FinishedAt = &fin
		return &row, applyErr
	}

	if err := s.DB.WithContext(ctx).Model(&Injection{}).Where("id = ?", row.ID).Updates(map[string]any{
		"status":     StatusRunning,
		"started_at": &applyAt,
	}).Error; err != nil {
		return &row, err
	}
	row.Status = StatusRunning
	row.StartedAt = &applyAt
	return &row, nil
}

// persistFailedChild lands a synthetic failed-child row when we can't even
// get to executor.Apply (point not found, capability unsupported, etc.).
// Returns the persisted row; on DB-failure-of-the-DB-write it returns nil.
func (s *Manager) persistFailedChild(ctx context.Context, batchID string, c CreateBatchChild, cause error) *Injection {
	now := time.Now().UTC()
	bid := batchID
	executorName := ""
	if s.Executor != nil {
		executorName = s.Executor.Name()
	}
	params := c.Params
	if params == nil {
		params = map[string]any{}
	}
	row := Injection{
		ID:             ulid.Make().String(),
		BatchID:        &bid,
		PointID:        c.PointID,
		Params:         JSONMap(params),
		IdempotencyKey: c.IdempotencyKey,
		ExecutorName:   executorName,
		Status:         StatusFailed,
		Diagnostics:    JSONMap{"error": cause.Error()},
		CallerMetadata: JSONMap(c.CallerMetadata),
		TaskID:         taskIDFromMeta(c.CallerMetadata),
		Ts:             now,
		StartedAt:      &now,
		FinishedAt:     &now,
	}
	if err := s.DB.WithContext(ctx).Create(&row).Error; err != nil {
		logrus.WithError(err).WithField("batch_id", batchID).Warn("chaos: persist failed-child row")
		return nil
	}
	return &row
}

func (s *Manager) loadBatchWithChildren(ctx context.Context, id string) (*BatchWithChildren, error) {
	var batch InjectionBatch
	if err := s.DB.WithContext(ctx).Where("id = ?", id).Take(&batch).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBatchNotFound
		}
		return nil, err
	}
	var children []Injection
	if err := s.DB.WithContext(ctx).
		Where("batch_id = ?", id).Order("ts ASC").Find(&children).Error; err != nil {
		return nil, err
	}
	return &BatchWithChildren{Batch: &batch, Children: children}, nil
}

func (s *Manager) GetInjectionBatch(ctx context.Context, id string) (*BatchWithChildren, error) {
	return s.loadBatchWithChildren(ctx, id)
}

// DeleteInjectionBatch cancels every non-terminal child and stamps the batch
// aggregated_status as `cancelled` (ADR-0006 stickiness).
func (s *Manager) DeleteInjectionBatch(ctx context.Context, id string) (*BatchWithChildren, error) {
	cur, err := s.loadBatchWithChildren(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range cur.Children {
		child := &cur.Children[i]
		if isTerminal(child.Status) {
			continue
		}
		updated, derr := s.DeleteInjection(ctx, child.ID)
		if derr != nil {
			logrus.WithError(derr).WithField("id", child.ID).Warn("chaos: batch-delete child")
			continue
		}
		cur.Children[i] = *updated
	}
	now := time.Now().UTC()
	if !isAggTerminal(cur.Batch.AggregatedStatus) {
		updates := map[string]any{
			"aggregated_status": AggCancelled,
			"finished_at":       &now,
		}
		if err := s.DB.WithContext(ctx).Model(&InjectionBatch{}).Where("id = ?", id).
			Updates(updates).Error; err != nil {
			return nil, err
		}
		cur.Batch.AggregatedStatus = AggCancelled
		cur.Batch.FinishedAt = &now
	}
	return cur, nil
}

func isAggTerminal(s string) bool {
	switch s {
	case AggSucceeded, AggFailed, AggPartial, AggCancelled:
		return true
	}
	return false
}

type ListPointsFilter struct {
	System     string
	Service    string
	Capability string
	Status     string
	Limit      int
	Offset     int
}

// ListSystemPoints returns the Points belonging to a system, joined to the
// chaos_services table so callers can resolve service_name without a second
// round-trip.
func (s *Manager) ListSystemPoints(ctx context.Context, f ListPointsFilter) ([]Point, map[int64]string, int64, error) {
	if f.System == "" {
		return nil, nil, 0, fmt.Errorf("chaos: system is required")
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	q := s.DB.WithContext(ctx).Model(&Point{}).Where("system_name = ?", f.System)
	if f.Capability != "" {
		q = q.Where("capability_name = ?", f.Capability)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Service != "" {
		var svc Service
		if err := s.DB.WithContext(ctx).
			Where("system_name = ? AND name = ?", f.System, f.Service).
			Order("last_seen_at DESC").First(&svc).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return []Point{}, map[int64]string{}, 0, nil
			}
			return nil, nil, 0, err
		}
		q = q.Where("service_id = ?", svc.ID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, nil, 0, err
	}
	var rows []Point
	if err := q.Order("created_at DESC").Limit(f.Limit).Offset(f.Offset).Find(&rows).Error; err != nil {
		return nil, nil, 0, err
	}

	svcIDs := make(map[int64]struct{}, len(rows))
	for _, r := range rows {
		if r.ServiceID != nil {
			svcIDs[*r.ServiceID] = struct{}{}
		}
	}
	names := make(map[int64]string, len(svcIDs))
	if len(svcIDs) > 0 {
		ids := make([]int64, 0, len(svcIDs))
		for k := range svcIDs {
			ids = append(ids, k)
		}
		var svcs []Service
		if err := s.DB.WithContext(ctx).Where("id IN ?", ids).Find(&svcs).Error; err != nil {
			return nil, nil, 0, err
		}
		for _, sv := range svcs {
			names[sv.ID] = sv.Name
		}
	}
	return rows, names, total, nil
}
