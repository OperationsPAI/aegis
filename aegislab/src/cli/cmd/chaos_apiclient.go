package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"aegis/cli/apiclient"
	"aegis/cli/client"
)

const chaosServerEnv = "AEGIS_CHAOS_SERVER"

// flagChaosServer backs the deprecated --chaos-server flag. Helm chart
// post-install hooks ship with this flag baked into their templates, so the
// flag stays as an alias for --server / AEGIS_SERVER (which the SDK already
// targets). Chart authors should migrate to --server; the warning below
// nudges them.
var flagChaosServer string

var chaosServerDeprecationOnce sync.Once

func resolveChaosServer() (string, error) {
	if flagChaosServer != "" {
		warnChaosServerDeprecated()
		return flagChaosServer, nil
	}
	if v := os.Getenv(chaosServerEnv); v != "" {
		warnChaosServerDeprecated()
		return v, nil
	}
	if flagServer != "" {
		return strings.TrimRight(flagServer, "/") + "/v1beta/chaos", nil
	}
	return "", usageErrorf("aegis-chaos URL required: pass --server, set AEGIS_SERVER, "+
		"or (deprecated) pass --chaos-server / set %s", chaosServerEnv)
}

func warnChaosServerDeprecated() {
	chaosServerDeprecationOnce.Do(func() {
		fmt.Fprintln(os.Stderr,
			"warning: --chaos-server / AEGIS_CHAOS_SERVER is deprecated; "+
				"use --server / AEGIS_SERVER (the gateway federates /v1beta/chaos). "+
				"Helm chart hooks should migrate.")
	})
}

// newChaosAPIClient targets aegis-chaos. When --chaos-server is set it goes
// direct to the chaos ClusterIP; otherwise it falls through to <server>/v1beta/chaos
// where the gateway federates the call into the chaos service.
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
