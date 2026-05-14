package blob

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Handler hosts the HTTP surface — the ingestion role. Auth happens
// upstream in middleware; per-bucket ACL happens here via Authorizer.
type Handler struct {
	svc        *Service
	signingKey []byte
	auth       *Authorizer
}

func NewHandler(svc *Service, auth *Authorizer, deps RegistryDeps) *Handler {
	return &Handler{svc: svc, auth: auth, signingKey: deps.SigningKey}
}

func subjectFromContext(c *gin.Context) Subject {
	uid, _ := middleware.GetCurrentUserID(c)
	return Subject{UserID: uid}
}

// ---- Request / response shapes ----

type presignPutReq struct {
	Key           string            `json:"key,omitempty"`
	ContentType   string            `json:"content_type"`
	ContentLength int64             `json:"content_length,omitempty"`
	EntityKind    string            `json:"entity_kind,omitempty"`
	EntityID      string            `json:"entity_id,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	TTLSeconds    int               `json:"ttl_seconds,omitempty"`
}

type presignGetReq struct {
	Key                 string `json:"key" binding:"required"`
	ResponseContentType string `json:"response_content_type,omitempty"`
	TTLSeconds          int    `json:"ttl_seconds,omitempty"`
}

type listResp struct {
	Items      []ObjectRecord `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// BucketSummary is the wire shape returned by GET /blob/buckets — the
// UI uses it to populate a bucket picker without hard-coding names.
type BucketSummary struct {
	Name           string `json:"name"`
	Driver         string `json:"driver"`
	MaxObjectBytes int64  `json:"max_object_bytes,omitempty"`
	RetentionDays  int    `json:"retention_days,omitempty"`
	PublicRead     bool   `json:"public_read,omitempty"`
}

type listBucketsResp struct {
	Items []BucketSummary `json:"items"`
}

// ListBuckets surfaces the configured bucket registry so the console
// can populate a picker without hard-coded names. Auth lives in the
// route group; ACL filtering by caller is intentionally not done yet —
// the registry is treated as public catalog data within the platform.
//
//	@Summary		List blob buckets
//	@Description	List configured blob buckets in the registry
//	@Tags			Blob
//	@ID				blob_list_buckets
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	dto.GenericResponse[listBucketsResp]	"Buckets listed successfully"
//	@Failure		401	{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		500	{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/blob/buckets [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) ListBuckets(c *gin.Context) {
	names := h.svc.Registry().Names()
	out := make([]BucketSummary, 0, len(names))
	for _, name := range names {
		b, err := h.svc.Registry().Lookup(name)
		if err != nil {
			continue
		}
		out = append(out, BucketSummary{
			Name:           name,
			Driver:         b.Config.Driver,
			MaxObjectBytes: b.Config.MaxObjectBytes,
			RetentionDays:  b.Config.RetentionDays,
			PublicRead:     b.Config.PublicRead,
		})
	}
	c.JSON(http.StatusOK, listBucketsResp{Items: out})
}

// ---- Endpoints ----

// PresignPut returns a presigned PUT URL for uploading an object.
//
//	@Summary		Presign object upload
//	@Description	Issue a presigned PUT URL for uploading an object to the bucket
//	@Tags			Blob
//	@ID				blob_presign_put
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string									true	"Bucket name"
//	@Param			request	body		presignPutReq							true	"Presign PUT request"
//	@Success		200		{object}	dto.GenericResponse[PresignedRequest]	"Presigned URL issued"
//	@Failure		400		{object}	dto.GenericResponse[any]				"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]				"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]				"Bucket not found"
//	@Failure		500		{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/presign-put [post]
//	@x-api-type		{"portal":"true"}
func (h *Handler) PresignPut(c *gin.Context) {
	bucket := c.Param("bucket")
	var req presignPutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	b, err := h.svc.Registry().Lookup(bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	sub := subjectFromContext(c)
	if !h.auth.CanWrite(&b.Config, sub) {
		dto.ErrorResponse(c, http.StatusForbidden, ErrUnauthorized.Error())
		return
	}
	uid := sub.UserID
	in := PresignPutInput{
		Bucket: bucket, Key: req.Key,
		ContentType: req.ContentType, ContentLength: req.ContentLength,
		EntityKind: req.EntityKind, EntityID: req.EntityID,
		Metadata: req.Metadata,
		TTL:      time.Duration(req.TTLSeconds) * time.Second,
	}
	if uid > 0 {
		in.UploadedBy = &uid
	}
	res, err := h.svc.PresignPut(c.Request.Context(), in)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// PresignGet returns a presigned GET URL for downloading an object.
//
//	@Summary		Presign object download
//	@Description	Issue a presigned GET URL for downloading an object from the bucket
//	@Tags			Blob
//	@ID				blob_presign_get
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string									true	"Bucket name"
//	@Param			request	body		presignGetReq							true	"Presign GET request"
//	@Success		200		{object}	dto.GenericResponse[PresignedRequest]	"Presigned URL issued"
//	@Failure		400		{object}	dto.GenericResponse[any]				"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]				"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]				"Bucket or object not found"
//	@Failure		500		{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/presign-get [post]
//	@x-api-type		{"portal":"true"}
func (h *Handler) PresignGet(c *gin.Context) {
	bucket := c.Param("bucket")
	var req presignGetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	b, err := h.svc.Registry().Lookup(bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	rec, err := h.svc.repo.FindByKey(c.Request.Context(), bucket, req.Key)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	if !h.auth.CanRead(&b.Config, subjectFromContext(c), rec.UploadedBy) {
		dto.ErrorResponse(c, http.StatusForbidden, ErrUnauthorized.Error())
		return
	}
	pr, err := h.svc.PresignGet(c.Request.Context(), bucket, req.Key, GetOpts{
		ResponseContentType: req.ResponseContentType,
		TTL:                 time.Duration(req.TTLSeconds) * time.Second,
	})
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, pr)
}

// InlineGet streams a single-segment object inline through the API.
//
//	@Summary		Inline object download
//	@Description	Stream object bytes inline through the API for single-segment keys
//	@Tags			Blob
//	@ID				blob_inline_get
//	@Produce		octet-stream
//	@Security		BearerAuth
//	@Param			bucket	path		string						true	"Bucket name"
//	@Param			key		path		string						true	"Object key"
//	@Success		200		{file}		binary						"Streamed object content"
//	@Failure		401		{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]	"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]	"Bucket or object not found"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/objects/{key} [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) InlineGet(c *gin.Context) {
	bucket := c.Param("bucket")
	key := c.Param("key")
	b, err := h.svc.Registry().Lookup(bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	rec, err := h.svc.repo.FindByKey(c.Request.Context(), bucket, key)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	if !h.auth.CanRead(&b.Config, subjectFromContext(c), rec.UploadedBy) {
		dto.ErrorResponse(c, http.StatusForbidden, ErrUnauthorized.Error())
		return
	}
	rc, meta, err := h.svc.Get(c.Request.Context(), bucket, key)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	defer func() { _ = rc.Close() }()
	if meta.ContentType != "" {
		c.Header("Content-Type", meta.ContentType)
	}
	c.Header("Content-Length", strconv.FormatInt(meta.Size, 10))
	_, _ = io.Copy(c.Writer, rc)
}

// Stat returns object metadata without streaming the body.
//
//	@Summary		Stat object
//	@Description	Return object metadata without streaming the body
//	@Tags			Blob
//	@ID				blob_stat
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string								true	"Bucket name"
//	@Param			key		path		string								true	"Object key"
//	@Success		200		{object}	dto.GenericResponse[ObjectMeta]		"Object metadata"
//	@Failure		401		{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		404		{object}	dto.GenericResponse[any]			"Bucket or object not found"
//	@Failure		500		{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/objects/{key} [head]
//	@x-api-type		{"portal":"true"}
func (h *Handler) Stat(c *gin.Context) {
	bucket := c.Param("bucket")
	key := c.Param("key")
	meta, err := h.svc.Stat(c.Request.Context(), bucket, key)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, meta)
}

// Delete removes an object from the bucket.
//
//	@Summary		Delete object
//	@Description	Delete an object from the bucket
//	@Tags			Blob
//	@ID				blob_delete
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string						true	"Bucket name"
//	@Param			key		path		string						true	"Object key"
//	@Success		204		{object}	dto.GenericResponse[any]	"Object deleted"
//	@Failure		401		{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]	"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]	"Bucket or object not found"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/objects/{key} [delete]
//	@x-api-type		{"portal":"true"}
func (h *Handler) Delete(c *gin.Context) {
	bucket := c.Param("bucket")
	key := c.Param("key")
	b, err := h.svc.Registry().Lookup(bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	rec, err := h.svc.repo.FindByKey(c.Request.Context(), bucket, key)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	if !h.auth.CanWrite(&b.Config, subjectFromContext(c)) &&
		(rec.UploadedBy == nil || *rec.UploadedBy != subjectFromContext(c).UserID) {
		dto.ErrorResponse(c, http.StatusForbidden, ErrUnauthorized.Error())
		return
	}
	if err := h.svc.Delete(c.Request.Context(), bucket, key); err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// List returns DB-backed object metadata records for the bucket.
//
//	@Summary		List object records (DB)
//	@Description	List object metadata records for the bucket from the metadata database
//	@Tags			Blob
//	@ID				blob_list
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket		path		string							true	"Bucket name"
//	@Param			entity_kind	query		string							false	"Filter by entity kind"
//	@Param			entity_id	query		string							false	"Filter by entity ID"
//	@Param			cursor		query		int								false	"Pagination cursor"
//	@Param			limit		query		int								false	"Page size"
//	@Success		200			{object}	dto.GenericResponse[listResp]	"Objects listed"
//	@Failure		401			{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		500			{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/objects [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) List(c *gin.Context) {
	bucket := c.Param("bucket")
	f := ListFilter{
		Bucket:     bucket,
		EntityKind: c.Query("entity_kind"),
		EntityID:   c.Query("entity_id"),
	}
	if s := c.Query("cursor"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			f.Cursor = v
		}
	}
	if s := c.Query("limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			f.Limit = v
		}
	}
	rows, err := h.svc.List(c.Request.Context(), f)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	resp := listResp{Items: rows}
	if len(rows) > 0 && len(rows) == f.Limit {
		resp.NextCursor = strconv.FormatInt(rows[len(rows)-1].ID, 10)
	}
	c.JSON(http.StatusOK, resp)
}

// StreamGet is the wildcard-key counterpart of InlineGet. It streams
// the object bytes directly to the response writer. Used by clients
// that need keys-with-slashes (zip streaming, file tree responses)
// without per-segment routing constraints.
//
//	@Summary		Stream object (wildcard key)
//	@Description	Stream object bytes for keys that contain slashes (zip streaming, file tree responses)
//	@Tags			Blob
//	@ID				blob_stream_get
//	@Produce		octet-stream
//	@Security		BearerAuth
//	@Param			bucket	path		string						true	"Bucket name"
//	@Param			key		path		string						true	"Object key (may contain slashes)"
//	@Success		200		{file}		binary						"Streamed object content"
//	@Failure		401		{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]	"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]	"Bucket or object not found"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/stream/{key} [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) StreamGet(c *gin.Context) {
	bucket := c.Param("bucket")
	key := strings.TrimPrefix(c.Param("key"), "/")
	b, err := h.svc.Registry().Lookup(bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	rec, err := h.svc.repo.FindByKey(c.Request.Context(), bucket, key)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	if !h.auth.CanRead(&b.Config, subjectFromContext(c), rec.UploadedBy) {
		dto.ErrorResponse(c, http.StatusForbidden, ErrUnauthorized.Error())
		return
	}
	rc, meta, err := h.svc.GetReader(c.Request.Context(), bucket, key)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	defer func() { _ = rc.Close() }()
	if meta.ContentType != "" {
		c.Header("Content-Type", meta.ContentType)
	}
	c.Header("Content-Length", strconv.FormatInt(meta.Size, 10))
	_, _ = io.Copy(c.Writer, rc)
}

// ListObjects exposes the driver-level (backend storage) listing,
// distinct from List which reads the metadata DB. Query params follow
// the S3 list-objects-v2 conventions: prefix, max_keys,
// continuation_token, delimiter.
//
//	@Summary		List driver-level objects
//	@Description	List objects directly from the storage driver (S3 list-objects-v2 conventions)
//	@Tags			Blob
//	@ID				blob_list_objects
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket				path		string								true	"Bucket name"
//	@Param			prefix				query		string								false	"Key prefix filter"
//	@Param			max_keys			query		int									false	"Maximum keys per page"
//	@Param			continuation_token	query		string								false	"Opaque continuation token"
//	@Param			delimiter			query		string								false	"Hierarchical listing delimiter"
//	@Success		200					{object}	dto.GenericResponse[ListResult]		"Objects listed"
//	@Failure		401					{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		404					{object}	dto.GenericResponse[any]			"Bucket not found"
//	@Failure		500					{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/object-list [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) ListObjects(c *gin.Context) {
	bucket := c.Param("bucket")
	opts := ListObjectsOpts{
		Prefix:            c.Query("prefix"),
		ContinuationToken: c.Query("continuation_token"),
		Delimiter:         c.Query("delimiter"),
	}
	if s := c.Query("max_keys"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			opts.MaxKeys = v
		}
	}
	res, err := h.svc.ListObjects(c.Request.Context(), bucket, opts)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// Raw serves the localfs driver's signed token URLs. Verifies the
// HMAC + expiry, then either streams the file (GET) or persists the
// body (PUT). Buckets backed by s3 never produce these tokens.
//
//	@Summary		Localfs signed token GET/PUT
//	@Description	Auth-free endpoint that verifies an HMAC-signed token and either streams (GET) or persists (PUT) the object body. The token itself is the auth.
//	@Tags			Blob
//	@ID				blob_raw
//	@Param			token	path		string						true	"Signed token"
//	@Success		200		{file}		binary						"Streamed object content (GET)"
//	@Success		204		{object}	dto.GenericResponse[any]	"Object persisted (PUT)"
//	@Failure		400		{object}	dto.GenericResponse[any]	"Unknown token op"
//	@Failure		403		{object}	dto.GenericResponse[any]	"Invalid or expired token"
//	@Failure		404		{object}	dto.GenericResponse[any]	"Bucket not found"
//	@Failure		405		{object}	dto.GenericResponse[any]	"Method not allowed for token op"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/blob/raw/{token} [get]
func (h *Handler) Raw(c *gin.Context) {
	raw := c.Param("token")
	tok, err := DecodeToken(h.signingKey, raw)
	if err != nil {
		dto.ErrorResponse(c, http.StatusForbidden, err.Error())
		return
	}
	b, err := h.svc.Registry().Lookup(tok.Bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	switch tok.Op {
	case OpGet:
		if c.Request.Method != http.MethodGet {
			dto.ErrorResponse(c, http.StatusMethodNotAllowed, "token is GET-only")
			return
		}
		rc, meta, err := b.Driver.Get(c.Request.Context(), tok.Key)
		if err != nil {
			h.writeServiceError(c, err)
			return
		}
		defer func() { _ = rc.Close() }()
		if meta.ContentType != "" {
			c.Header("Content-Type", meta.ContentType)
		}
		c.Header("Content-Length", strconv.FormatInt(meta.Size, 10))
		_, _ = io.Copy(c.Writer, rc)
	case OpPut:
		if c.Request.Method != http.MethodPut {
			dto.ErrorResponse(c, http.StatusMethodNotAllowed, "token is PUT-only")
			return
		}
		_, err := b.Driver.Put(c.Request.Context(), tok.Key, c.Request.Body, PutOpts{
			ContentType:   c.GetHeader("Content-Type"),
			ContentLength: c.Request.ContentLength,
		})
		if err != nil {
			h.writeServiceError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	default:
		dto.ErrorResponse(c, http.StatusBadRequest, "unknown token op")
	}
}

func (h *Handler) writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrBucketNotFound), errors.Is(err, ErrObjectNotFound):
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDriverNotImplemented):
		dto.ErrorResponse(c, http.StatusNotImplemented, err.Error())
	case errors.Is(err, ErrTokenInvalid), errors.Is(err, ErrUnauthorized):
		dto.ErrorResponse(c, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrObjectTooLarge):
		dto.ErrorResponse(c, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, ErrContentTypeRejected):
		dto.ErrorResponse(c, http.StatusUnsupportedMediaType, err.Error())
	default:
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
	}
}
