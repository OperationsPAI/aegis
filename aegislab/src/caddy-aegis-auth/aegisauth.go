package aegisauth

import (
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(AegisAuth{})
	httpcaddyfile.RegisterHandlerDirective("aegis_auth", parseCaddyfile)
}

const (
	headerAuthMode    = "X-Aegis-Auth-Mode"
	headerReqAudience = "X-Aegis-Required-Audiences"

	headerUserID       = "X-Aegis-User-Id"
	headerUserEmail    = "X-Aegis-User-Email"
	headerRoles        = "X-Aegis-Roles"
	headerTokenAud     = "X-Aegis-Token-Aud"
	headerTokenJti     = "X-Aegis-Token-Jti"
	headerUsername     = "X-Aegis-Username"
	headerIsActive     = "X-Aegis-Is-Active"
	headerIsAdmin      = "X-Aegis-Is-Admin"
	headerAuthType     = "X-Aegis-Auth-Type"
	headerAPIKeyID     = "X-Aegis-Api-Key-Id"
	headerAPIKeyScopes = "X-Aegis-Api-Key-Scopes"
	headerTaskID       = "X-Aegis-Task-Id"
	headerSignature    = "X-Aegis-Signature"
)

// canonicalOrder defines the HMAC signing order for trusted headers.
var canonicalOrder = []string{
	headerUserID, headerUserEmail, headerRoles, headerTokenAud, headerTokenJti,
	headerUsername, headerIsActive, headerIsAdmin, headerAuthType,
	headerAPIKeyID, headerAPIKeyScopes, headerTaskID,
}

type UnifiedClaims struct {
	Typ          string   `json:"typ"`
	UserID       int      `json:"user_id,omitempty"`
	Username     string   `json:"username,omitempty"`
	Email        string   `json:"email,omitempty"`
	IsActive     bool     `json:"is_active,omitempty"`
	IsAdmin      bool     `json:"is_admin,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	AuthType     string   `json:"auth_type,omitempty"`
	TaskID       string   `json:"task_id,omitempty"`
	Service      string   `json:"service,omitempty"`
	APIKeyID     int      `json:"api_key_id,omitempty"`
	APIKeyScopes []string `json:"api_key_scopes,omitempty"`
	jwt.RegisteredClaims
}

// AegisAuth is a Caddy HTTP middleware that validates JWTs against a
// remote JWKS endpoint and injects signed trusted headers for downstream
// services.
type AegisAuth struct {
	JWKSURLs            []string      `json:"jwks_urls"`
	HMACKey             string        `json:"hmac_key"`
	JWKSRefreshInterval caddy.Duration `json:"jwks_refresh_interval,omitempty"`

	hmacKey []byte
	keys    map[string]*rsa.PublicKey
	mu      *sync.RWMutex
	logger  *zap.Logger
	stop    chan struct{}
}

func (AegisAuth) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.aegis_auth",
		New: func() caddy.Module { return new(AegisAuth) },
	}
}

func (a *AegisAuth) Provision(ctx caddy.Context) error {
	a.logger = ctx.Logger()
	a.hmacKey = []byte(a.HMACKey)
	a.mu = &sync.RWMutex{}
	a.stop = make(chan struct{})

	if err := a.refreshKeys(); err != nil {
		return fmt.Errorf("initial JWKS fetch: %w", err)
	}

	interval := time.Duration(a.JWKSRefreshInterval)
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go a.refreshLoop(interval)
	return nil
}

func (a *AegisAuth) Cleanup() error {
	close(a.stop)
	return nil
}

func (a *AegisAuth) Validate() error {
	if len(a.JWKSURLs) == 0 {
		return errors.New("aegis_auth: at least one jwks_url is required")
	}
	if len(a.hmacKey) == 0 {
		return errors.New("aegis_auth: hmac_key is required")
	}
	return nil
}

func (a *AegisAuth) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	mode := r.Header.Get(headerAuthMode)
	if mode == "" {
		mode = "jwt"
	}

	// Strip control headers so upstreams cannot see them.
	r.Header.Del(headerAuthMode)
	r.Header.Del(headerReqAudience)

	if mode == "none" {
		return next.ServeHTTP(w, r)
	}

	raw := bearer(r)
	if raw == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil
	}

	claims, err := a.parseAndVerify(raw)
	if err != nil {
		a.logger.Debug("auth rejected", zap.String("path", r.URL.Path), zap.Error(err))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil
	}

	audiences := r.Header.Get(headerReqAudience)

	switch mode {
	case "jwt":
		if err := checkAudience(claims, audiences); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return nil
		}
	case "service_token":
		if claims.Typ != "task" && claims.Typ != "service_account" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return nil
		}
	case "jwt_or_service":
		if claims.Typ == "human" {
			if err := checkAudience(claims, audiences); err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return nil
			}
		}
	default:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil
	}

	a.injectHeaders(r, claims)
	return next.ServeHTTP(w, r)
}

// --- JWT verification ---

func (a *AegisAuth) parseAndVerify(tokenString string) (*UnifiedClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &UnifiedClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		a.mu.RLock()
		defer a.mu.RUnlock()
		pub, ok := a.keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown kid %q", kid)
		}
		return pub, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*UnifiedClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	if claims.Typ == "human" && !claims.IsActive {
		return nil, errors.New("user account is inactive")
	}
	return claims, nil
}

// --- Header injection ---

func (a *AegisAuth) injectHeaders(r *http.Request, c *UnifiedClaims) {
	aud := ""
	if len(c.Audience) > 0 {
		aud = c.Audience[0]
	}
	isActive := boolStr(c.IsActive)
	isAdmin := boolStr(c.IsAdmin)

	vals := map[string]string{
		headerUserID:       strconv.Itoa(c.UserID),
		headerUserEmail:    c.Email,
		headerRoles:        strings.Join(c.Roles, ","),
		headerTokenAud:     aud,
		headerTokenJti:     c.ID,
		headerUsername:     c.Username,
		headerIsActive:     isActive,
		headerIsAdmin:      isAdmin,
		headerAuthType:     c.AuthType,
		headerAPIKeyID:     strconv.Itoa(c.APIKeyID),
		headerAPIKeyScopes: strings.Join(c.APIKeyScopes, ","),
		headerTaskID:       c.TaskID,
	}

	if c.Typ == "task" || c.Typ == "service_account" {
		vals[headerUserID] = "0"
		vals[headerRoles] = "service:" + c.Service
		vals[headerUsername] = "service"
		vals[headerIsActive] = "1"
		vals[headerIsAdmin] = "0"
		if c.AuthType == "" {
			vals[headerAuthType] = "service"
		}
	}

	canonical := make([]string, len(canonicalOrder))
	for i, k := range canonicalOrder {
		canonical[i] = vals[k]
	}
	mac := hmac.New(sha256.New, a.hmacKey)
	mac.Write([]byte(strings.Join(canonical, "|")))
	sig := hex.EncodeToString(mac.Sum(nil))

	for k, v := range vals {
		r.Header.Set(k, v)
	}
	r.Header.Set(headerSignature, sig)
}

// --- JWKS refresh ---

func (a *AegisAuth) refreshLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := a.refreshKeys(); err != nil {
				a.logger.Error("jwks refresh failed", zap.Error(err))
			}
		case <-a.stop:
			return
		}
	}
}

func (a *AegisAuth) refreshKeys() error {
	client := &http.Client{Timeout: 10 * time.Second}
	merged := make(map[string]*rsa.PublicKey)
	var lastErr error
	for _, url := range a.JWKSURLs {
		keys, err := fetchJWKSFrom(client, url)
		if err != nil {
			a.logger.Warn("jwks fetch failed for source", zap.String("url", url), zap.Error(err))
			lastErr = err
			continue
		}
		for kid, pub := range keys {
			if _, exists := merged[kid]; !exists {
				merged[kid] = pub
			}
		}
	}
	if len(merged) == 0 {
		if lastErr != nil {
			return fmt.Errorf("all JWKS sources failed, last error: %w", lastErr)
		}
		return errors.New("jwks documents contained no usable RSA keys")
	}
	a.mu.Lock()
	a.keys = merged
	a.mu.Unlock()
	a.logger.Info("jwks refreshed", zap.Int("keys", len(merged)), zap.Int("sources", len(a.JWKSURLs)))
	return nil
}

func fetchJWKSFrom(client *http.Client, url string) (map[string]*rsa.PublicKey, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks %s returned status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read jwks body from %s: %w", url, err)
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode jwks from %s: %w", url, err)
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		e := new(big.Int).SetBytes(eBytes)
		if !e.IsInt64() {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}
	}
	return keys, nil
}

// --- Caddyfile parsing ---

func (a *AegisAuth) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // consume directive name
	for d.NextBlock(0) {
		switch d.Val() {
		case "jwks_url":
			if !d.NextArg() {
				return d.ArgErr()
			}
			a.JWKSURLs = append(a.JWKSURLs, d.Val())
		case "hmac_key":
			if !d.NextArg() {
				return d.ArgErr()
			}
			a.HMACKey = d.Val()
		case "jwks_refresh_interval":
			if !d.NextArg() {
				return d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return d.Errf("invalid duration %q: %v", d.Val(), err)
			}
			a.JWKSRefreshInterval = caddy.Duration(dur)
		default:
			return d.Errf("unknown option %q", d.Val())
		}
	}
	return nil
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var a AegisAuth
	if err := a.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return &a, nil
}

// --- helpers ---

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func checkAudience(c *UnifiedClaims, required string) error {
	if required == "" {
		return nil
	}
	want := strings.Split(required, ",")
	for _, w := range want {
		w = strings.TrimSpace(w)
		for _, h := range c.Audience {
			if h == w {
				return nil
			}
		}
	}
	return errors.New("audience mismatch")
}

// Interface guards
var (
	_ caddy.Module                = (*AegisAuth)(nil)
	_ caddy.Provisioner           = (*AegisAuth)(nil)
	_ caddy.Validator             = (*AegisAuth)(nil)
	_ caddy.CleanerUpper          = (*AegisAuth)(nil)
	_ caddyhttp.MiddlewareHandler = (*AegisAuth)(nil)
	_ caddyfile.Unmarshaler       = (*AegisAuth)(nil)
)
