package client

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aegis/platform/consts"
)

const apiKeyTokenPath = consts.APIPathAuthAPIKeyToken

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

// loginRequest matches dto.LoginReq for POST /api/v2/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponseData matches dto.LoginResp.
type loginResponseData struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// LoginWithPassword exchanges a username/password pair for a bearer
// token via POST /api/v2/auth/login (issues a unified aegis JWT).
func LoginWithPassword(server, username, password string) (*LoginResult, error) {
	return LoginWithPasswordTLS(server, username, password, TLSOptions{})
}

// LoginWithPasswordTLS is LoginWithPassword with explicit TLS options.
//
// Goes through the dedicated POST /api/v2/auth/login endpoint (issues a
// unified aegis JWT). The OIDC password grant it used to call was removed in
// the auth unification (RFC #550); the CLI command surface is unchanged.
func LoginWithPasswordTLS(server, username, password string, opts TLSOptions) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if password == "" {
		return nil, fmt.Errorf("password is required")
	}

	c := NewClientWithTLS(server, "", 30*time.Second, opts)

	var resp APIResponse[loginResponseData]
	if err := c.Post(consts.APIPathAuthLogin, loginRequest{
		Username: username,
		Password: password,
	}, &resp); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}
	if resp.Data.Token == "" {
		return nil, fmt.Errorf("login failed: empty token in response")
	}

	return &LoginResult{
		Token:     resp.Data.Token,
		ExpiresAt: resp.Data.ExpiresAt,
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
	return RefreshTokenTLS(server, currentToken, TLSOptions{})
}

// RefreshTokenTLS is RefreshToken with explicit TLS options so transparent
// refresh honors the same trust config as the original request.
func RefreshTokenTLS(server, currentToken string, opts TLSOptions) (string, time.Time, error) {
	c := NewClientWithTLS(server, currentToken, 30*time.Second, opts)

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

// ParseJWTExp decodes the unverified `exp` claim from a JWT. Signature is not
// validated — that is the server's job. Returns the zero time if the token is
// not a JWT, lacks an `exp` claim, or fails to decode.
func ParseJWTExp(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}
	if claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
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
