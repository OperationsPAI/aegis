package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OIDCDiscovery contains the endpoints from OpenID Connect discovery.
type OIDCDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	Issuer                string `json:"issuer"`
}

// OIDCTokenResponse contains the tokens returned by an OIDC token exchange.
type OIDCTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
}

// DiscoverOIDC fetches the OpenID Connect discovery document from the issuer.
func DiscoverOIDC(issuer string, opts TLSOptions) (*OIDCDiscovery, error) {
	issuer = strings.TrimRight(issuer, "/")
	discoveryURL := issuer + "/.well-known/openid-configuration"

	httpClient := &http.Client{
		Timeout:   10 * time.Second,
		Transport: TransportFor(opts),
	}
	resp, err := httpClient.Get(discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OIDC discovery returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var discovery OIDCDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return nil, fmt.Errorf("decode OIDC discovery: %w", err)
	}

	if discovery.AuthorizationEndpoint == "" {
		return nil, fmt.Errorf("OIDC discovery missing authorization_endpoint")
	}
	if discovery.TokenEndpoint == "" {
		return nil, fmt.Errorf("OIDC discovery missing token_endpoint")
	}

	return &discovery, nil
}

// PKCEChallenge holds a PKCE code verifier and its S256 challenge.
type PKCEChallenge struct {
	Verifier  string
	Challenge string
}

// GeneratePKCE generates a PKCE code verifier (32 random bytes, base64url)
// and the corresponding S256 challenge.
func GeneratePKCE() (*PKCEChallenge, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	return &PKCEChallenge{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}

// GenerateState generates a random state parameter for CSRF protection.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ExchangeOIDCCode exchanges an authorization code for tokens using the
// PKCE code verifier.
func ExchangeOIDCCode(tokenEndpoint, code, redirectURI, clientID, codeVerifier string, opts TLSOptions) (*OIDCTokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {codeVerifier},
	}

	return postOIDCToken(tokenEndpoint, data, opts)
}

// RefreshOIDCToken uses a refresh token to obtain new access and refresh tokens.
func RefreshOIDCToken(tokenEndpoint, refreshToken, clientID string, opts TLSOptions) (*OIDCTokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}

	return postOIDCToken(tokenEndpoint, data, opts)
}

func postOIDCToken(tokenEndpoint string, data url.Values, opts TLSOptions) (*OIDCTokenResponse, error) {
	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: TransportFor(opts),
	}

	resp, err := httpClient.PostForm(tokenEndpoint, data)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp OIDCTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token response contains empty access_token")
	}

	return &tokenResp, nil
}

// OIDCCallbackResult holds the authorization code and state from the
// browser redirect callback.
type OIDCCallbackResult struct {
	Code  string
	State string
	Error string
}

const callbackSuccessHTML = `<!DOCTYPE html>
<html><head><title>Login Successful</title></head>
<body style="font-family:sans-serif;text-align:center;padding-top:50px">
<h2>Login successful</h2>
<p>You can close this tab and return to the terminal.</p>
</body></html>`

const callbackErrorHTML = `<!DOCTYPE html>
<html><head><title>Login Failed</title></head>
<body style="font-family:sans-serif;text-align:center;padding-top:50px">
<h2>Login failed</h2>
<p>%s</p>
<p>Return to the terminal for details.</p>
</body></html>`

// WaitForOIDCCallback starts a temporary HTTP server on the given listener
// and waits for a single OAuth2 callback. The server shuts down after
// receiving the callback or when the timeout expires.
func WaitForOIDCCallback(listener net.Listener, expectedState string, timeout time.Duration) (*OIDCCallbackResult, error) {
	resultCh := make(chan *OIDCCallbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		if errMsg := q.Get("error"); errMsg != "" {
			desc := q.Get("error_description")
			if desc != "" {
				errMsg += ": " + desc
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, callbackErrorHTML, errMsg)
			select {
			case resultCh <- &OIDCCallbackResult{Error: errMsg}:
			default:
			}
			return
		}

		code := q.Get("code")
		state := q.Get("state")

		if state != expectedState {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, callbackErrorHTML, "state mismatch")
			select {
			case resultCh <- &OIDCCallbackResult{Error: "state parameter mismatch"}:
			default:
			}
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, callbackSuccessHTML)
		select {
		case resultCh <- &OIDCCallbackResult{Code: code, State: state}:
		default:
		}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener) //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("timed out waiting for browser callback (waited %s)", timeout)
	}
}

// OpenBrowser opens the given URL in the user's default browser.
func OpenBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
	return cmd.Start()
}

// BuildAuthorizationURL constructs the OIDC authorization URL with PKCE parameters.
func BuildAuthorizationURL(authEndpoint, clientID, redirectURI, scope, state, codeChallenge string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scope},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return authEndpoint + "?" + params.Encode()
}
