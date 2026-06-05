//go:build duckdb_arrow

package blob

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/platform/duckdbquery"

	"github.com/gin-gonic/gin"
)

// queryStatementTimeout bounds one query so a bad scan cannot peg the
// shared engine. Enforced via the request context deadline.
const queryStatementTimeout = 2 * time.Minute

// queryBucketLimits is the per-session resource fence. Sources are
// presigned remote URLs, so allowed_directories is not set; the
// no-S3-secret posture + SQL denylist confine remote reads.
var queryBucketLimits = duckdbquery.Limits{
	MemoryLimit: "2GB",
	Threads:     2,
}

// queryReq is the body of POST /buckets/{bucket}/query.
type queryReq struct {
	Prefix string   `json:"prefix,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	SQL    string   `json:"sql" binding:"required"`
}

// queryRowsResp is the Accept: application/json response body.
type queryRowsResp struct {
	RowCount int64            `json:"row_count"`
	Rows     []map[string]any `json:"rows"`
}

// schemaTable mirrors duckdbquery.Table for the schema response.
type schemaTable struct {
	Table    string               `json:"table"`
	RowCount int64                `json:"row_count"`
	Columns  []duckdbquery.Column `json:"columns"`
}

type schemaResp struct {
	Tables []schemaTable `json:"tables"`
}

// QueryBucket runs a read-only SQL query over the parquet objects named
// by {prefix | keys} and streams the result as Arrow IPC (default) or
// JSON rows (Accept: application/json). One VIEW is registered per
// *.parquet object; view name = sanitizeViewName(filestem).
//
//	@Summary		Query bucket parquet objects (SQL)
//	@Description	Run a read-only SELECT/WITH query over the parquet objects under a prefix (or an explicit key list). Default response is an Arrow IPC stream; send Accept: application/json for decoded rows. One VIEW per *.parquet; view name = sanitized filestem. p50/p90/p95/p99 percentile macros are pre-registered.
//	@Tags			Blob
//	@ID				blob_query_bucket
//	@Accept			json
//	@Produce		application/vnd.apache.arrow.stream
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string						true	"Bucket name"
//	@Param			request	body		queryReq					true	"Query request"
//	@Success		200		{object}	queryRowsResp				"Decoded rows (Accept: application/json) or Arrow IPC stream"
//	@Failure		400		{object}	dto.GenericResponse[any]	"Invalid SQL or selector"
//	@Failure		401		{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]	"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]	"Bucket not found or no parquet objects"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/query [post]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) QueryBucket(c *gin.Context) {
	bucket := c.Param("bucket")
	var req queryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	if !h.authorizeBucketRead(c, bucket) {
		return
	}
	// Reject bad SQL before resolving sources / minting presigns.
	if _, err := duckdbquery.ValidateSQL(req.SQL); err != nil {
		h.writeQueryError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), queryStatementTimeout)
	defer cancel()

	sources, err := h.svc.resolveQuerySources(ctx, bucket, req.Prefix, req.Keys)
	if err != nil {
		h.writeQueryError(c, err)
		return
	}

	if wantsJSON(c) {
		count, rows, qErr := duckdbquery.QueryRows(ctx, sources, req.SQL, queryBucketLimits)
		if qErr != nil {
			h.writeQueryError(c, qErr)
			return
		}
		dto.SuccessResponse(c, queryRowsResp{RowCount: count, Rows: rows})
		return
	}

	reader, err := duckdbquery.Query(ctx, sources, req.SQL, queryBucketLimits)
	if err != nil {
		h.writeQueryError(c, err)
		return
	}
	defer func() { _ = reader.Close() }()
	c.Header("Content-Type", "application/vnd.apache.arrow.stream")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, reader)
}

// SchemaBucket lists the logical tables (one VIEW per *.parquet under
// the selector) with their columns and row counts.
//
//	@Summary		Bucket parquet schema
//	@Description	List the logical tables exposed over the parquet objects under a prefix (or an explicit key list): one VIEW per *.parquet with its columns and row count. Used to drive SQL against POST /query.
//	@Tags			Blob
//	@ID				blob_schema_bucket
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string						true	"Bucket name"
//	@Param			prefix	query		string						false	"Key prefix selector"
//	@Param			keys	query		[]string					false	"Explicit object keys selector"
//	@Success		200		{object}	schemaResp					"Tables listed"
//	@Failure		400		{object}	dto.GenericResponse[any]	"Invalid selector"
//	@Failure		401		{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]	"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]	"Bucket not found or no parquet objects"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/schema [get]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) SchemaBucket(c *gin.Context) {
	bucket := c.Param("bucket")
	if !h.authorizeBucketRead(c, bucket) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), queryStatementTimeout)
	defer cancel()

	sources, err := h.svc.resolveQuerySources(ctx, bucket, c.Query("prefix"), c.QueryArray("keys"))
	if err != nil {
		h.writeQueryError(c, err)
		return
	}
	libTables, err := duckdbquery.Schema(ctx, sources, queryBucketLimits)
	if err != nil {
		h.writeQueryError(c, err)
		return
	}
	tables := make([]schemaTable, 0, len(libTables))
	for _, t := range libTables {
		tables = append(tables, schemaTable{Table: t.Table, RowCount: t.RowCount, Columns: t.Columns})
	}
	dto.SuccessResponse(c, schemaResp{Tables: tables})
}

// authorizeBucketRead enforces the same bucket-level read ACL the other
// blob read endpoints use. Writes the error response and returns false
// on denial / missing bucket.
func (h *Handler) authorizeBucketRead(c *gin.Context, bucket string) bool {
	b, err := h.svc.Registry().Lookup(bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return false
	}
	if !h.auth.CanRead(&b.Config, subjectFromContext(c), nil) {
		dto.ErrorResponse(c, http.StatusForbidden, ErrUnauthorized.Error())
		return false
	}
	return true
}

func wantsJSON(c *gin.Context) bool {
	return strings.Contains(strings.ToLower(c.GetHeader("Accept")), "application/json")
}

// writeQueryError maps the lib's consts.Err* + blob errors to HTTP
// status codes in the aegis GenericResponse shape.
func (h *Handler) writeQueryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, consts.ErrBadRequest):
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, consts.ErrNotFound), errors.Is(err, ErrBucketNotFound), errors.Is(err, ErrObjectNotFound):
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrUnauthorized):
		dto.ErrorResponse(c, http.StatusForbidden, err.Error())
	default:
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
	}
}
