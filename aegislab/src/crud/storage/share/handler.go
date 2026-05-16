package share

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

type uploadResp struct {
	ShortCode string `json:"short_code"`
	ShareURL  string `json:"share_url"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Size      int64  `json:"size"`
}

type linkView struct {
	ShortCode        string `json:"short_code"`
	Bucket           string `json:"bucket"`
	OriginalFilename string `json:"original_filename"`
	ContentType      string `json:"content_type"`
	SizeBytes        int64  `json:"size_bytes"`
	OwnerUserID      int    `json:"owner_user_id"`
	CreatedAt        string `json:"created_at"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	MaxViews         *int   `json:"max_views,omitempty"`
	ViewCount        int    `json:"view_count"`
	Status           int    `json:"status"`
	ShareURL         string `json:"share_url"`
}

type listResp struct {
	Items []linkView `json:"items"`
	Total int64      `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}

func (h *Handler) viewOf(l *ShareLink) linkView {
	v := linkView{
		ShortCode:        l.ShortCode,
		Bucket:           l.Bucket,
		OriginalFilename: l.OriginalFilename,
		ContentType:      l.ContentType,
		SizeBytes:        l.SizeBytes,
		OwnerUserID:      l.OwnerUserID,
		CreatedAt:        l.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		MaxViews:         l.MaxViews,
		ViewCount:        l.ViewCount,
		Status:           l.Status,
	}
	if l.ExpiresAt != nil {
		v.ExpiresAt = l.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	base := h.svc.Config().PublicBaseURL
	if base != "" {
		v.ShareURL = base + "/s/" + l.ShortCode
	}
	return v
}

// Upload handles multipart/form-data upload.
//
// Deprecated: prefer /init + presigned PUT + /commit for large files.
// The streaming multipart path buffers the body through aegislab and
// the edge proxy, which on slow / lossy international links measured
// ~38 KB/s on a 17.9 MB APK. New clients should call InitUpload, PUT
// to the returned URL, then CommitUpload.
//
//	@Summary		Upload a file to share (deprecated)
//	@Description	Deprecated — prefer /init + presigned PUT + /commit for large files. Upload a file via multipart/form-data and obtain a short share code.
//	@Tags			Share
//	@ID				share_upload
//	@Accept			mpfd
//	@Produce		json
//	@Security		BearerAuth
//	@Param			file			formData	file							true	"File to upload"
//	@Param			ttl_seconds		formData	int								false	"Lifetime in seconds"
//	@Param			max_views		formData	int								false	"Maximum number of views before expiry"
//	@Success		200				{object}	dto.GenericResponse[uploadResp]	"Upload successful"
//	@Failure		400				{object}	dto.GenericResponse[any]		"Invalid request"
//	@Failure		401				{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		413				{object}	dto.GenericResponse[any]		"File exceeds upload limit"
//	@Failure		500				{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/api/v2/share/upload [post]
//	@x-api-type		{"portal":"true"}
func (h *Handler) Upload(c *gin.Context) {
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "missing user id")
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "missing 'file' part: "+err.Error())
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	defer func() { _ = f.Close() }()

	ttl, _ := strconv.ParseInt(c.PostForm("ttl_seconds"), 10, 64)
	maxViews, _ := strconv.Atoi(c.PostForm("max_views"))
	contentType := fileHeader.Header.Get("Content-Type")

	res, err := h.svc.Upload(c.Request.Context(), UploadInput{
		OwnerUserID: uid,
		Filename:    fileHeader.Filename,
		ContentType: contentType,
		Size:        fileHeader.Size,
		Body:        f,
		TTLSeconds:  ttl,
		MaxViews:    maxViews,
	})
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	resp := uploadResp{ShortCode: res.ShortCode, ShareURL: res.ShareURL, Size: res.Size}
	if res.ExpiresAt != nil {
		resp.ExpiresAt = res.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	dto.JSONResponse(c, http.StatusOK, "share link created", resp)
}

// initUploadReq is the JSON body of POST /api/v2/share/init.
type initUploadReq struct {
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	TTLSeconds  int64  `json:"ttl_seconds"`
	MaxViews    int    `json:"max_views"`
}

// initUploadResp is the JSON body of the InitUpload response.
type initUploadResp struct {
	Code            string            `json:"code"`
	PresignedPutURL string            `json:"presigned_put_url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers,omitempty"`
	ExpiresAt       string            `json:"expires_at"`
	MaxSize         int64             `json:"max_size"`
	CommitURL       string            `json:"commit_url"`
	Bucket          string            `json:"bucket"`
	ObjectKey       string            `json:"object_key"`
}

// commitUploadReq is the JSON body of POST /api/v2/share/{code}/commit.
type commitUploadReq struct {
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// InitUpload reserves a short code + presigned PUT URL for a new share.
// Bytes are uploaded directly to the object store via the returned URL;
// the client then calls CommitUpload to finalise the share row.
//
//	@Summary		Reserve a presigned PUT for a new share
//	@Description	Step 1 of the presigned-PUT flow. Validates quota + per-file cap, reserves a short code in 'pending' lifecycle, and returns a presigned PUT URL (15 min TTL) plus the commit URL the client must call after the PUT lands.
//	@Tags			Share
//	@ID				share_init_upload
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		initUploadReq						true	"Init upload request"
//	@Success		200		{object}	dto.GenericResponse[initUploadResp]	"Init successful"
//	@Failure		400		{object}	dto.GenericResponse[any]			"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		413		{object}	dto.GenericResponse[any]			"File exceeds upload limit"
//	@Failure		507		{object}	dto.GenericResponse[any]			"User quota exceeded"
//	@Failure		500		{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/api/v2/share/init [post]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) InitUpload(c *gin.Context) {
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "missing user id")
		return
	}
	var body initUploadReq
	if err := c.ShouldBindJSON(&body); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.Filename == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "filename is required")
		return
	}
	if body.Size <= 0 {
		dto.ErrorResponse(c, http.StatusBadRequest, "size must be > 0")
		return
	}

	res, err := h.svc.InitUpload(c.Request.Context(), InitUploadInput{
		OwnerUserID: uid,
		Filename:    body.Filename,
		ContentType: body.ContentType,
		Size:        body.Size,
		TTLSeconds:  body.TTLSeconds,
		MaxViews:    body.MaxViews,
	})
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	dto.JSONResponse(c, http.StatusOK, "init upload", initUploadResp{
		Code:            res.Code,
		PresignedPutURL: res.PresignedURL,
		Method:          res.Method,
		Headers:         res.Headers,
		ExpiresAt:       res.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		MaxSize:         res.MaxSize,
		CommitURL:       res.CommitURL,
		Bucket:          res.Bucket,
		ObjectKey:       res.ObjectKey,
	})
}

// CommitUpload finalises a pending share after the client's direct PUT
// to the presigned URL completes. The server Stats the object to
// confirm it landed, optionally verifies size, and flips lifecycle to
// 'live'. Safe to retry — second invocation is a no-op.
//
//	@Summary		Commit a presigned-PUT share upload
//	@Description	Step 3 of the presigned-PUT flow. Stats the object to confirm the PUT landed and flips the share row from 'pending' to 'live'. Idempotent on retry.
//	@Tags			Share
//	@ID				share_commit_upload
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			code	path		string							true	"Share short code"
//	@Param			body	body		commitUploadReq					false	"Optional verification body"
//	@Success		200		{object}	dto.GenericResponse[uploadResp]	"Commit successful"
//	@Failure		400		{object}	dto.GenericResponse[any]		"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]		"Forbidden (non-owner)"
//	@Failure		404		{object}	dto.GenericResponse[any]		"Share not found or object missing"
//	@Failure		413		{object}	dto.GenericResponse[any]		"Uploaded object exceeds size limit"
//	@Failure		409		{object}	dto.GenericResponse[any]		"Size mismatch between declared and uploaded"
//	@Failure		500		{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/api/v2/share/{code}/commit [post]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) CommitUpload(c *gin.Context) {
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "missing user id")
		return
	}
	code := c.Param("code")
	if code == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "code is required")
		return
	}
	var body commitUploadReq
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			dto.ErrorResponse(c, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
	}

	res, err := h.svc.CommitUpload(c.Request.Context(), CommitUploadInput{
		OwnerUserID: uid,
		Code:        code,
		Size:        body.Size,
		ContentType: body.ContentType,
		SHA256:      body.SHA256,
	})
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	logrus.WithFields(logrus.Fields{
		"event":   "share.commit",
		"code":    code,
		"user_id": uid,
		"size":    res.Size,
	}).Info("share commit")

	resp := uploadResp{ShortCode: res.ShortCode, ShareURL: res.ShareURL, Size: res.Size}
	if res.ExpiresAt != nil {
		resp.ExpiresAt = res.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	dto.JSONResponse(c, http.StatusOK, "share committed", resp)
}

// List returns share links owned by the current user.
//
//	@Summary		List own share links
//	@Description	List share links owned by the current authenticated user
//	@Tags			Share
//	@ID				share_list
//	@Produce		json
//	@Security		BearerAuth
//	@Param			page			query		int								false	"Page number"
//	@Param			size			query		int								false	"Page size"
//	@Param			include_expired	query		bool							false	"Include expired links"
//	@Success		200				{object}	dto.GenericResponse[listResp]	"Share links listed successfully"
//	@Failure		401				{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		500				{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/api/v2/share [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) List(c *gin.Context) {
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "missing user id")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	includeExpired := c.Query("include_expired") == "true"
	rows, total, err := h.svc.ListOwn(c.Request.Context(), uid, page, size, includeExpired)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	items := make([]linkView, 0, len(rows))
	for i := range rows {
		items = append(items, h.viewOf(&rows[i]))
	}
	dto.JSONResponse(c, http.StatusOK, "share links listed", listResp{Items: items, Total: total, Page: page, Size: size})
}

// GetOne returns metadata for a single share link.
//
//	@Summary		Get share link detail
//	@Description	Get metadata for a single share link by short code
//	@Tags			Share
//	@ID				share_get_one
//	@Produce		json
//	@Security		BearerAuth
//	@Param			code	path		string							true	"Share short code"
//	@Success		200		{object}	dto.GenericResponse[linkView]	"Share link detail"
//	@Failure		401		{object}	dto.GenericResponse[any]		"Authentication required"
//	@Failure		404		{object}	dto.GenericResponse[any]		"Share link not found"
//	@Failure		410		{object}	dto.GenericResponse[any]		"Share link no longer available"
//	@Failure		500		{object}	dto.GenericResponse[any]		"Internal server error"
//	@Router			/api/v2/share/{code} [get]
//	@x-api-type		{"portal":"true"}
func (h *Handler) GetOne(c *gin.Context) {
	uid, _ := middleware.GetCurrentUserID(c)
	code := c.Param("code")
	link, err := h.svc.Get(c.Request.Context(), code, uid, false)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	dto.JSONResponse(c, http.StatusOK, "share link detail", h.viewOf(link))
}

// Revoke marks a share link as revoked.
//
//	@Summary		Revoke share link
//	@Description	Revoke a share link owned by the current authenticated user
//	@Tags			Share
//	@ID				share_revoke
//	@Produce		json
//	@Security		BearerAuth
//	@Param			code	path		string						true	"Share short code"
//	@Success		204		{object}	dto.GenericResponse[any]	"Share link revoked"
//	@Failure		401		{object}	dto.GenericResponse[any]	"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]	"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]	"Share link not found"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/share/{code} [delete]
//	@x-api-type		{"portal":"true"}
func (h *Handler) Revoke(c *gin.Context) {
	uid, _ := middleware.GetCurrentUserID(c)
	code := c.Param("code")
	if err := h.svc.Revoke(c.Request.Context(), code, uid, false); err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Redirect is the unauthenticated `/s/:code` entry point. When the
// `[share] public_base_url` is configured (driver presign URLs are
// publicly resolvable), it 302s. Otherwise — the common in-cluster
// setup where presign URLs target an internal Service DNS not reachable
// from outside — it streams the bytes through this handler so
// edge-proxy can deliver them.
//
//	@Summary		Resolve a public share link
//	@Description	Public unauthenticated entry point. Either redirects (302) to a presigned URL or streams the file bytes directly.
//	@Tags			Share
//	@ID				share_redirect
//	@Produce		octet-stream
//	@Param			code	path		string						true	"Share short code"
//	@Success		200		{file}		binary						"Streamed file content"
//	@Success		302		{string}	string						"Redirect to presigned URL"
//	@Failure		404		{string}	string						"Not found"
//	@Failure		410		{string}	string						"Share link no longer available"
//	@Failure		500		{string}	string						"Internal server error"
//	@Router			/s/{code} [get]
func (h *Handler) Redirect(c *gin.Context) {
	code := c.Param("code")
	if h.svc.Config().PublicBaseURL != "" {
		url, err := h.svc.View(c.Request.Context(), code)
		if err != nil {
			h.writeRedirectError(c, err)
			return
		}
		c.Redirect(http.StatusFound, url)
		return
	}
	rc, meta, link, err := h.svc.Stream(c.Request.Context(), code)
	if err != nil {
		h.writeRedirectError(c, err)
		return
	}
	defer func() { _ = rc.Close() }()
	if link.ContentType != "" {
		c.Header("Content-Type", link.ContentType)
	} else if meta != nil && meta.ContentType != "" {
		c.Header("Content-Type", meta.ContentType)
	}
	if meta != nil && meta.Size > 0 {
		c.Header("Content-Length", strconv.FormatInt(meta.Size, 10))
	}
	c.Header("Content-Disposition", contentDisposition(link.OriginalFilename))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, rc)
}

func (h *Handler) writeRedirectError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrShareNotFound):
		c.String(http.StatusNotFound, "not found")
	case errors.Is(err, ErrShareGone):
		c.String(http.StatusGone, "share link no longer available")
	default:
		c.String(http.StatusInternalServerError, err.Error())
	}
}

func (h *Handler) writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrShareNotFound):
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrShareGone):
		dto.ErrorResponse(c, http.StatusGone, err.Error())
	case errors.Is(err, ErrUploadTooLarge):
		dto.ErrorResponse(c, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, ErrQuotaExceeded):
		dto.ErrorResponse(c, http.StatusInsufficientStorage, err.Error())
	case errors.Is(err, ErrForbidden):
		dto.ErrorResponse(c, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrShortCodeFailure):
		dto.ErrorResponse(c, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrCommitObjectMissing):
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrCommitSizeMismatch):
		dto.ErrorResponse(c, http.StatusConflict, err.Error())
	default:
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
	}
}
