package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/platform/consts"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func setupScopeRouter(t *testing.T, principal func(c *gin.Context), required string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if principal != nil {
		r.Use(principal)
	}
	r.GET("/probe", RequireScope(required), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func setServicePrincipal(scopes []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(consts.CtxKeyIsServiceToken, true)
		c.Set(consts.CtxKeyTokenType, "service")
		c.Set(consts.CtxKeyTaskID, "task-42")
		c.Set(consts.CtxKeyScopes, scopes)
		c.Next()
	}
}

func TestRequireScope_NoAuth_Returns401(t *testing.T) {
	r := setupScopeRouter(t, nil, "chaos.webhook.write")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}
}

func TestRequireScope_ScopePresent_Pass(t *testing.T) {
	r := setupScopeRouter(t, setServicePrincipal([]string{"chaos.webhook.write"}), "chaos.webhook.write")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with scope present, got %d", w.Code)
	}
}

func TestRequireScope_EmptyScopes_WarnAndPass(t *testing.T) {
	var buf bytes.Buffer
	old := logrus.StandardLogger().Out
	oldLevel := logrus.StandardLogger().Level
	logrus.StandardLogger().SetOutput(&buf)
	logrus.StandardLogger().SetLevel(logrus.WarnLevel)
	defer func() {
		logrus.StandardLogger().SetOutput(old)
		logrus.StandardLogger().SetLevel(oldLevel)
	}()

	r := setupScopeRouter(t, setServicePrincipal(nil), "chaos.webhook.write")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (transient accept) with empty scopes, got %d", w.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "principal has no scopes") {
		t.Fatalf("expected WARN log about missing scopes, got: %s", out)
	}
	if !strings.Contains(out, "service:task-42") {
		t.Fatalf("expected principal ident in log, got: %s", out)
	}
}

func TestRequireScope_NonEmptyButMissing_Returns403(t *testing.T) {
	r := setupScopeRouter(t, setServicePrincipal([]string{"other.scope"}), "chaos.webhook.write")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with non-matching scope set, got %d", w.Code)
	}
}
