// HTTP-level tests for the SSR render handler. These exercise the
// auth-aware redirect / 404 paths through a gin engine; the markdown
// rendering itself is covered in render_test.go.
package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newRenderRouter(t *testing.T) (*gin.Engine, *Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := newServiceForTest(t)
	h := NewRenderHandler(svc)
	r := gin.New()
	// No auth middleware: tests poke the bare handler. Detail's anonymous
	// vs. owner branches are driven by setting the uid context key.
	r.GET("/p/:slug", h.Render)
	r.GET("/p/:slug/*filepath", h.Render)
	return r, svc
}

// TestSSR_PrivateAnonymous_RedirectsToLogin: SSR for a private site
// without a logged-in user must 302 to the login page with return_to
// preserved.
func TestSSR_PrivateAnonymous_RedirectsToLogin(t *testing.T) {
	r, svc := newRenderRouter(t)
	if _, err := svc.CreateSite(context.Background(), 42, "secret", "private", "",
		[]UploadFile{mdFile("index.md", "hi")}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/p/secret", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%q", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/auth/login?return_to=") {
		t.Fatalf("expected /auth/login?return_to=..., got %q", loc)
	}
	if !strings.Contains(loc, "%2Fp%2Fsecret") {
		t.Fatalf("return_to does not preserve original path: %q", loc)
	}
}

// TestSSR_MissingSlug_404 covers the simplest miss: an unknown slug
// returns plain-text 404 regardless of visibility / auth.
func TestSSR_MissingSlug_404(t *testing.T) {
	r, _ := newRenderRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/p/no-such", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
