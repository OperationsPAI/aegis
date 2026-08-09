package configcenterclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	configcenterclient "aegis/clients/configcenter"
	"aegis/crud/admin/configcenter"
	"aegis/platform/consts"

	"github.com/spf13/viper"
)

// fakeTokenSource is a no-op TokenSource — the fake server doesn't enforce auth.
type fakeTokenSource struct{}

func (fakeTokenSource) Token(context.Context) (string, error) { return "test-token", nil }

func TestBootstrapDynamicViper_SeedsAndWatches(t *testing.T) {
	const (
		seedKey   = "injection.system.otel-demo.executor_authoritative"
		seedValue = "chaos-service"
		viperKey  = "aegis.injection.system.otel-demo.executor_authoritative"

		updateKey   = "injection.catalog_source"
		updateValue = "chaos_service"
		updateViper = "aegis.injection.catalog_source"
	)

	t.Cleanup(func() { viper.Reset() })

	// Channel the test uses to push a watch event after assertion #1.
	eventCh := make(chan configcenter.Entry, 1)

	mux := http.NewServeMux()
	listPath := consts.APIPathConfigPrefix + configcenterclient.DynamicViperNamespace
	mux.HandleFunc(listPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		out := struct {
			Items []configcenter.Entry `json:"items"`
		}{
			Items: []configcenter.Entry{
				{
					Namespace: configcenterclient.DynamicViperNamespace,
					Key:       seedKey,
					Value:     seedValue,
					Layer:     configcenter.LayerEtcd,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	watchPath := consts.APIPathConfigPrefix + configcenterclient.DynamicViperNamespace + "/watch"
	mux.HandleFunc(watchPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		flusher.Flush()
		for {
			select {
			case e := <-eventCh:
				b, _ := json.Marshal(e)
				_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := configcenterclient.NewRemoteClient(
		configcenterclient.RemoteClientConfig{BaseURL: srv.URL, Timeout: 2 * time.Second},
		fakeTokenSource{},
	)
	if err != nil {
		t.Fatalf("NewRemoteClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stop, err := configcenterclient.BootstrapDynamicViper(ctx, client, configcenterclient.DynamicViperNamespace)
	if err != nil {
		t.Fatalf("BootstrapDynamicViper: %v", err)
	}
	t.Cleanup(stop)

	if got := viper.GetString(viperKey); got != seedValue {
		t.Fatalf("after seed: viper[%s]=%q, want %q", viperKey, got, seedValue)
	}

	// Push a watch event and assert it lands in viper. The SSE goroutine is
	// async, so poll briefly.
	eventCh <- configcenter.Entry{
		Namespace: configcenterclient.DynamicViperNamespace,
		Key:       updateKey,
		Value:     updateValue,
		Layer:     configcenter.LayerEtcd,
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(viper.GetString(updateViper)) == updateValue {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("after watch event: viper[%s]=%q, want %q", updateViper, viper.GetString(updateViper), updateValue)
}
