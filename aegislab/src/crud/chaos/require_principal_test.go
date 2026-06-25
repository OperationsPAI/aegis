package chaos

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"aegis/platform/consts"

	"github.com/gin-gonic/gin"
)

// principalEngine wires requireChaosPrincipal behind a stub that populates the
// auth context the way the real upstream chain would, selected by a test header.
func principalEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		switch c.GetHeader("X-Test-Principal") {
		case "service":
			c.Set(consts.CtxKeyIsServiceToken, true)
		case "admin":
			c.Set(consts.CtxKeyIsAdmin, true)
		case "user":
			// ordinary/default user: authenticated, but neither service nor admin
			c.Set(consts.CtxKeyUserID, 42)
			c.Set(consts.CtxKeyIsServiceToken, false)
			c.Set(consts.CtxKeyIsAdmin, false)
		}
		c.Next()
	})
	r.Use(requireChaosPrincipal())
	r.GET("/v1beta/injections/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

func TestRequireChaosPrincipal(t *testing.T) {
	cases := []struct {
		name      string
		principal string
		want      int
	}{
		{"service account allowed", "service", http.StatusOK},
		{"admin allowed", "admin", http.StatusOK},
		{"ordinary user forbidden", "user", http.StatusForbidden},
		{"anonymous forbidden", "", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := principalEngine()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1beta/injections/x", nil)
			if tc.principal != "" {
				req.Header.Set("X-Test-Principal", tc.principal)
			}
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("%s: expected %d, got %d: %s", tc.name, tc.want, w.Code, w.Body.String())
			}
		})
	}
}
