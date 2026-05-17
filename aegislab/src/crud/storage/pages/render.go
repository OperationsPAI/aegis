package pages

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// RenderHandler is the public SSR surface. Distinct type so the route
// registrar can keep concerns separate (no auth middleware leaks into the
// management handler's audit calls).
type RenderHandler struct {
	svc *Service
}

func NewRenderHandler(svc *Service) *RenderHandler { return &RenderHandler{svc: svc} }

// Render serves /p/:slug and /p/:slug/*filepath.
//
// Behaviour:
//   - 404 when slug not registered
//   - private + no user      → 302 /auth/login?return_to=<full>
//   - private + non-owner    → 404 (do not reveal existence)
//   - filepath cleaning: percent-decode, reject "..", default to "index.md"
//   - .md → render to HTML; other → stream raw with mime from extension
func (h *RenderHandler) Render(c *gin.Context) {
	slug := c.Param("slug")
	site, err := h.svc.FindBySlug(c.Request.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(c)
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}

	if site.Visibility == VisibilityPrivate {
		uid, ok := middleware.GetCurrentUserID(c)
		if !ok || uid <= 0 {
			c.Redirect(http.StatusFound, "/auth/login?return_to="+url.QueryEscape(c.Request.URL.RequestURI()))
			return
		}
		if uid != site.OwnerID {
			h.notFound(c)
			return
		}
	}

	raw := c.Param("filepath")
	cleaned, err := cleanRenderPath(raw)
	if err != nil {
		h.notFound(c)
		return
	}

	if strings.HasSuffix(strings.ToLower(cleaned), ".md") {
		h.serveMarkdown(c, site, cleaned)
		return
	}
	h.serveRaw(c, site, cleaned)
}

func (h *RenderHandler) serveMarkdown(c *gin.Context, site *PageSite, cleanedPath string) {
	body, _, err := h.svc.FetchBytes(c.Request.Context(), site, cleanedPath)
	if err != nil {
		h.notFound(c)
		return
	}
	mdPaths, err := h.svc.ListMarkdownFiles(c.Request.Context(), site)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := RenderMarkdown(RenderInput{
		Slug:          site.Slug,
		SiteTitle:     site.Title,
		CurrentPath:   cleanedPath,
		MarkdownPaths: mdPaths,
		Source:        body,
	})
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", out)
}

func (h *RenderHandler) serveRaw(c *gin.Context, site *PageSite, cleanedPath string) {
	rc, meta, err := h.svc.FetchReader(c.Request.Context(), site, cleanedPath)
	if err != nil {
		h.notFound(c)
		return
	}
	defer func() { _ = rc.Close() }()
	// Prefer extension-derived MIME so historical uploads that were stored
	// with a generic `application/octet-stream` still render correctly —
	// browsers refuse `<img src=".../foo.svg">` served as octet-stream
	// even when the bytes are a valid SVG.
	ct := mime.TypeByExtension(filepath.Ext(cleanedPath))
	if ct == "" {
		ct = meta.ContentType
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Header("Content-Type", ct)
	_, _ = io.Copy(c.Writer, rc)
}

func (h *RenderHandler) notFound(c *gin.Context) {
	c.Data(http.StatusNotFound, "text/plain; charset=utf-8", []byte("404 page not found"))
}

// cleanRenderPath percent-decodes, strips leading "/", rejects "..",
// and substitutes "index.md" for empty / trailing-slash paths.
func cleanRenderPath(raw string) (string, error) {
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	decoded = strings.TrimPrefix(decoded, "/")
	if decoded == "" || strings.HasSuffix(decoded, "/") {
		decoded += "index.md"
	}
	cleaned := path.Clean(decoded)
	if cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, "/") ||
		strings.HasPrefix(cleaned, "../") ||
		strings.Contains(cleaned, "/../") ||
		strings.HasSuffix(cleaned, "/..") {
		return "", errors.New("invalid path")
	}
	return cleaned, nil
}

// ServeStaticAssets handles /static/pages/* by serving the embedded asset tree.
func ServeStaticAssets(c *gin.Context) {
	rel := strings.TrimPrefix(c.Param("filepath"), "/")
	if rel == "" {
		c.Status(http.StatusNotFound)
		return
	}
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := fs.ReadFile(sub, rel)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(rel))
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Data(http.StatusOK, ct, data)
}
