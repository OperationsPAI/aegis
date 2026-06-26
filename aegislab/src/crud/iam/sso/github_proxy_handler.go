package sso

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/framework"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

const (
	githubProviderName = "github"
	githubAPIBaseURL   = "https://api.github.com"
)

var githubHTTPClient = &http.Client{Timeout: 30 * time.Second}

// GitHubProxyHandler forwards a small allow-list of read-only GitHub REST API
// calls using the access token captured during the user's federated GitHub
// login. It lets the frontend browse private repositories the user can already
// see, without ever exposing the token to the browser.
type GitHubProxyHandler struct {
	repo *FederationRepository
}

func NewGitHubProxyHandler(repo *FederationRepository) *GitHubProxyHandler {
	return &GitHubProxyHandler{repo: repo}
}

// ProxyTrees forwards GET /repos/:owner/:repo/git/trees/:sha to GitHub's Trees
// API (the frontend appends ?recursive=1 for full file listings).
func (h *GitHubProxyHandler) ProxyTrees(c *gin.Context) {
	path := "/repos/" + c.Param("owner") + "/" + c.Param("repo") + "/git/trees/" + c.Param("sha")
	h.proxy(c, path)
}

// ProxyContents forwards GET /repos/:owner/:repo/contents/*path to GitHub's
// Contents API. The catch-all :path already carries its leading slash.
func (h *GitHubProxyHandler) ProxyContents(c *gin.Context) {
	path := "/repos/" + c.Param("owner") + "/" + c.Param("repo") + "/contents" + c.Param("path")
	h.proxy(c, path)
}

func (h *GitHubProxyHandler) proxy(c *gin.Context, apiPath string) {
	userID, ok := middleware.GetCurrentUserID(c)
	if !ok || userID <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "authentication required")
		return
	}

	identity, err := h.repo.FindIdentityByUserAndProvider(c.Request.Context(), userID, githubProviderName)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, "no GitHub identity linked to this account")
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to load GitHub identity")
		return
	}

	accessToken := accessTokenFromMetadata(identity.Metadata)
	if accessToken == "" {
		dto.ErrorResponse(c, http.StatusNotFound, "no GitHub access token stored for this account")
		return
	}

	// Build the upstream URL from a fixed host so the path segments can never
	// redirect the request elsewhere. url.URL escapes the decoded gin params
	// while preserving the slashes that separate the contents path.
	target := url.URL{Scheme: "https", Host: "api.github.com", Path: apiPath, RawQuery: c.Request.URL.RawQuery}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, "failed to build GitHub request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// Forward conditional-request headers so the browser's cached ETag /
	// Last-Modified can still earn a 304 from GitHub.
	if v := c.GetHeader("If-None-Match"); v != "" {
		req.Header.Set("If-None-Match", v)
	}
	if v := c.GetHeader("If-Modified-Since"); v != "" {
		req.Header.Set("If-Modified-Since", v)
	}

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadGateway, "GitHub API request failed")
		return
	}
	defer resp.Body.Close()

	for _, header := range []string{"Content-Type", "ETag", "Last-Modified", "Cache-Control"} {
		if v := resp.Header.Get(header); v != "" {
			c.Header(header, v)
		}
	}
	c.Status(resp.StatusCode)
	_, _ = io.Copy(c.Writer, resp.Body)
}

func accessTokenFromMetadata(metadata string) string {
	if metadata == "" {
		return ""
	}
	var t federatedTokenMetadata
	if err := json.Unmarshal([]byte(metadata), &t); err != nil {
		return ""
	}
	return t.AccessToken
}

// RoutesGitHubProxy mounts the authenticated GitHub proxy under /api/v2/github.
func RoutesGitHubProxy(handler *GitHubProxyHandler) framework.RouteRegistrar {
	return framework.RouteRegistrar{
		Audience: framework.AudiencePortal,
		Name:     "sso.github-proxy",
		Register: func(v2 *gin.RouterGroup) {
			g := v2.Group("/github", middleware.JWTAuth(), middleware.RequireHumanUserAuth())
			g.GET("/repos/:owner/:repo/git/trees/:sha", handler.ProxyTrees)
			g.GET("/repos/:owner/:repo/contents/*path", handler.ProxyContents)
		},
	}
}
