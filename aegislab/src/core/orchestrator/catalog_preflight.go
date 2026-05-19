package consumer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aegis/cli/apiclient"
	"aegis/platform/config"
	chaoscrud "aegis/crud/chaos"

	"github.com/OperationsPAI/chaos-experiment/pkg/guidedcli"
	"github.com/sirupsen/logrus"
)

// Logging-only preflight; the result is not consulted by BuildInjection. Real
// catalog cutover deferred to step 5b when the executor moves to chaos service.

const (
	catalogSourceFlagKey = "aegis.injection.catalog_source"
	catalogSourceInProc  = "in_process"
	catalogSourceChaos   = "chaos_service"

	catalogPreflightTimeout = 5 * time.Second
)

// pointsListerFunc is the seam tests inject — production wires it to the
// generated SDK against config.GetChaosServiceURL().
type pointsListerFunc func(ctx context.Context, system, service, capability string) (matchedPointID string, httpStatus int, err error)

// runCatalogPreflight enumerates the guided configs and, when the etcd flag
// requests it, calls the chaos service to validate each Point exists. The
// return value is informational only — callers continue to BuildInjection
// regardless. Set lister to nil to use the default SDK-backed implementation.
func runCatalogPreflight(ctx context.Context, system string, configs []guidedcli.GuidedConfig, logEntry *logrus.Entry, lister pointsListerFunc) {
	source := strings.TrimSpace(config.GetString(catalogSourceFlagKey))
	if source == "" {
		source = catalogSourceInProc
	}
	if source != catalogSourceChaos {
		return
	}
	url := config.GetChaosServiceURL()
	if url == "" {
		// Silent override per design: missing URL = no chaos service to call.
		return
	}
	if lister == nil {
		lister = newSDKPointsLister(url)
	}

	for i, cfg := range configs {
		capability, ok := chaoscrud.ChaosTypeToCapability[strings.TrimSpace(cfg.ChaosType)]
		if !ok {
			logEntry.WithFields(logrus.Fields{
				"index":      i,
				"chaos_type": cfg.ChaosType,
			}).Warn("catalog preflight: no capability mapping for chaos_type; using in-process resolution")
			continue
		}
		service := strings.TrimSpace(cfg.App)
		callCtx, cancel := context.WithTimeout(ctx, catalogPreflightTimeout)
		pointID, status, err := lister(callCtx, system, service, capability)
		cancel()
		switch {
		case err != nil:
			logEntry.WithFields(logrus.Fields{
				"index":       i,
				"system":      system,
				"service":     service,
				"capability":  capability,
				"http_status": status,
			}).Warnf("chaos service catalog read failed, falling back to in-process: %v", err)
		case pointID == "":
			logEntry.WithFields(logrus.Fields{
				"index":      i,
				"system":     system,
				"service":    service,
				"capability": capability,
			}).Warn("point not found in chaos service catalog; using in-process resolution")
		default:
			logEntry.WithFields(logrus.Fields{
				"index":      i,
				"system":     system,
				"service":    service,
				"capability": capability,
				"point_id":   pointID,
			}).Info("catalog source: chaos_service")
		}
	}
}

// newSDKPointsLister returns a pointsListerFunc backed by the generated
// chaos-service Go SDK. Each invocation builds a fresh per-call configuration
// so test seams can override base URLs without process-wide state.
func newSDKPointsLister(baseURL string) pointsListerFunc {
	return func(ctx context.Context, system, service, capability string) (string, int, error) {
		cfg := apiclient.NewConfiguration()
		cfg.Servers = apiclient.ServerConfigurations{{URL: strings.TrimRight(baseURL, "/")}}
		cli := apiclient.NewAPIClient(cfg)
		req := cli.ChaosAPI.ChaosListSystemPoints(ctx, system).Limit(50)
		if service != "" {
			req = req.Service(service)
		}
		if capability != "" {
			req = req.Capability(capability)
		}
		resp, httpResp, err := req.Execute()
		status := 0
		if httpResp != nil {
			status = httpResp.StatusCode
		}
		if err != nil {
			return "", status, err
		}
		if status >= 500 {
			return "", status, fmt.Errorf("chaos service returned %d", status)
		}
		if resp == nil || resp.Data == nil {
			return "", status, nil
		}
		for _, p := range resp.Data.Points {
			if p.Id != nil && *p.Id != "" {
				return *p.Id, status, nil
			}
		}
		return "", status, nil
	}
}
