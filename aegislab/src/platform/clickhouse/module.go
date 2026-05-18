package clickhouse

import "go.uber.org/fx"

// Module exposes the ClickHouse-backed LogReader to the dependency graph.
// It is registered alongside platform/loki was, in boot/app.go's
// ObserveOptions, so consumers (e.g. injection.Service) can fx-resolve the
// reader the same way they used to resolve *loki.Client.
var Module = fx.Module("clickhouse",
	fx.Provide(NewClickHouseLogReader),
)
