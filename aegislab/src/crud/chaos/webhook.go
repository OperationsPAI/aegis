package chaos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"gorm.io/gorm"
)

var errWebhookDisabled = errors.New("chaos webhook: disabled (empty backendURL)")

const webhookMaxAttempts = 5

// webhookBackoff is the inter-attempt sleep schedule. 5 attempts with
// 4 sleeps between them = 1+2+4+8 = 15s of total wait — short enough
// that a reconciler tick stays interactive, long enough to tide over a
// one-pod aegis-api roll.
var webhookBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

type WebhookSender struct {
	httpClient *http.Client
	backendURL string
	bearer     string
	db         *gorm.DB
	logger     *logrus.Logger
}

func NewWebhookSender(httpClient *http.Client, backendURL string, db *gorm.DB, logger *logrus.Logger) *WebhookSender {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	if backendURL == "" {
		logger.Info("chaos webhook: delivery disabled (chaos.backendURL empty)")
	}
	return &WebhookSender{httpClient: httpClient, backendURL: backendURL, db: db, logger: logger}
}

// SetBearer wires a static Bearer token for the receiver's
// TrustedHeaderAuth fallthrough path. The aegis-chaos pod reads
// CHAOS_WEBHOOK_BEARER at boot; production deployments stamp a
// service-token JWT, dev installs can stamp an admin token.
func (w *WebhookSender) SetBearer(token string) {
	w.bearer = token
}

// webhookPayload uses json.RawMessage so the JSON columns (Groundtruth /
// Diagnostics / CallerMetadata) survive the Injection→payload→HTTP→
// receiver hop as logically-equivalent JSON. Byte-identity is not
// preserved — the source columns are gorm JSONMap, so jsonOrNull
// re-marshals via Go's map sort order. Field set + values match.
type webhookPayload struct {
	InjectionID    string          `json:"injection_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Status         string          `json:"status"`
	Groundtruth    json.RawMessage `json:"groundtruth,omitempty"`
	Diagnostics    json.RawMessage `json:"diagnostics,omitempty"`
	CallerMetadata json.RawMessage `json:"caller_metadata,omitempty"`
	StartedAt      string          `json:"started_at,omitempty"`
	FinishedAt     string          `json:"finished_at,omitempty"`
}

func (w *WebhookSender) Fire(ctx context.Context, inj *Injection) error {
	if w == nil || w.backendURL == "" {
		return errWebhookDisabled
	}

	payload := webhookPayload{
		InjectionID:    inj.ID,
		IdempotencyKey: inj.IdempotencyKey,
		Status:         inj.Status,
		Groundtruth:    jsonOrNull(inj.Groundtruth),
		Diagnostics:    jsonOrNull(inj.Diagnostics),
		CallerMetadata: jsonOrNull(inj.CallerMetadata),
	}
	if inj.StartedAt != nil {
		payload.StartedAt = inj.StartedAt.UTC().Format(time.RFC3339)
	}
	if inj.FinishedAt != nil {
		payload.FinishedAt = inj.FinishedAt.UTC().Format(time.RFC3339)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := w.backendURL + "/api/v1/hooks/chaos"
	var lastErr error
	for attempt := 0; attempt < webhookMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				// Return without recording another (cancelled-ctx) attempt
				// against the row — a bare `break` here would only exit the
				// select and then run w.attempt(ctx, ...) with a dead ctx.
				return ctx.Err()
			case <-time.After(webhookBackoff[attempt-1]):
			}
		}
		lastErr = w.attempt(ctx, url, body)
		w.recordAttempt(ctx, inj.ID, lastErr)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (w *WebhookSender) attempt(ctx context.Context, url string, body []byte) error {
	attemptCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+w.bearer)
	}
	otel.GetTextMapPropagator().Inject(attemptCtx, propagation.HeaderCarrier(req.Header))

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("hook status %d: %s", resp.StatusCode, bytes.TrimSpace(snippet))
}

func (w *WebhookSender) recordAttempt(ctx context.Context, id string, attemptErr error) {
	now := time.Now().UTC()
	updates := map[string]any{"webhook_attempted_at": &now}
	if attemptErr == nil {
		updates["webhook_error"] = ""
	} else {
		updates["webhook_error"] = attemptErr.Error()
	}
	if err := w.db.WithContext(ctx).Model(&Injection{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		w.logger.WithError(err).WithField("id", id).Warn("chaos webhook: record attempt")
	}
}

type batchChildPayload struct {
	InjectionID    string          `json:"injection_id"`
	PointID        string          `json:"point_id"`
	Status         string          `json:"status"`
	StartedAt      string          `json:"started_at,omitempty"`
	FinishedAt     string          `json:"finished_at,omitempty"`
	Groundtruth    json.RawMessage `json:"groundtruth,omitempty"`
	Diagnostics    json.RawMessage `json:"diagnostics_brief,omitempty"`
	CallerMetadata json.RawMessage `json:"caller_metadata,omitempty"`
}

type batchPayload struct {
	BatchID             string              `json:"batch_id"`
	IdempotencyKey      string              `json:"idempotency_key"`
	AggregatedStatus    string              `json:"aggregated_status"`
	StartedAt           string              `json:"started_at,omitempty"`
	FinishedAt          string              `json:"finished_at,omitempty"`
	ChildResults        []batchChildPayload `json:"child_results"`
	BatchCallerMetadata json.RawMessage     `json:"batch_caller_metadata,omitempty"`
}

// FireBatch posts the §10.2 batch envelope to /api/v1/hooks/chaos-batch.
// Same retry/timeout policy as singleton Fire.
func (w *WebhookSender) FireBatch(ctx context.Context, batch *InjectionBatch, children []Injection) error {
	if w == nil || w.backendURL == "" {
		return errWebhookDisabled
	}

	payload := batchPayload{
		BatchID:             batch.ID,
		IdempotencyKey:      batch.IdempotencyKey,
		AggregatedStatus:    batch.AggregatedStatus,
		BatchCallerMetadata: jsonOrNull(batch.BatchCallerMetadata),
		ChildResults:        make([]batchChildPayload, 0, len(children)),
	}
	if batch.StartedAt != nil {
		payload.StartedAt = batch.StartedAt.UTC().Format(time.RFC3339)
	}
	if batch.FinishedAt != nil {
		payload.FinishedAt = batch.FinishedAt.UTC().Format(time.RFC3339)
	}
	for _, c := range children {
		cp := batchChildPayload{
			InjectionID:    c.ID,
			PointID:        c.PointID,
			Status:         c.Status,
			Groundtruth:    jsonOrNull(c.Groundtruth),
			Diagnostics:    jsonOrNull(c.Diagnostics),
			CallerMetadata: jsonOrNull(c.CallerMetadata),
		}
		if c.StartedAt != nil {
			cp.StartedAt = c.StartedAt.UTC().Format(time.RFC3339)
		}
		if c.FinishedAt != nil {
			cp.FinishedAt = c.FinishedAt.UTC().Format(time.RFC3339)
		}
		payload.ChildResults = append(payload.ChildResults, cp)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal batch payload: %w", err)
	}

	url := w.backendURL + "/api/v1/hooks/chaos-batch"
	var lastErr error
	for attempt := 0; attempt < webhookMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(webhookBackoff[attempt-1]):
			}
		}
		lastErr = w.attempt(ctx, url, body)
		w.recordBatchAttempt(ctx, batch.ID, lastErr)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (w *WebhookSender) recordBatchAttempt(ctx context.Context, id string, attemptErr error) {
	now := time.Now().UTC()
	updates := map[string]any{"webhook_attempted_at": &now}
	if attemptErr == nil {
		updates["webhook_error"] = ""
	} else {
		updates["webhook_error"] = attemptErr.Error()
	}
	if err := w.db.WithContext(ctx).Model(&InjectionBatch{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		w.logger.WithError(err).WithField("id", id).Warn("chaos webhook: record batch attempt")
	}
}

func jsonOrNull(m JSONMap) json.RawMessage {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}
