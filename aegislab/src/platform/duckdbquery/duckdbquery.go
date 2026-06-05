// Package duckdbquery is a reusable DuckDB-over-presigned-URL query
// engine. It registers one VIEW per Source (a parquet file reachable by
// presigned HTTPS URL or a local/s3 path), validates read-only user SQL,
// and streams the result as an Arrow IPC stream. Callers mint the URLs;
// the engine never holds S3 credentials and sets no S3 secret, so only
// the URLs the caller provides can resolve.
//
// The arrow-backed implementation lives in duckdbquery_arrow.go behind
// the duckdb_arrow build tag; a stub in duckdbquery_noarrow.go returns a
// clear error when that tag is absent.
package duckdbquery

import (
	"context"
	"io"
)

// Source binds a logical VIEW name to the parquet file it reads. URL is
// passed verbatim to read_parquet() — a presigned https URL for
// S3-backed stores, or an s3:// / filesystem path otherwise.
type Source struct {
	View string
	URL  string
}

// Column is one column of a queried table.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Table is the schema of one registered Source: its VIEW name, row
// count (-1 when unknown), and column metadata.
type Table struct {
	Table    string   `json:"table"`
	RowCount int64    `json:"row_count"`
	Columns  []Column `json:"columns"`
}

// Limits fences a single query session. Zero fields fall back to
// package defaults (see defaultLimits).
type Limits struct {
	// MemoryLimit is the DuckDB memory_limit, e.g. "2GB". Empty → default.
	MemoryLimit string
	// Threads caps DuckDB worker threads. <=0 → default.
	Threads int
	// AllowedDirectories fences read_parquet() against the local
	// filesystem. Applied (with lock_configuration) only when every
	// Source URL is a local/filesystem path; presigned-URL sessions need
	// external access and skip the local fence (the no-S3-secret posture
	// plus the SQL denylist are the backstop there).
	AllowedDirectories []string
}

// defaultLimits is the resource fence applied when a caller leaves a
// field zero. Small thread count keeps one query from pegging a shared
// engine; the statement timeout is enforced via the request context
// deadline by callers.
var defaultLimits = Limits{
	MemoryLimit: "2GB",
	Threads:     2,
}

func (l Limits) memoryLimit() string {
	if l.MemoryLimit == "" {
		return defaultLimits.MemoryLimit
	}
	return l.MemoryLimit
}

func (l Limits) threads() int {
	if l.Threads <= 0 {
		return defaultLimits.Threads
	}
	return l.Threads
}

// Query validates userSQL (read-only SELECT/WITH only), registers one
// VIEW per Source, applies the resource + local-read fence, runs the
// query, and returns an Arrow IPC stream. The caller owns the returned
// ReadCloser and must Close it. A statement timeout is enforced via
// ctx's deadline.
func Query(ctx context.Context, sources []Source, userSQL string, lim Limits) (io.ReadCloser, error) {
	return query(ctx, sources, userSQL, lim)
}

// Schema registers one VIEW per Source and returns each table's column
// metadata + row count. A failing Source is skipped, not fatal.
func Schema(ctx context.Context, sources []Source, lim Limits) ([]Table, error) {
	return schema(ctx, sources, lim)
}

// QueryRows runs userSQL and decodes the result into JSON-friendly row
// maps server-side. Used by the Accept: application/json response path.
func QueryRows(ctx context.Context, sources []Source, userSQL string, lim Limits) (int64, []map[string]any, error) {
	return queryRows(ctx, sources, userSQL, lim)
}

// ValidateSQL exposes the read-only allowlist so callers can reject a
// query before opening an engine. Returns the normalized statement.
func ValidateSQL(raw string) (string, error) {
	return validateSQL(raw)
}

// SanitizeViewName turns a parquet filestem into a safe VIEW identifier.
// Exposed so producers derive the same VIEW names the engine registers.
func SanitizeViewName(raw string) string {
	return sanitizeViewName(raw)
}
