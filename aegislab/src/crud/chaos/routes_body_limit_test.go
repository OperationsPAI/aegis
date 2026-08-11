package chaos

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLimitRequestBody_Rejects2MiBAccepts100KiB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(limitRequestBody())
	e.POST("/echo", func(c *gin.Context) { c.Status(http.StatusOK) })

	big := bytes.NewReader([]byte(strings.Repeat("x", 2<<20)))
	req := httptest.NewRequest(http.MethodPost, "/echo", big)
	req.ContentLength = int64(big.Len())
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("2MiB: want 413, got %d", rec.Code)
	}

	small := bytes.NewReader([]byte(strings.Repeat("x", 100<<10)))
	req = httptest.NewRequest(http.MethodPost, "/echo", small)
	req.ContentLength = int64(small.Len())
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("100KiB: must not be 413, got %d", rec.Code)
	}
}
