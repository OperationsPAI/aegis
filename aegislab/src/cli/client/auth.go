package client

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aegis/platform/consts"
)

const (
	apiKeyTokenPath = consts.APIPathAuthAPIKeyToken
	// ssoTokenPath is the OIDC token endpoint exposed by the aegis-sso
	// service. Reachable via the cluster gateway (auth=none route on
	// `/token`) so aegisctl can use it through the same `--server` URL.
	ssoTokenPath = "/token"
	// ssoCLIClientID is a public OIDC client (no client_secret) seeded
	// by aegis-sso for the CLI to use the password grant.
	ssoCLIClientID = "aegis-cli"
)

// APIKeyTokenDebug contains the fully materialized signed request data for
// POST /api/v2/auth/api-key/token.
type APIKeyTokenDebug struct {
	Method          string
	Path            string
	KeyID           string
	Timestamp       string
	Nonce           string
	BodySHA256      string
	CanonicalString string
	Signature       string
}

func (d *APIKeyTokenDebug) Headers() map[string]string {
	return map[string]string{
		"X-Key-Id":    d.KeyID,
		"X-Timestamp": d.Timestamp,
		"X-Nonce":     d.Nonce,
		"X-Signature": d.Signature,
	}
}

// apiKeyTokenResponseData matches dto.APIKeyTokenResp.
type apiKeyTokenResponseData struct {
	Token     string    `json:"token"`
	TokenType string    `json:"token_type"`
	ExpiresAt time.Time `json:"expires_at"`
	AuthType  string    `json:"auth_type"`
	KeyID     string    `json:"key_id"`
}

// tokenRefreshRequest matches dto.TokenRefreshReq.
type tokenRefreshRequest struct {
	Token string `json:"token"`
}

// tokenRefreshResponseData matches dto.TokenRefreshResp.
type tokenRefreshResponseData struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// LoginResult contains the result of a successful login.
type LoginResult struct {
	Token     string
	ExpiresAt time.Time
	AuthType  string
	KeyID     string
	Username  string
}

// ssoTokenResponse mirrors the OIDC /token success body. All other
// fields (refresh_token, scope, id_token) are returned by aegis-sso but
// the CLI only needs the access token + lifetime today.
type ssoTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	IDToken     string `json:"id_token,omitempty"`
}

type ssoTokenError struct {
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// LoginWithPassword exchanges a username/password pair for a bearer
// token via the SSO `/token` endpoint (OIDC password grant against the
// public `aegis-cli` client). This replaces the legacy
// /api/v2/auth/login flow — both ultimately route to the aegis-sso
// process and mint the same JWT, but the OIDC path is the supported
// surface going forward.
func LoginWithPassword(server, username, password string) (*LoginResult, error) {
	return LoginWithPasswordTLS(server, username, password, TLSOptions{})
}

// LoginWithPasswordTLS is LoginWithPassword with explicit TLS options.
func LoginWithPasswordTLS(server, username, password string, opts TLSOptions) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if password == "" {
		return nil, fmt.Errorf("password is required")
	}

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", ssoCLIClientID)
	form.Set("username", username)
	form.Set("password", password)

	tokURL := strings.TrimRight(server, "/") + ssoTokenPath
	req, err := http.NewRequest(http.MethodPost, tokURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: TransportFor(opts)}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var oerr ssoTokenError
		_ = json.Unmarshal(body, &oerr)
		switch {
		case oerr.Description != "":
			return nil, fmt.Errorf("login failed: %s (%s)", oerr.Description, oerr.Code)
		case oerr.Code != "":
			return nil, fmt.Errorf("login failed: %s", oerr.Code)
		default:
			return nil, fmt.Errorf("login failed: HTTP %d: %s", resp.StatusCode, string(body))
		}
	}

	var tok ssoTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("login failed: empty access_token in /token response")
	}

	return &LoginResult{
		Token:     tok.AccessToken,
		ExpiresAt: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		AuthType:  "password",
		Username:  username,
	}, nil
}

// LoginWithAPIKey exchanges a Key ID / Key Secret signature for a bearer token.
func LoginWithAPIKey(server, keyID, keySecret string) (*LoginResult, error) {
	return LoginWithAPIKeyTLS(server, keyID, keySecret, TLSOptions{})
}

// LoginWithAPIKeyTLS is LoginWithAPIKey with explicit TLS options.
func LoginWithAPIKeyTLS(server, keyID, keySecret string, opts TLSOptions) (*LoginResult, error) {
	keyID = strings.TrimSpace(keyID)
	keySecret = strings.TrimSpace(keySecret)
	if keyID == "" {
		return nil, fmt.Errorf("key id is required")
	}
	if keySecret == "" {
		return nil, fmt.Errorf("key secret is required")
	}

	c := NewClientWithTLS(server, "", 30*time.Second, opts)
	debugInfo, err := PrepareAPIKeyTokenDebug(keyID, keySecret, time.Now().UTC(), "")
	if err != nil {
		return nil, fmt.Errorf("prepare signed headers: %w", err)
	}

	var resp APIResponse[apiKeyTokenResponseData]
	if err := c.PostWithHeaders(apiKeyTokenPath, debugInfo.Headers(), &resp); err != nil {
		return nil, fmt.Errorf("exchange api key token failed: %w", err)
	}

	return &LoginResult{
		Token:     resp.Data.Token,
		ExpiresAt: resp.Data.ExpiresAt,
		AuthType:  resp.Data.AuthType,
		KeyID:     resp.Data.KeyID,
	}, nil
}

// RefreshToken refreshes an existing JWT token.
func RefreshToken(server, currentToken string) (string, time.Time, error) {
	c := NewClient(server, currentToken, 30*time.Second)

	var resp APIResponse[tokenRefreshResponseData]
	err := c.Post(consts.APIPathAuthRefresh, tokenRefreshRequest{
		Token: currentToken,
	}, &resp)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token refresh failed: %w", err)
	}

	return resp.Data.Token, resp.Data.ExpiresAt, nil
}

// ProfileData represents the user profile returned by GET /api/v2/auth/profile.
type ProfileData struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar,omitempty"`
	Role     string `json:"role,omitempty"`
}

// GetProfile fetches the current user's profile.
func GetProfile(server, token string) (*ProfileData, error) {
	c := NewClient(server, token, 30*time.Second)

	var resp APIResponse[ProfileData]
	if err := c.Get(consts.APIPathAuthProfile, &resp); err != nil {
		return nil, fmt.Errorf("get profile failed: %w", err)
	}

	return &resp.Data, nil
}

// IsTokenExpired checks whether the stored token expiry has passed.
func IsTokenExpired(expiry time.Time) bool {
	if expiry.IsZero() {
		return false // unknown expiry — assume valid
	}
	return time.Now().After(expiry)
}

// PrepareAPIKeyTokenDebug builds the canonical string, signature, and
// headers for the token exchange request.
func PrepareAPIKeyTokenDebug(keyID, keySecret string, now time.Time, nonce string) (*APIKeyTokenDebug, error) {
	keyID = strings.TrimSpace(keyID)
	keySecret = strings.TrimSpace(keySecret)
	nonce = strings.TrimSpace(nonce)
	if keyID == "" {
		return nil, fmt.Errorf("key id is required")
	}
	if keySecret == "" {
		return nil, fmt.Errorf("key secret is required")
	}
	var err error
	if nonce == "" {
		nonce, err = newAPIKeyNonce()
		if err != nil {
			return nil, err
		}
	}

	timestamp := strconv.FormatInt(now.Unix(), 10)
	bodySHA256 := sha256Hex("")
	canonical := canonicalAPIKeyString("POST", apiKeyTokenPath, timestamp, nonce, bodySHA256)

	return &APIKeyTokenDebug{
		Method:          "POST",
		Path:            apiKeyTokenPath,
		KeyID:           keyID,
		Timestamp:       timestamp,
		Nonce:           nonce,
		BodySHA256:      bodySHA256,
		CanonicalString: canonical,
		Signature:       signAPIKeyRequest(keySecret, canonical),
	}, nil
}

func buildAPIKeyHeaders(keyID, keySecret string, now time.Time, path string) (map[string]string, error) {
	debugInfo, err := prepareAPIKeyDebug(keyID, keySecret, now, path, "")
	if err != nil {
		return nil, err
	}
	return debugInfo.Headers(), nil
}

func prepareAPIKeyDebug(keyID, keySecret string, now time.Time, path, nonce string) (*APIKeyTokenDebug, error) {
	keyID = strings.TrimSpace(keyID)
	keySecret = strings.TrimSpace(keySecret)
	nonce = strings.TrimSpace(nonce)
	if keyID == "" {
		return nil, fmt.Errorf("key id is required")
	}
	if keySecret == "" {
		return nil, fmt.Errorf("key secret is required")
	}
	var err error
	if nonce == "" {
		nonce, err = newAPIKeyNonce()
		if err != nil {
			return nil, err
		}
	}

	timestamp := strconv.FormatInt(now.Unix(), 10)
	bodySHA256 := sha256Hex("")
	canonical := canonicalAPIKeyString("POST", path, timestamp, nonce, bodySHA256)

	return &APIKeyTokenDebug{
		Method:          "POST",
		Path:            path,
		KeyID:           keyID,
		Timestamp:       timestamp,
		Nonce:           nonce,
		BodySHA256:      bodySHA256,
		CanonicalString: canonical,
		Signature:       signAPIKeyRequest(keySecret, canonical),
	}, nil
}

func canonicalAPIKeyString(method, path, timestamp, nonce, bodySHA256 string) string {
	return strings.Join([]string{
		strings.ToUpper(method),
		path,
		timestamp,
		nonce,
		bodySHA256,
	}, "\n")
}

func signAPIKeyRequest(secretKey, payload string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func newAPIKeyNonce() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(nonce), nil
}

func sha256Hex(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
