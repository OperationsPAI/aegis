package share

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
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
	c.JSON(http.StatusOK, resp)
}

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
	c.JSON(http.StatusOK, listResp{Items: items, Total: total, Page: page, Size: size})
}

func (h *Handler) GetOne(c *gin.Context) {
	uid, _ := middleware.GetCurrentUserID(c)
	code := c.Param("code")
	link, err := h.svc.Get(c.Request.Context(), code, uid, false)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.viewOf(link))
}

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
	default:
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
	}
}
