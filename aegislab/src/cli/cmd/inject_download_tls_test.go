package cmd

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegis/cli/client"
)

func newDatapackTLSServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/files/download") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDownloadInjectionFile_DefaultVerifiesAndFails(t *testing.T) {
	srv := newDatapackTLSServer(t, "data")
	httpClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: client.TransportFor(client.TLSOptions{}),
	}
	out := filepath.Join(t.TempDir(), "f.parquet")
	err := downloadInjectionFile(httpClient, srv.URL, "tok", 7, "abnormal_metrics.parquet", out)
	if err == nil {
		t.Fatal("expected TLS verification failure against self-signed server, got nil")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected certificate error, got: %v", err)
	}
}

func TestDownloadInjectionFile_InsecureSucceeds(t *testing.T) {
	srv := newDatapackTLSServer(t, "parquet-bytes")
	httpClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: client.TransportFor(client.TLSOptions{Insecure: true}),
	}
	out := filepath.Join(t.TempDir(), "f.parquet")
	if err := downloadInjectionFile(httpClient, srv.URL, "tok", 7, "abnormal_metrics.parquet", out); err != nil {
		t.Fatalf("insecure download should succeed: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "parquet-bytes" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestDownloadInjectionFile_CustomCASucceeds(t *testing.T) {
	srv := newDatapackTLSServer(t, "parquet-bytes")

	caPath := filepath.Join(t.TempDir(), "ca.crt")
	cert := srv.Certificate()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	// Sanity: the cert is genuinely untrusted by the system pool.
	if _, err := cert.Verify(x509.VerifyOptions{}); err == nil {
		t.Skip("httptest cert unexpectedly chains to a system root")
	}

	httpClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: client.TransportFor(client.TLSOptions{CACert: caPath}),
	}
	out := filepath.Join(t.TempDir(), "f.parquet")
	if err := downloadInjectionFile(httpClient, srv.URL, "tok", 7, "abnormal_metrics.parquet", out); err != nil {
		t.Fatalf("custom-CA download should succeed: %v", err)
	}
}
