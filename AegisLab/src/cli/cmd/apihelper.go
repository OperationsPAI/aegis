package cmd

import (
	"context"
	"strings"

	"aegis/cli/apiclient"
)

// newAPIClient returns an apiclient.APIClient pointed at flagServer with the
// loaded JWT token applied via context. Callers run methods like
//
//	cli, ctx := newAPIClient()
//	resp, _, err := cli.ShareAPI.ShareList(ctx).Page(1).Execute()
//
// using the returned ctx so the BearerAuth header gets attached.
func newAPIClient() (*apiclient.APIClient, context.Context) {
	cfg := apiclient.NewConfiguration()
	if flagServer != "" {
		cfg.Servers = apiclient.ServerConfigurations{{URL: strings.TrimRight(flagServer, "/")}}
	}
	ctx := context.Background()
	if flagToken != "" {
		ctx = context.WithValue(ctx, apiclient.ContextAPIKeys, map[string]apiclient.APIKey{
			"BearerAuth": {Key: flagToken, Prefix: "Bearer"},
		})
	}
	return apiclient.NewAPIClient(cfg), ctx
}
