package chaossystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"aegis/cli/apiclient"
	chaos "aegis/platform/chaos"
	"aegis/platform/config"

	"github.com/sirupsen/logrus"
)

// OutboundBearerEnv is the env-var fallback for the SA-minted backend->chaos
// bearer token, kept in sync with core/orchestrator's resolver. We avoid
// importing the consumer package here to keep this side of the boundary
// dependency-free.
const outboundBearerEnv = "CHAOS_OUTBOUND_BEARER"

// chaosBearerProvider lets boot.runtime_stack inject the SA-minted token at
// runtime. Returns the current token, or empty when mint hasn't run.
var (
	chaosBearerProvider     func() string
	chaosBearerProviderOnce sync.Once
)

// SetChaosOutboundBearerProvider is the boot-time wiring point. Safe to call
// once at fx Initialize; subsequent calls replace the provider.
func SetChaosOutboundBearerProvider(p func() string) {
	chaosBearerProvider = p
}

func resolveChaosBearer() string {
	if chaosBearerProvider != nil {
		if tok := strings.TrimSpace(chaosBearerProvider()); tok != "" {
			return tok
		}
	}
	envTok := strings.TrimSpace(os.Getenv(outboundBearerEnv))
	if envTok != "" {
		chaosBearerProviderOnce.Do(func() {
			logrus.WithField("env", outboundBearerEnv).
				Error("DEPRECATED: chaossystem→chaos auth using static CHAOS_OUTBOUND_BEARER; chaos-client SA mint not wired (missing seed?). Token will be rejected once one-release grace window closes.")
		})
	}
	return envTok
}

// enumerateCandidatesViaChaosService is the default value of
// enumerateCandidatesFn. It hits chaos-service's
// /v1beta/systems/{sys}/candidates endpoint and translates the typed response
// back into the local GuidedConfig shape via JSON round-trip — the same wire
// contract the chaos-service handler enforces.
func enumerateCandidatesViaChaosService(ctx context.Context, system, namespace string) ([]chaos.GuidedConfig, error) {
	baseURL := strings.TrimSpace(config.GetChaosServiceURL())
	if baseURL == "" {
		return nil, errors.New("chaos.service_url is empty; cannot enumerate candidates via chaos service")
	}
	cfg := apiclient.NewConfiguration()
	cfg.Servers = apiclient.ServerConfigurations{{URL: strings.TrimRight(baseURL, "/")}}
	if bearer := resolveChaosBearer(); bearer != "" {
		cfg.AddDefaultHeader("Authorization", "Bearer "+bearer)
	}
	cli := apiclient.NewAPIClient(cfg)

	req := cli.ChaosAPI.ChaosListSystemCandidates(ctx, system)
	if namespace != "" {
		req = req.Namespace(namespace)
	}
	resp, _, err := req.Execute()
	if err != nil {
		return nil, fmt.Errorf("chaos service list candidates: %w", err)
	}
	if resp == nil || resp.Data == nil {
		return nil, errors.New("chaos service list candidates: empty response")
	}

	raw, err := json.Marshal(resp.Data.Candidates)
	if err != nil {
		return nil, fmt.Errorf("re-encode chaos service candidates: %w", err)
	}
	var out []chaos.GuidedConfig
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode chaos service candidates: %w", err)
	}
	return out, nil
}
