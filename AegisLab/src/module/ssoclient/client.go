package ssoclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"aegis/infra/jwtkeys"
	"aegis/utils"

	"github.com/golang-jwt/jwt/v5"
	lru "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/sirupsen/logrus"
)

const (
	defaultHTTPTimeout = 5 * time.Second
	checkCacheSize     = 10000
	checkCacheTTL      = 30 * time.Second
	serviceTokenSkew   = 60 * time.Second
)

type Config struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	JWKSURL      string
}

// Client is the AegisLab-side bridge to aegis-sso. Use NewClient via fx; tests
// can construct it directly via newClientForTest.
type Client struct {
	cfg        Config
	httpClient *http.Client
	verifier   *jwtkeys.Verifier
	cache      *lru.LRU[string, bool]

	mu           sync.Mutex
	serviceToken string
	tokenExp     time.Time
}

// NewClient builds a client wired against a remote SSO. The caller is
// responsible for starting JWKS refresh and bootstrap (the fx module does
// both).
func NewClient(cfg Config, verifier *jwtkeys.Verifier) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		verifier: verifier,
		cache:    lru.NewLRU[string, bool](checkCacheSize, nil, checkCacheTTL),
	}
}

func (c *Client) VerifyToken(ctx context.Context, raw string) (*utils.Claims, error) {
	_ = ctx
	return utils.ParseToken(raw, c.verifier.Resolve)
}

func (c *Client) VerifyServiceToken(ctx context.Context, raw string) (*utils.ServiceClaims, error) {
	_ = ctx
	return utils.ParseServiceToken(raw, c.verifier.Resolve)
}

func (c *Client) GetUser(ctx context.Context, id int) (*UserInfo, error) {
	var u UserInfo
	if err := c.doJSON(ctx, http.MethodGet, "/v1/users/"+strconv.Itoa(id), nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) GetUsers(ctx context.Context, ids []int) (map[int]*UserInfo, error) {
	var resp map[string]*UserInfo
	if err := c.doJSON(ctx, http.MethodPost, "/v1/users:batch", map[string]any{"ids": ids}, &resp); err != nil {
		return nil, err
	}
	out := make(map[int]*UserInfo, len(resp))
	for k, v := range resp {
		id, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		out[id] = v
	}
	return out, nil
}

func cacheKey(p CheckParams) string {
	return fmt.Sprintf("%d|%s|%s|%s", p.UserID, p.Permission, p.ScopeType, p.ScopeID)
}

// Check consults the LRU cache first; on miss it calls POST /v1/check and
// caches both positive and negative results (consistent with 30s TTL freshness
// guarantee in design §1).
func (c *Client) Check(ctx context.Context, p CheckParams) (bool, error) {
	key := cacheKey(p)
	if v, ok := c.cache.Get(key); ok {
		return v, nil
	}
	body := map[string]any{
		"user_id":    p.UserID,
		"permission": p.Permission,
	}
	if p.ScopeType != "" {
		body["scope_type"] = p.ScopeType
		body["scope_id"] = p.ScopeID
	}
	var resp struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/check", body, &resp); err != nil {
		return false, err
	}
	c.cache.Add(key, resp.Allowed)
	return resp.Allowed, nil
}

func (c *Client) CheckBatch(ctx context.Context, ps []CheckParams) ([]bool, error) {
	out := make([]bool, len(ps))
	misses := make([]int, 0, len(ps))
	checks := make([]map[string]any, 0, len(ps))
	for i, p := range ps {
		if v, ok := c.cache.Get(cacheKey(p)); ok {
			out[i] = v
			continue
		}
		misses = append(misses, i)
		entry := map[string]any{"user_id": p.UserID, "permission": p.Permission}
		if p.ScopeType != "" {
			entry["scope_type"] = p.ScopeType
			entry["scope_id"] = p.ScopeID
		}
		checks = append(checks, entry)
	}
	if len(misses) == 0 {
		return out, nil
	}
	var resp []struct {
		Allowed bool `json:"allowed"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/check:batch", map[string]any{"checks": checks}, &resp); err != nil {
		return nil, err
	}
	if len(resp) != len(misses) {
		return nil, fmt.Errorf("ssoclient: check:batch returned %d results for %d misses", len(resp), len(misses))
	}
	for j, idx := range misses {
		out[idx] = resp[j].Allowed
		c.cache.Add(cacheKey(ps[idx]), resp[j].Allowed)
	}
	return out, nil
}

func (c *Client) RegisterPermissions(ctx context.Context, perms []PermissionSpec) error {
	body := map[string]any{
		"service":     c.cfg.ClientID,
		"permissions": perms,
	}
	return c.doJSON(ctx, http.MethodPost, "/v1/permissions:register", body, nil)
}

func (c *Client) GrantScopedRole(ctx context.Context, g GrantParams) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/grants", grantBody(g), nil)
}

func (c *Client) RevokeScopedRole(ctx context.Context, g GrantParams) error {
	return c.doJSON(ctx, http.MethodDelete, "/v1/grants", grantBody(g), nil)
}

func grantBody(g GrantParams) map[string]any {
	body := map[string]any{
		"user_id":    g.UserID,
		"scope_type": g.ScopeType,
		"scope_id":   g.ScopeID,
	}
	// Role can arrive as the role name or as the int role_id encoded as a
	// string (allowed by the interface contract).
	if id, err := strconv.Atoi(g.Role); err == nil {
		body["role_id"] = id
	} else {
		body["role"] = g.Role
	}
	return body
}

// envelope mirrors dto.GenericResponse on the SSO side: {code, message, data}.
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	tok, err := c.getServiceToken(ctx)
	if err != nil {
		return fmt.Errorf("ssoclient: acquire service token: %w", err)
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var env envelope
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &env)
	}
	if resp.StatusCode >= 400 {
		msg := env.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("ssoclient: %s %s -> %d: %s", method, path, resp.StatusCode, msg)
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// getServiceToken returns a cached service token, refreshing it via the SSO
// /token endpoint when missing or within `serviceTokenSkew` of expiry. Callers
// always go through this so the background refresher is purely optional.
func (c *Client) getServiceToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	tok := c.serviceToken
	exp := c.tokenExp
	c.mu.Unlock()
	if tok != "" && time.Until(exp) > serviceTokenSkew {
		return tok, nil
	}
	return c.refreshServiceToken(ctx)
}

func (c *Client) refreshServiceToken(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ssoclient: /token returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("ssoclient: /token returned empty access_token")
	}
	exp := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if tr.ExpiresIn == 0 {
		// Fall back to decoding the JWT exp claim if the OP didn't send expires_in.
		if t, _, err := new(jwt.Parser).ParseUnverified(tr.AccessToken, jwt.MapClaims{}); err == nil {
			if claims, ok := t.Claims.(jwt.MapClaims); ok {
				if v, ok := claims["exp"].(float64); ok {
					exp = time.Unix(int64(v), 0)
				}
			}
		}
	}

	c.mu.Lock()
	c.serviceToken = tr.AccessToken
	c.tokenExp = exp
	c.mu.Unlock()
	return tr.AccessToken, nil
}

func (c *Client) startTokenRefresher(ctx context.Context) {
	go func() {
		for {
			c.mu.Lock()
			exp := c.tokenExp
			c.mu.Unlock()
			wait := time.Until(exp) - serviceTokenSkew
			if wait < time.Second {
				wait = time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			refreshCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if _, err := c.refreshServiceToken(refreshCtx); err != nil {
				logrus.WithError(err).Warn("ssoclient: service-token refresh failed")
			}
			cancel()
		}
	}()
}
