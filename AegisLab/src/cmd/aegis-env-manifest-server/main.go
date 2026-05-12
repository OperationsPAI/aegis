// aegis-env-manifest-server is a tiny standalone process that serves only
// the .well-known/aegis-environments.json manifest endpoint. It exists so
// the aegis-ui environment-discovery feature can be verified end-to-end
// against two independent "backends" without spinning up the full
// AegisLab stack twice (mysql, redis, etcd, k8s, ...).
//
// The handler logic mirrors platform/router/well_known.go so production
// behavior matches verification behavior.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
)

type EnvironmentDescriptor struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	BaseURL string `json:"baseUrl"`
	Badge   string `json:"badge,omitempty"`
}

type EnvironmentManifest struct {
	Default      string                  `json:"default"`
	Environments []EnvironmentDescriptor `json:"environments"`
}

func main() {
	addr := flag.String("addr", ":18080", "listen address (host:port)")
	flag.Parse()

	raw := os.Getenv("AEGIS_ENVIRONMENTS_JSON")
	if raw == "" {
		log.Fatal("AEGIS_ENVIRONMENTS_JSON must be set to a JSON manifest")
	}
	var manifest EnvironmentManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		log.Fatalf("invalid AEGIS_ENVIRONMENTS_JSON: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/aegis-environments.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(manifest)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("aegis-env-manifest-server listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
