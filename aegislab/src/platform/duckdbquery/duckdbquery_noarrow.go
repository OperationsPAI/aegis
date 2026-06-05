//go:build !duckdb_arrow

package duckdbquery

import (
	"context"
	"fmt"
	"io"
)

const errRequiresTag = "duckdbquery requires building with -tags duckdb_arrow"

func query(_ context.Context, _ []Source, _ string, _ Limits) (io.ReadCloser, error) {
	return nil, fmt.Errorf("%s", errRequiresTag)
}

func schema(_ context.Context, _ []Source, _ Limits) ([]Table, error) {
	return nil, fmt.Errorf("%s", errRequiresTag)
}

func queryRows(_ context.Context, _ []Source, _ string, _ Limits) (int64, []map[string]any, error) {
	return 0, nil, fmt.Errorf("%s", errRequiresTag)
}
