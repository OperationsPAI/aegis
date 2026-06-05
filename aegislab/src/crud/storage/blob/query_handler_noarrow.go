//go:build !duckdb_arrow

package blob

import (
	"net/http"

	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
)

const errQueryRequiresTag = "bucket query requires building aegis-blob with -tags duckdb_arrow"

// QueryBucket is the tagless stub — the DuckDB engine is compiled out.
//
//	@Summary		Query bucket parquet objects (SQL)
//	@Description	Run a read-only SELECT/WITH query over the parquet objects under a prefix (or an explicit key list). Default response is an Arrow IPC stream; send Accept: application/json for decoded rows. One VIEW per *.parquet; view name = sanitized filestem. p50/p90/p95/p99 percentile macros are pre-registered.
//	@Tags			Blob
//	@ID				blob_query_bucket
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string						true	"Bucket name"
//	@Param			request	body		map[string]interface{}		true	"Query request"
//	@Success		200		{object}	dto.GenericResponse[any]	"Decoded rows or Arrow IPC stream"
//	@Failure		501		{object}	dto.GenericResponse[any]	"Built without duckdb_arrow"
//	@Router			/api/v2/blob/buckets/{bucket}/query [post]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) QueryBucket(c *gin.Context) {
	dto.ErrorResponse(c, http.StatusNotImplemented, errQueryRequiresTag)
}

// SchemaBucket is the tagless stub.
//
//	@Summary		Bucket parquet schema
//	@Description	List the logical tables exposed over the parquet objects under a prefix (or an explicit key list): one VIEW per *.parquet with its columns and row count.
//	@Tags			Blob
//	@ID				blob_schema_bucket
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string						true	"Bucket name"
//	@Param			prefix	query		string						false	"Key prefix selector"
//	@Param			keys	query		[]string					false	"Explicit object keys selector"
//	@Success		200		{object}	dto.GenericResponse[any]	"Tables listed"
//	@Failure		501		{object}	dto.GenericResponse[any]	"Built without duckdb_arrow"
//	@Router			/api/v2/blob/buckets/{bucket}/schema [get]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) SchemaBucket(c *gin.Context) {
	dto.ErrorResponse(c, http.StatusNotImplemented, errQueryRequiresTag)
}
