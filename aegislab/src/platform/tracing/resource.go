package tracing

import (
	"aegis/platform/config"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

// rcabenchResource builds the shared *resource.Resource that every OTel
// signal aegislab emits (traces + logs) stamps onto its payloads. Keeping
// both signals on byte-identical attributes is what lets the ClickHouse
// queries in crud/observability/trace and platform/clickhouse filter on a
// single `ServiceName = 'rcabench'` predicate.
func rcabenchResource() (*resource.Resource, error) {
	// Pass the empty SchemaURL so Merge() doesn't reject our overrides
	// when resource.Default() ships a newer schema URL than the version
	// of semconv we import.
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"",
			semconv.ServiceName(config.GetString("name")),
			semconv.ServiceVersion(config.GetString("version")),
		),
	)
}
