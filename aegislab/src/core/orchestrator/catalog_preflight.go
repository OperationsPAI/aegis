package consumer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"aegis/cli/apiclient"
	chaoscrud "aegis/crud/chaos"
	"aegis/platform/config"

	guidedcli "aegis/platform/chaos"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// catalogReadSourceTotal records which source resolved the catalog read for
// each guided config processed by the preflight. soak observability: a spike
// in source="in_process" while chaos_service is the configured default
// indicates the chaos service is being bypassed (etcd flipped, URL missing,
// or DB fallback firing).
var catalogReadSourceTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "aegis_catalog_read_source_total",
	Help: "Catalog reads resolved by source for §11.4.5 cutover observability.",
}, []string{"source"})

// OutboundBearerEnv is read once at lister construction. Symmetric to
// CHAOS_INBOUND_BEARER (chaos service side); kept as a separate env var
// from CHAOS_WEBHOOK_BEARER (chaos → backend direction) so the two
// service-to-service hops can be rotated independently. The constant
// itself lives in platform/chaos so both this side and chaossystem's
// candidate enumerator read the same name.
const OutboundBearerEnv = guidedcli.OutboundBearerEnv

var outboundBearerAttachOnce sync.Once

const (
	catalogSourceInProc = "in_process"
	catalogSourceChaos  = "chaos_service"

	catalogPreflightTimeout = 5 * time.Second
)

// pointsListerFunc is the seam tests inject — production wires it to the
// generated SDK against config.GetChaosServiceURL().
type pointsListerFunc func(ctx context.Context, system, service, capability string) (matchedPointID string, httpStatus int, err error)

// runCatalogPreflight enumerates the guided configs and resolves each Point
// against the chaos service catalog. Failures fall back silently (metric
// records the effective source); BuildInjection continues regardless. Set
// lister to nil to use the default production implementation.
//
// logicalSystem MUST be the catalog-side system name (e.g. "otel-demo"),
// not the concrete pool-allocated namespace (e.g. "otel-demo0"). The chaos
// service indexes chaos_points by Point.SystemName, which is populated from
// the seed manifests' metadata.system field — passing a namespace-suffixed
// value here will silently fall through to WARN-fallback every call.
// runCatalogPreflight returns, per config (index-aligned), the catalog-resolved
// point_id, or "" when it could not be resolved (no URL, miss, error, or no
// capability mapping). The dispatcher uses a non-empty resolved id to address
// the Point directly instead of recomputing the content hash — eliminating the
// cross-process hash-agreement (system/namespace/chart_version) that caused the
// opaque 404 class. An empty entry tells the dispatcher to fall back to
// in-process hash derivation (kind / in-process mode).
func runCatalogPreflight(ctx context.Context, logicalSystem string, configs []guidedcli.GuidedConfig, _ *gorm.DB, logEntry *logrus.Entry, lister pointsListerFunc) []string {
	url := config.GetChaosServiceURL()
	if url == "" {
		// Missing URL = no chaos service to call. Each config still counts
		// as an in_process resolution so the metric reflects the effective
		// source.
		for range configs {
			catalogReadSourceTotal.WithLabelValues(catalogSourceInProc).Inc()
		}
		return make([]string, len(configs))
	}
	if lister == nil {
		lister = newSDKPointsLister(url, logEntry)
	}
	return runChaosServicePreflight(ctx, logicalSystem, configs, logEntry, lister)
}

func runChaosServicePreflight(ctx context.Context, logicalSystem string, configs []guidedcli.GuidedConfig, logEntry *logrus.Entry, lister pointsListerFunc) []string {
	resolved := make([]string, len(configs))
	for i, cfg := range configs {
		capability, ok := chaoscrud.ChaosTypeToCapability[strings.TrimSpace(cfg.ChaosType)]
		if !ok {
			catalogReadSourceTotal.WithLabelValues(catalogSourceInProc).Inc()
			logEntry.WithFields(logrus.Fields{
				"index":      i,
				"chaos_type": cfg.ChaosType,
			}).Warn("catalog preflight: no capability mapping for chaos_type; using in-process resolution")
			continue
		}
		service := strings.TrimSpace(cfg.App)
		callCtx, cancel := context.WithTimeout(ctx, catalogPreflightTimeout)
		pointID, status, err := lister(callCtx, logicalSystem, service, capability)
		cancel()
		switch {
		case err != nil:
			catalogReadSourceTotal.WithLabelValues(catalogSourceInProc).Inc()
			logEntry.WithFields(logrus.Fields{
				"index":       i,
				"system":      logicalSystem,
				"service":     service,
				"capability":  capability,
				"http_status": status,
			}).Warnf("chaos service catalog read failed, falling back to in-process: %v", err)
		case pointID == "":
			catalogReadSourceTotal.WithLabelValues(catalogSourceInProc).Inc()
			logEntry.WithFields(logrus.Fields{
				"index":      i,
				"system":     logicalSystem,
				"service":    service,
				"capability": capability,
			}).Warn("point not found in chaos service catalog; using in-process resolution")
		default:
			resolved[i] = pointID
			catalogReadSourceTotal.WithLabelValues(catalogSourceChaos).Inc()
			logEntry.WithFields(logrus.Fields{
				"index":      i,
				"system":     logicalSystem,
				"service":    service,
				"capability": capability,
				"point_id":   pointID,
			}).Info("catalog source: chaos_service")
		}
	}
	return resolved
}

// newSDKPointsLister returns a pointsListerFunc backed by the generated
// chaos-service Go SDK. Each invocation builds a fresh per-call configuration
// so test seams can override base URLs without process-wide state.
//
// The lister prefers the backend→chaos SA token minted at boot
// (chaosSATokenRef). When the SA mint hasn't run it falls back to
// CHAOS_OUTBOUND_BEARER with a one-shot deprecation ERROR. Empty on both
// sides → no header (kind dev path); chaos service then falls back to its
// existing TrustedHeaderAuth chain.
func newSDKPointsLister(baseURL string, logEntry *logrus.Entry) pointsListerFunc {
	return func(ctx context.Context, system, service, capability string) (string, int, error) {
		bearer := resolveChaosOutboundBearer()
		if bearer != "" && logEntry != nil {
			outboundBearerAttachOnce.Do(func() {
				logEntry.Info("chaos outbound: attaching bearer to catalog preflight requests")
			})
		}
		cfg := apiclient.NewConfiguration()
		cfg.HTTPClient = newOtelHTTPClient()
		cfg.Servers = apiclient.ServerConfigurations{{URL: strings.TrimRight(baseURL, "/")}}
		if bearer != "" {
			cfg.AddDefaultHeader("Authorization", "Bearer "+bearer)
		}
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
