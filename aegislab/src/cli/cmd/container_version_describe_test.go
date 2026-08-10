package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// containerVersionDescribeServer serves:
//   - GET /api/v2/containers                        — container list (for name resolution)
//   - GET /api/v2/containers/{id}                   — container detail (for status + version list)
//   - GET /api/v2/containers/{id}/versions/{vid}    — version detail
//
// The `withHelm` switch toggles whether the version detail includes a
// helm_config object, so we can exercise both pedestal (helm_config present)
// and algorithm (helm_config nil) flows.
func containerVersionDescribeServer(t *testing.T, withHelm bool) *httptest.Server {
	t.Helper()

	listResp := map[string]any{
		"code":    200,
		"message": "success",
		"data": map[string]any{
			"items": []map[string]any{
				{"id": 42, "name": "ts-hotel", "type": "pedestal", "status": "enabled", "created_at": "2026-01-01"},
			},
			"pagination": map[string]any{"page": 1, "size": 100, "total": 1, "total_pages": 1},
		},
	}

	detailResp := map[string]any{
		"code":    200,
		"message": "success",
		"data": map[string]any{
			"id":     42,
			"name":   "ts-hotel",
			"type":   "pedestal",
			"status": "enabled",
			"versions": []map[string]any{
				{"id": 99, "name": "v1.0.0", "image_ref": "docker.io/ns/ts-hotel:v1.0.0", "usage": 0, "updated_at": "2026-01-02"},
			},
			"created_at": "2026-01-01",
			"updated_at": "2026-01-02",
		},
	}

	versionData := map[string]any{
		"id":          99,
		"name":        "v1.0.0",
		"image_ref":   "docker.io/ns/ts-hotel:v1.0.0",
		"usage":       0,
		"updated_at":  "2026-01-02",
		"github_link": "https://github.com/example/ts-hotel",
		"command":     "",
		"env_vars":    `[{"key":"LOG_LEVEL","type":"fixed","required":true,"default_value":"info"}]`,
	}
	if withHelm {
		versionData["helm_config"] = map[string]any{
			"id":         7,
			"version":    "0.5.3",
			"chart_name": "ts-hotel",
			"repo_name":  "ts-charts",
			"repo_url":   "https://charts.example.com",
			"values":     map[string]any{"replicaCount": 3, "image.tag": "v1.0.0"},
		}
	}
	versionResp := map[string]any{"code": 200, "message": "success", "data": versionData}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/containers":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(listResp)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/containers/42":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(detailResp)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/containers/42/versions/99":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(versionResp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestContainerVersionDescribeTextWithHelm(t *testing.T) {
	ts := containerVersionDescribeServer(t, true)
	defer ts.Close()

	res := runCLI(t, "container", "version", "describe", "ts-hotel", "v1.0.0",
		"--server", ts.URL, "--token", "tok")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	want := []string{
		"Name:        ts-hotel",
		"Version:     v1.0.0",
		"Image Ref:   docker.io/ns/ts-hotel:v1.0.0",
		"GitHub Link: https://github.com/example/ts-hotel",
		"Helm Config:",
		"Chart Name: ts-hotel",
		"Version:    0.5.3",
		"Repo Name:  ts-charts",
		"Repo URL:   https://charts.example.com",
		"Values:",
		"image.tag: v1.0.0",
		"replicaCount: 3",
		"Env Vars:",
		"LOG_LEVEL",
	}
	for _, s := range want {
		if !strings.Contains(res.stdout, s) {
			t.Errorf("stdout missing %q; got:\n%s", s, res.stdout)
		}
	}
}

func TestContainerVersionDescribeJSON(t *testing.T) {
	ts := containerVersionDescribeServer(t, true)
	defer ts.Close()

	res := runCLI(t, "container", "version", "describe", "42", "99",
		"--server", ts.URL, "--token", "tok", "--format", "json")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		t.Fatalf("stdout should be JSON: %v; got %q", err, res.stdout)
	}
	if payload["name"] != "v1.0.0" {
		t.Errorf("name = %v, want v1.0.0", payload["name"])
	}
	if payload["image_ref"] != "docker.io/ns/ts-hotel:v1.0.0" {
		t.Errorf("image_ref = %v", payload["image_ref"])
	}
	hc, ok := payload["helm_config"].(map[string]any)
	if !ok {
		t.Fatalf("helm_config missing or wrong type: %v", payload["helm_config"])
	}
	if hc["chart_name"] != "ts-hotel" {
		t.Errorf("chart_name = %v", hc["chart_name"])
	}
	if hc["repo_url"] != "https://charts.example.com" {
		t.Errorf("repo_url = %v", hc["repo_url"])
	}
}

func TestContainerVersionDescribeYAML(t *testing.T) {
	ts := containerVersionDescribeServer(t, true)
	defer ts.Close()

	res := runCLI(t, "container", "version", "describe", "ts-hotel", "v1.0.0",
		"--server", ts.URL, "--token", "tok", "--format", "yaml")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	for _, s := range []string{
		"name: v1.0.0",
		"image_ref: docker.io/ns/ts-hotel:v1.0.0",
		"helm_config:",
		"chart_name: ts-hotel",
		"repo_url: https://charts.example.com",
	} {
		if !strings.Contains(res.stdout, s) {
			t.Errorf("yaml output missing %q; got:\n%s", s, res.stdout)
		}
	}
}

func TestContainerVersionDescribeNoHelm(t *testing.T) {
	ts := containerVersionDescribeServer(t, false)
	defer ts.Close()

	res := runCLI(t, "container", "version", "describe", "ts-hotel", "v1.0.0",
		"--server", ts.URL, "--token", "tok")
	if res.code != ExitCodeSuccess {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", res.code, res.stderr, res.stdout)
	}
	if !strings.Contains(res.stdout, "Helm Config:") {
		t.Errorf("missing Helm Config section; got:\n%s", res.stdout)
	}
	// Must render <none> gracefully — no broken "docker.io/:" artifact.
	if !strings.Contains(res.stdout, "<none>") {
		t.Errorf("expected <none> sentinel for missing helm_config; got:\n%s", res.stdout)
	}
	if strings.Contains(res.stdout, "Chart Name:") {
		t.Errorf("should not render Chart Name when helm_config is nil; got:\n%s", res.stdout)
	}
}

func TestContainerVersionDescribeVersionNotFound(t *testing.T) {
	ts := containerVersionDescribeServer(t, true)
	defer ts.Close()

	res := runCLI(t, "container", "version", "describe", "ts-hotel", "v9.9.9",
		"--server", ts.URL, "--token", "tok")
	if res.code != ExitCodeNotFound {
		t.Fatalf("exit = %d, want %d; stderr=%q", res.code, ExitCodeNotFound, res.stderr)
	}
}
