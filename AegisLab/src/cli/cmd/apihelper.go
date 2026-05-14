package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"aegis/cli/apiclient"
	"aegis/cli/internal/cli/clierr"
	"aegis/cli/internal/cli/exitcode"
)

// lastRespHeaders holds the response headers of the most recent generated-
// client call. The CLI is single-shot per process, so a package-level slot
// is safe enough; it lets apiClientCLIError surface X-Request-Id (which the
// generator-level *GenericOpenAPIError otherwise drops on the floor).
var (
	lastRespMu      sync.Mutex
	lastRespHeaders http.Header
)

type captureTransport struct{ base http.RoundTripper }

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if resp != nil {
		lastRespMu.Lock()
		lastRespHeaders = resp.Header.Clone()
		lastRespMu.Unlock()
	}
	return resp, err
}

func getLastResponseHeader(name string) string {
	lastRespMu.Lock()
	defer lastRespMu.Unlock()
	if lastRespHeaders == nil {
		return ""
	}
	return lastRespHeaders.Get(name)
}

// newAPIClient returns an apiclient.APIClient pointed at flagServer with the
// loaded JWT token applied via context. Callers run methods like
//
//	cli, ctx := newAPIClient()
//	resp, _, err := cli.ShareAPI.ShareList(ctx).Page(1).Execute()
//
// using the returned ctx so the BearerAuth header gets attached.
func newAPIClient() (*apiclient.APIClient, context.Context) {
	cfg := apiclient.NewConfiguration()
	cfg.HTTPClient = &http.Client{Transport: &captureTransport{base: http.DefaultTransport}}
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

// apiClientCLIError converts a *apiclient.GenericOpenAPIError into the
// structured CLIError envelope used by --output=json consumers. Returns
// nil if err is not a generated-client error.
func apiClientCLIError(err error) *clierr.CLIError {
	var apiErr *apiclient.GenericOpenAPIError
	if !errors.As(err, &apiErr) {
		return nil
	}
	exitCode := exitcode.ForError(err)
	typ := "server"
	switch exitCode {
	case exitcode.CodeAuthFailure:
		typ = "auth"
	case exitcode.CodeNotFound:
		typ = "not_found"
	case exitcode.CodeConflict:
		typ = "conflict"
	case exitcode.CodeUsage:
		typ = "usage"
	case exitcode.CodeDecodeFailure:
		typ = "decode"
	}
	msg := apiErr.Error()
	// `json: cannot unmarshal X into Go struct field F of type T` carries
	// the expected type after `of type` — surface it as the conventional
	// "expected T" suffix so consumers can grep on the keyword.
	if typ == "decode" && strings.Contains(msg, "of type ") {
		if idx := strings.LastIndex(msg, "of type "); idx >= 0 {
			expected := strings.TrimSpace(msg[idx+len("of type "):])
			msg = msg + " (expected " + expected + ")"
		}
	}
	out := &clierr.CLIError{
		Type:     typ,
		Message:  msg,
		ExitCode: exitCode,
	}
	// Pull request_id + a sanitized server-side cause out of the body
	// so --output=json keeps the original observability fields.
	// X-Request-Id from the last response header is the canonical
	// observability handle (server logs key off it). Body-level
	// `request_id` is unreliable — handlers populate it inconsistently.
	out.RequestID = getLastResponseHeader("X-Request-Id")
	if body := apiErr.Body(); len(body) > 0 {
		var env struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &env) == nil {
			if env.Message != "" && exitCode != exitcode.CodeServerError {
				// Don't leak generic 5xx server messages; keep them only
				// when the status code is in the 4xx user-actionable band.
				out.Cause = env.Message
			}
		}
	}
	return out
}
