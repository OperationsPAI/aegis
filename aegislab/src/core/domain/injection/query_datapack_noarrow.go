//go:build !duckdb_arrow

package injection

import (
	"context"
	"fmt"
	"io"

	"aegis/platform/authz"
)

func (s *Service) queryDatapackFileContent(ctx context.Context, scope authz.CallerScope, id int, filePath string) (string, int64, io.ReadCloser, error) {
	_ = ctx
	_ = scope
	_ = id
	_ = filePath
	return "", 0, nil, fmt.Errorf("QueryDatapackFileContent requires building with -tags duckdb_arrow")
}

func (s *Service) getDatapackSchema(ctx context.Context, scope authz.CallerScope, id int) (*DatapackSchemaResp, error) {
	_ = ctx
	_ = scope
	_ = id
	return nil, fmt.Errorf("GetDatapackSchema requires building with -tags duckdb_arrow")
}

func (s *Service) runDatapackQuery(ctx context.Context, scope authz.CallerScope, id int, userSQL string) (io.ReadCloser, error) {
	_ = ctx
	_ = scope
	_ = id
	_ = userSQL
	return nil, fmt.Errorf("QueryDatapack requires building with -tags duckdb_arrow")
}
