package chaos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	ssoclient "aegis/clients/sso"
	"aegis/platform/config"
	"aegis/platform/consts"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

const (
	webhookTokenSAName    = "chaos-service"
	webhookTokenLifetime  = 24 * time.Hour
	webhookTokenRefresh   = 12 * time.Hour
	webhookTokenIssueDays = 1
)

// backendTokenSource is the slice of *ssoclient.Client the provisioner needs:
// the cached aegis-backend client_credentials bearer that authenticates the
// chaos-service token-issue call. Narrow interface keeps the test free of the
// full ssoclient wiring.
type backendTokenSource interface {
	ServiceToken(ctx context.Context) (string, error)
}

// WebhookTokenProvisioner mints a fresh chaos-service token from SSO at fx
// OnStart and refreshes it on a background timer, caching the current value in
// an atomic ref the WebhookSender reads per request. This replaces the static,
// hand-stamped CHAOS_SA_TOKEN Secret: that token was minted before auth-unify
// added the `typ` claim, so the receiver's RequireServiceAccount gate rejected
// it. Re-minting through SSO always yields a current-format token.
//
// Unlike the worker's chaos-CLIENT token (core/orchestrator/chaos_sa_token.go),
// chaos has no signer — it FETCHES the token from SSO's
// /v1/service-accounts/chaos-service/issue endpoint, authenticating with its
// aegis-backend service token.
type WebhookTokenProvisioner struct {
	src        backendTokenSource
	httpClient *http.Client
	ssoBaseURL string
	tokenRef   atomic.Pointer[string]
	logger     *logrus.Entry
}

func NewWebhookTokenProvisioner(src backendTokenSource, ssoBaseURL string) *WebhookTokenProvisioner {
	return &WebhookTokenProvisioner{
		src:        src,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		ssoBaseURL: ssoBaseURL,
		logger:     logrus.StandardLogger().WithField("component", "chaos_webhook_token"),
	}
}

// Token returns the most recently minted chaos-service token, or "" while the
// boot mint hasn't completed (or failed). The WebhookSender falls back to the
// static CHAOS_SA_TOKEN env in that window.
func (p *WebhookTokenProvisioner) Token() string {
	if v := p.tokenRef.Load(); v != nil {
		return *v
	}
	return ""
}

func (p *WebhookTokenProvisioner) refresh(ctx context.Context) error {
	tok, err := p.mint(ctx)
	if err != nil {
		return err
	}
	p.tokenRef.Store(&tok)
	p.logger.Info("chaos-service webhook token minted from SSO")
	return nil
}

// mint authenticates with the aegis-backend bearer and asks SSO to issue a
// fresh chaos-service token. The aegis-backend token passes the issue
// endpoint's requireAdminOrService gate, so no admin credential is needed.
func (p *WebhookTokenProvisioner) mint(ctx context.Context) (string, error) {
	if p.ssoBaseURL == "" {
		return "", fmt.Errorf("sso.base_url not configured")
	}
	backendTok, err := p.src.ServiceToken(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire aegis-backend token: %w", err)
	}

	body, err := json.Marshal(map[string]any{"lifetime_days": webhookTokenIssueDays})
	if err != nil {
		return "", err
	}
	url := p.ssoBaseURL + "/v1/service-accounts/" + webhookTokenSAName + "/issue"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", consts.AuthSchemeBearer+backendTok)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("sso issue %s -> %d: %s", webhookTokenSAName, resp.StatusCode, bytes.TrimSpace(raw))
	}

	var env struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("decode issue response: %w", err)
	}
	if env.Data.Token == "" {
		return "", fmt.Errorf("sso issue returned empty token")
	}
	return env.Data.Token, nil
}

func (p *WebhookTokenProvisioner) runRefresh(ctx context.Context) {
	ticker := time.NewTicker(webhookTokenRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := p.refresh(refreshCtx); err != nil {
				p.logger.WithError(err).Error("chaos-service webhook token refresh failed; previous token still served until expiry")
			}
			cancel()
		}
	}
}

// WebhookTokenModule mints the chaos-service webhook token at boot, wires the
// WebhookSender to read it per request, and starts the background refresher.
// Soft-fail: an unreachable SSO at boot logs and leaves the sender on the
// static CHAOS_SA_TOKEN env fallback until the first successful refresh.
var WebhookTokenModule = fx.Module("chaos.webhook_token",
	fx.Provide(newWebhookTokenProvisioner),
	fx.Invoke(registerWebhookTokenLifecycle),
)

func newWebhookTokenProvisioner(client *ssoclient.Client) *WebhookTokenProvisioner {
	return NewWebhookTokenProvisioner(client, config.GetString("sso.base_url"))
}

func registerWebhookTokenLifecycle(lc fx.Lifecycle, p *WebhookTokenProvisioner, w *WebhookSender) {
	w.SetTokenProvider(p.Token)
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			bootCtx, bootCancel := context.WithTimeout(ctx, 15*time.Second)
			defer bootCancel()
			if err := p.refresh(bootCtx); err != nil {
				p.logger.WithError(err).Error("initial chaos-service webhook token mint failed; webhook will fall back to CHAOS_SA_TOKEN env until refresh succeeds")
			}
			go p.runRefresh(ctx)
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			return nil
		},
	})
}
