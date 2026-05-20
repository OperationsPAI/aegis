package chaos

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// captureLogger returns a logger writing into buf at WARN+ level so tests
// can assert the startup-WARN substring without other noise.
func captureLogger(buf *bytes.Buffer) *logrus.Logger {
	l := logrus.New()
	l.SetOutput(buf)
	l.SetLevel(logrus.WarnLevel)
	l.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true, DisableColors: true})
	return l
}

// resetOnce makes the sync.Once instances behave fresh for each subtest
// since they're package-globals. The middleware exposes no Reset, so we
// reach in via a small helper kept in test-only code.
func resetInboundOnce() {
	inboundUnsetWarnOnce = sync.Once{}
	requireBearerWarnOnce = sync.Once{}
}

func newAuthRouter(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(mw)
	r.GET("/probe", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

// fallthroughRejecter is a stand-in for TrustedHeaderAuth used in tests:
// always rejects so we can prove the static-bearer path is the only thing
// that lets a request through when the env is set.
func fallthroughRejecter(c *gin.Context) {
	c.AbortWithStatus(http.StatusUnauthorized)
}

func TestChaosAuth_EnvSet_MissingHeader_Rejects(t *testing.T) {
	resetInboundOnce()
	var buf bytes.Buffer
	mw := newChaosAuth("s3cret", true, fallthroughRejecter, captureLogger(&buf))
	r := newAuthRouter(mw)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when env set + header missing, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestChaosAuth_EnvSet_GoodHeader_Accepts(t *testing.T) {
	resetInboundOnce()
	var buf bytes.Buffer
	mw := newChaosAuth("s3cret", true, fallthroughRejecter, captureLogger(&buf))
	r := newAuthRouter(mw)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with matching bearer, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestChaosAuth_EnvSet_WrongHeader_FallsThroughAndRejects(t *testing.T) {
	resetInboundOnce()
	var buf bytes.Buffer
	mw := newChaosAuth("s3cret", true, fallthroughRejecter, captureLogger(&buf))
	r := newAuthRouter(mw)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected fallback rejection (401) on wrong bearer, got %d", w.Code)
	}
	// Wrong-bearer attempts must log a WARN (path + remote) — token must NOT appear.
	out := buf.String()
	if !strings.Contains(out, "presented bearer did not match") {
		t.Fatalf("expected fallback WARN, got: %s", out)
	}
	if strings.Contains(out, "wrong") || strings.Contains(out, "s3cret") {
		t.Fatalf("token leaked into log output: %s", out)
	}
}

func TestChaosAuth_EnvUnset_RequireBearerFalse_PassesThrough(t *testing.T) {
	resetInboundOnce()
	var buf bytes.Buffer
	passthrough := func(c *gin.Context) { c.Next() }
	mw := newChaosAuth("", false, passthrough, captureLogger(&buf))
	r := newAuthRouter(mw)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected pass-through 200 when env unset + require=false, got %d", w.Code)
	}
	if !strings.Contains(buf.String(), InboundBearerEnv+" unset") {
		t.Fatalf("expected startup WARN about unset env, got: %s", buf.String())
	}
}

func TestChaosAuth_EnvUnset_RequireBearerTrue_Rejects(t *testing.T) {
	resetInboundOnce()
	var buf bytes.Buffer
	// Fallback must NOT run when require=true + env unset; use a panicking
	// handler to prove fail-closed short-circuits before any fallback.
	panicFallback := func(c *gin.Context) { t.Fatalf("fallback unexpectedly invoked") }
	mw := newChaosAuth("", true, panicFallback, captureLogger(&buf))
	r := newAuthRouter(mw)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when env unset + require=true, got %d", w.Code)
	}
	if !strings.Contains(buf.String(), RequireBearerEnv+"=true") {
		t.Fatalf("expected ERROR log mentioning %s=true, got: %s", RequireBearerEnv, buf.String())
	}
}

func TestRequireBearerFromEnv(t *testing.T) {
	cases := map[string]bool{
		"":      true,
		"true":  true,
		"True":  true,
		"1":     true,
		"yes":   true,
		"false": false,
		"FALSE": false,
		"0":     false,
		"no":    false,
		"off":   false,
	}
	for raw, want := range cases {
		if got := requireBearerFromEnv(raw); got != want {
			t.Errorf("requireBearerFromEnv(%q) = %v, want %v", raw, got, want)
		}
	}
}
