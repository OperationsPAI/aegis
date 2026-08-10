package execution

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesRegistersEverythingExactlyOnce(t *testing.T) {
	handler := &Handler{}
	reg := Routes(handler)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v2 := engine.Group("/api/v2")
	reg.Register(v2)

	type key struct {
		method string
		path   string
	}
	counts := make(map[key]int, len(engine.Routes()))
	for _, r := range engine.Routes() {
		counts[key{r.Method, r.Path}]++
	}

	expected := []key{
		{"GET", "/api/v2/projects/:project_id/executions"},
		{"POST", "/api/v2/projects/:project_id/executions/execute"},
		{"GET", "/api/v2/executions"},
		{"GET", "/api/v2/executions/labels"},
		{"GET", "/api/v2/executions/:execution_id"},
		{"PATCH", "/api/v2/executions/:execution_id/labels"},
		{"POST", "/api/v2/executions/batch-delete"},
		{"POST", "/api/v2/executions/compare"},
		{"POST", "/api/v2/executions/:execution_id/detector_results"},
		{"POST", "/api/v2/executions/:execution_id/granularity_results"},
	}
	for _, k := range expected {
		switch counts[k] {
		case 0:
			t.Errorf("missing route %s %s", k.method, k.path)
		case 1:
			delete(counts, k)
		default:
			t.Errorf("route %s %s registered %d times", k.method, k.path, counts[k])
		}
	}
	for k, n := range counts {
		t.Errorf("unexpected route %s %s registered (count=%d)", k.method, k.path, n)
	}
}
