package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"aegis/cli/config"
)

// TestEtcdRoundTrip exercises put -> get -> list --prefix -> delete against
// a stub configcenter and asserts the wire shape aegisctl emits.
func TestEtcdRoundTrip(t *testing.T) {
	oldServer, oldToken, oldOutput, oldYes := flagServer, flagToken, flagOutput, etcdDeleteYes
	oldNS, oldPrefix, oldCfg := etcdNamespace, etcdListPrefix, cfg
	defer func() {
		flagServer, flagToken, flagOutput, etcdDeleteYes = oldServer, oldToken, oldOutput, oldYes
		etcdNamespace, etcdListPrefix, cfg = oldNS, oldPrefix, oldCfg
	}()
	cfg = &config.Config{Contexts: map[string]config.Context{}}

	type seenReq struct {
		Method string
		Path   string
		Auth   string
		Body   string
	}
	var (
		mu    sync.Mutex
		seen  []seenReq
		store = map[string]json.RawMessage{}
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, seenReq{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: string(body)})
		mu.Unlock()

		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v2/config/"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/config/"), "/")
			if len(parts) != 2 {
				http.Error(w, "bad path", http.StatusBadRequest)
				return
			}
			var req struct {
				Value  json.RawMessage `json:"value"`
				Reason string          `json:"reason"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			store[parts[0]+"/"+parts[1]] = req.Value
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodGet && strings.Count(r.URL.Path, "/") == 5:
			// /api/v2/config/<ns>/<key>
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/config/"), "/")
			val, ok := store[parts[0]+"/"+parts[1]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"namespace": parts[0], "key": parts[1], "value": val, "layer": "etcd",
			})

		case r.Method == http.MethodGet && strings.Count(r.URL.Path, "/") == 4:
			// /api/v2/config/<ns>
			ns := strings.TrimPrefix(r.URL.Path, "/api/v2/config/")
			items := []map[string]any{}
			for k, v := range store {
				kparts := strings.SplitN(k, "/", 2)
				if kparts[0] != ns {
					continue
				}
				items = append(items, map[string]any{
					"namespace": kparts[0], "key": kparts[1], "value": v, "layer": "etcd",
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v2/config/"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/config/"), "/")
			delete(store, parts[0]+"/"+parts[1])
			w.WriteHeader(http.StatusNoContent)

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	flagServer = ts.URL
	flagToken = "test-token"
	flagOutput = "json"
	etcdNamespace = ""
	etcdListPrefix = ""

	// 1) put aegis.injection.catalog_source chaos_service
	etcdPutReason = "step5b-r2-test"
	if err := etcdPutCmd.RunE(etcdPutCmd, []string{"aegis.injection.catalog_source", "chaos_service"}); err != nil {
		t.Fatalf("put: %v", err)
	}

	// 2) get
	out, err := captureStdout(t, func() error {
		return etcdGetCmd.RunE(etcdGetCmd, []string{"aegis.injection.catalog_source"})
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var got configEntry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode get output %q: %v", out, err)
	}
	if got.Namespace != "aegis" || got.Key != "injection.catalog_source" {
		t.Fatalf("unexpected entry: %+v", got)
	}
	var val string
	if err := json.Unmarshal(got.Value, &val); err != nil || val != "chaos_service" {
		t.Fatalf("unexpected value %s err=%v", string(got.Value), err)
	}

	// 3) list --prefix aegis.injection
	etcdListPrefix = "aegis.injection"
	out, err = captureStdout(t, func() error {
		return etcdListCmd.RunE(etcdListCmd, nil)
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listed []configEntry
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode list output: %v", err)
	}
	if len(listed) != 1 || listed[0].Key != "injection.catalog_source" {
		t.Fatalf("unexpected list: %+v", listed)
	}

	// 4) delete --yes
	etcdDeleteYes = true
	if err := etcdDeleteCmd.RunE(etcdDeleteCmd, []string{"aegis.injection.catalog_source"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify wire shape across all four hops.
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 4 {
		t.Fatalf("expected 4 requests, got %d: %+v", len(seen), seen)
	}
	want := []struct {
		method, path string
	}{
		{"PUT", "/api/v2/config/aegis/injection.catalog_source"},
		{"GET", "/api/v2/config/aegis/injection.catalog_source"},
		{"GET", "/api/v2/config/aegis"},
		{"DELETE", "/api/v2/config/aegis/injection.catalog_source"},
	}
	for i, w := range want {
		if seen[i].Method != w.method || seen[i].Path != w.path {
			t.Errorf("req[%d]: got %s %s, want %s %s", i, seen[i].Method, seen[i].Path, w.method, w.path)
		}
		if seen[i].Auth != "Bearer test-token" {
			t.Errorf("req[%d] auth header = %q, want Bearer test-token", i, seen[i].Auth)
		}
	}
	// PUT body must carry the JSON-encoded string and reason.
	var putBody struct {
		Value  json.RawMessage `json:"value"`
		Reason string          `json:"reason"`
	}
	if err := json.Unmarshal([]byte(seen[0].Body), &putBody); err != nil {
		t.Fatalf("put body parse: %v", err)
	}
	if string(putBody.Value) != `"chaos_service"` {
		t.Errorf("PUT value = %s, want \"chaos_service\"", string(putBody.Value))
	}
	if putBody.Reason != "step5b-r2-test" {
		t.Errorf("PUT reason = %q, want step5b-r2-test", putBody.Reason)
	}
}

func TestSplitEtcdKey(t *testing.T) {
	defer func() { etcdNamespace = "" }()
	etcdNamespace = ""
	ns, key, err := splitEtcdKey("aegis.injection.catalog_source")
	if err != nil || ns != "aegis" || key != "injection.catalog_source" {
		t.Fatalf("auto-split: ns=%q key=%q err=%v", ns, key, err)
	}
	if _, _, err := splitEtcdKey("nodots"); err == nil {
		t.Fatalf("expected error for non-dotted key when --namespace is empty")
	}
	etcdNamespace = "custom"
	ns, key, err = splitEtcdKey("flat_key")
	if err != nil || ns != "custom" || key != "flat_key" {
		t.Fatalf("override: ns=%q key=%q err=%v", ns, key, err)
	}
}
