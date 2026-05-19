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

// webhookBackoff matches the design-doc retry schedule. 5 attempts ≈ 31s
// of total wait — short enough that a reconciler tick stays interactive
// for an operator watching `kubectl get injection`, long enough to tide
// over a one-pod aegis-api roll.
var webhookBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}

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

// WithBearer wires a static Bearer token for the receiver's
// TrustedHeaderAuth fallthrough path. The aegis-chaos pod reads
// CHAOS_WEBHOOK_BEARER at boot; production deployments stamp a
// service-token JWT, dev installs can stamp an admin token.
func (w *WebhookSender) WithBearer(token string) *WebhookSender {
	w.bearer = token
	return w
}

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
	for attempt := 0; attempt < len(webhookBackoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				lastErr = ctx.Err()
				break
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
