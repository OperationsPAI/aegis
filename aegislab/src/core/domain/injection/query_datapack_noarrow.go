//go:build !duckdb_arrow

package injection

import (
	"context"
	"fmt"
	"io"
)

func (s *Service) queryDatapackFileContent(ctx context.Context, id int, filePath string) (string, int64, io.ReadCloser, error) {
	_ = ctx
	_ = id
	_ = filePath
	return "", 0, nil, fmt.Errorf("QueryDatapackFileContent requires building with -tags duckdb_arrow")
}

func (s *Service) getDatapackSchema(ctx context.Context, id int) (*DatapackSchemaResp, error) {
	_ = ctx
	_ = id
	return nil, fmt.Errorf("GetDatapackSchema requires building with -tags duckdb_arrow")
}

func (s *Service) runDatapackQuery(ctx context.Context, id int, userSQL string) (io.ReadCloser, error) {
	_ = ctx
	_ = id
	_ = userSQL
	return nil, fmt.Errorf("QueryDatapack requires building with -tags duckdb_arrow")
}
