package consumer

import (
	"testing"

	chdriver "github.com/ClickHouse/clickhouse-go/v2"

	db "aegis/platform/db"
)

// TestFreshnessProbeOptions_UsesHTTPProtocol pins the regression fix for
// issue #226: the freshness probe MUST speak HTTP, because the etcd
// `database.clickhouse.port` config key holds the HTTP listener port
// (8123) — not the native binary protocol port (9000). Speaking native
// bytes at 8123 yields `unexpected packet [72] from server` and breaks
// every BuildDatapack run.
//
// This test is a static config-shape assertion; it does not open a
// network connection.
func TestFreshnessProbeOptions_UsesHTTPProtocol(t *testing.T) {
	cfg := &db.DatabaseConfig{
		Host:     "clickhouse.observability.svc",
		Port:     8123, // HTTP listener — what etcd is actually configured with
		Database: "otel",
		User:     "default",
		Password: "secret",
	}

	opts := freshnessProbeOptions(cfg)

	if opts.Protocol != chdriver.HTTP {
		t.Fatalf("freshnessProbeOptions Protocol = %v, want chdriver.HTTP (regression #226: native protocol at port 8123 yields packet [72] error)", opts.Protocol)
	}
	if got, want := len(opts.Addr), 1; got != want {
		t.Fatalf("Addr length = %d, want %d", got, want)
	}
	if got, want := opts.Addr[0], "clickhouse.observability.svc:8123"; got != want {
		t.Fatalf("Addr[0] = %q, want %q (port must come from db.DatabaseConfig.Port, not a hardcoded 9000)", got, want)
	}
	if got, want := opts.Auth.Database, "otel"; got != want {
		t.Fatalf("Auth.Database = %q, want %q", got, want)
	}
	if got, want := opts.Auth.Username, "default"; got != want {
		t.Fatalf("Auth.Username = %q, want %q", got, want)
	}
	if got, want := opts.Auth.Password, "secret"; got != want {
		t.Fatalf("Auth.Password = %q, want %q", got, want)
	}
}

// TestFreshnessProbeOptions_DefaultsDatabaseToOtel covers the back-compat
// behaviour preserved from the original implementation: when the config
// block omits `database.clickhouse.db`, the probe falls back to "otel"
// (the database name where otel-collector writes otel_traces).
func TestFreshnessProbeOptions_DefaultsDatabaseToOtel(t *testing.T) {
	cfg := &db.DatabaseConfig{
		Host: "ch",
		Port: 8123,
	}

	opts := freshnessProbeOptions(cfg)

	if got, want := opts.Auth.Database, "otel"; got != want {
		t.Fatalf("Auth.Database = %q, want %q (default fallback when cfg.Database is empty)", got, want)
	}
}

// TestFreshnessProbeOptions_HonoursCustomPort proves the helper does NOT
// hardcode 8123 — it just reads whatever the etcd config says. If a future
// deploy moves the HTTP listener to a non-standard port, the probe still
// works.
func TestFreshnessProbeOptions_HonoursCustomPort(t *testing.T) {
	cfg := &db.DatabaseConfig{
		Host: "ch",
		Port: 18123, // hypothetical alternate HTTP listener
	}

	opts := freshnessProbeOptions(cfg)

	if got, want := opts.Addr[0], "ch:18123"; got != want {
		t.Fatalf("Addr[0] = %q, want %q", got, want)
	}
	if opts.Protocol != chdriver.HTTP {
		t.Fatalf("Protocol must remain HTTP regardless of port; got %v", opts.Protocol)
	}
}
