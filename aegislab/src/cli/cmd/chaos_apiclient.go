package cmd

import (
	"context"
	"net/http"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/client"
)

// newChaosAPIClient targets the standalone aegis-chaos service (port 8086),
// not the main aegislab backend on flagServer. The chaos service mounts
// handlers at /v1beta and authenticates via the same Bearer scheme.
func newChaosAPIClient() (*apiclient.APIClient, context.Context, error) {
	server, err := resolveChaosServer()
	if err != nil {
		return nil, nil, err
	}
	cfg := apiclient.NewConfiguration()
	cfg.HTTPClient = &http.Client{Transport: &captureTransport{base: client.TransportFor(resolveTLSOptions())}}
	cfg.Servers = apiclient.ServerConfigurations{{URL: strings.TrimRight(server, "/")}}
	ctx := context.Background()
	if flagToken != "" {
		ctx = context.WithValue(ctx, apiclient.ContextAPIKeys, map[string]apiclient.APIKey{
			"BearerAuth": {Key: flagToken, Prefix: "Bearer"},
		})
	}
	return apiclient.NewAPIClient(cfg), ctx, nil
}
