package pages

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aegis/platform/dto"
	"aegis/platform/middleware"

	"github.com/gin-gonic/gin"
)

// PageSiteResponse is the wire shape for management endpoints.
type PageSiteResponse struct {
	ID         int64       `json:"id"`
	Slug       string      `json:"slug"`
	Visibility string      `json:"visibility"`
	Title      string      `json:"title"`
	OwnerID    int         `json:"owner_id"`
	SizeBytes  int64       `json:"size_bytes"`
	FileCount  int32       `json:"file_count"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
	URL        string      `json:"url"`
	Files      []FileEntry `json:"files,omitempty"`
}

type updateReq struct {
	Slug       *string `json:"slug,omitempty"`
	Visibility *string `json:"visibility,omitempty"`
	Title      *string `json:"title,omitempty"`
}

type listResp struct {
	Items []PageSiteResponse `json:"items"`
}

// Handler is the management API (Portal + SDK). SSR lives in render.go.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// CreatePage uploads a brand new site.
//
//	@Summary		Create page site
//	@Description	Upload markdown + asset files to create a new static-site. Multipart body; each file part's filename is its site-relative path.
//	@Tags			Pages
//	@ID				pages_create
//	@Accept			multipart/form-data
//	@Produce		json
//	@Security		BearerAuth
//	@Param			slug		formData	string	false	"Optional slug. Auto-derived if absent."
//	@Param			visibility	formData	string	false	"public_listed | public_unlisted | private"
//	@Param			title		formData	string	false	"Display title"
//	@Param			files		formData	file	true	"Site files (any number; at least one .md)"
//	@Success		201			{object}	dto.GenericResponse[PageSiteResponse]
//	@Failure		400			{object}	dto.GenericResponse[any]
//	@Failure		401			{object}	dto.GenericResponse[any]
//	@Failure		413			{object}	dto.GenericResponse[any]
//	@Router			/api/v2/pages [post]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) CreatePage(c *gin.Context) {
	start := time.Now()
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	slug := strings.TrimSpace(c.PostForm("slug"))
	visibility := strings.TrimSpace(c.PostForm("visibility"))
	title := strings.TrimSpace(c.PostForm("title"))

	files, err := collectFiles(c)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}

	site, err := h.svc.CreateSite(c.Request.Context(), uid, slug, visibility, title, files)
	if err != nil {
		writeServiceError(c, err)
		middleware.AuditAction(c, "pages.create", "", err, start, uid, ResourcePages)
		return
	}
	middleware.AuditAction(c, "pages.create", "slug="+site.Slug, nil, start, uid, ResourcePages)
	dto.JSONResponse(c, http.StatusCreated, "page site created", siteToResponse(site, nil))
}

// ReplacePage replaces the files inside an existing site.
//
//	@Summary		Replace site files
//	@Description	Atomically replace every file under a site with the multipart upload payload.
//	@Tags			Pages
//	@ID				pages_replace
//	@Accept			multipart/form-data
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		int		true	"Site ID"
//	@Param			files	formData	file	true	"Site files (any number; at least one .md)"
//	@Success		200		{object}	dto.GenericResponse[PageSiteResponse]
//	@Failure		400		{object}	dto.GenericResponse[any]
//	@Failure		401		{object}	dto.GenericResponse[any]
//	@Failure		403		{object}	dto.GenericResponse[any]
//	@Failure		404		{object}	dto.GenericResponse[any]
//	@Router			/api/v2/pages/{id}/upload [post]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) ReplacePage(c *gin.Context) {
	start := time.Now()
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	id, err := pathID(c)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	files, err := collectFiles(c)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	site, err := h.svc.ReplaceFiles(c.Request.Context(), uid, id, files)
	if err != nil {
		writeServiceError(c, err)
		middleware.AuditAction(c, "pages.replace", "", err, start, uid, ResourcePages)
		return
	}
	middleware.AuditAction(c, "pages.replace", "id="+strconv.FormatInt(id, 10), nil, start, uid, ResourcePages)
	dto.JSONResponse(c, http.StatusOK, "page site replaced", siteToResponse(site, nil))
}

// UpdatePage patches slug / visibility / title.
//
//	@Summary		Update site metadata
//	@Description	Patch slug / visibility / title for an existing site.
//	@Tags			Pages
//	@ID				pages_update
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path	int			true	"Site ID"
//	@Param			body	body	updateReq	true	"Fields to update"
//	@Success		200		{object}	dto.GenericResponse[PageSiteResponse]
//	@Failure		400		{object}	dto.GenericResponse[any]
//	@Failure		401		{object}	dto.GenericResponse[any]
//	@Failure		403		{object}	dto.GenericResponse[any]
//	@Failure		404		{object}	dto.GenericResponse[any]
//	@Router			/api/v2/pages/{id} [patch]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) UpdatePage(c *gin.Context) {
	start := time.Now()
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	id, err := pathID(c)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	var req updateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	site, err := h.svc.UpdateMeta(c.Request.Context(), uid, id, req.Slug, req.Visibility, req.Title)
	if err != nil {
		writeServiceError(c, err)
		middleware.AuditAction(c, "pages.update", "", err, start, uid, ResourcePages)
		return
	}
	middleware.AuditAction(c, "pages.update", "id="+strconv.FormatInt(id, 10), nil, start, uid, ResourcePages)
	dto.JSONResponse(c, http.StatusOK, "page site updated", siteToResponse(site, nil))
}

// DeletePage removes site blobs + row.
//
//	@Summary		Delete site
//	@Description	Delete every file in the site and remove the metadata row.
//	@Tags			Pages
//	@ID				pages_delete
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path	int	true	"Site ID"
//	@Success		204
//	@Failure		401	{object}	dto.GenericResponse[any]
//	@Failure		403	{object}	dto.GenericResponse[any]
//	@Failure		404	{object}	dto.GenericResponse[any]
//	@Router			/api/v2/pages/{id} [delete]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) DeletePage(c *gin.Context) {
	start := time.Now()
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	id, err := pathID(c)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.DeleteSite(c.Request.Context(), uid, id); err != nil {
		writeServiceError(c, err)
		middleware.AuditAction(c, "pages.delete", "", err, start, uid, ResourcePages)
		return
	}
	middleware.AuditAction(c, "pages.delete", "id="+strconv.FormatInt(id, 10), nil, start, uid, ResourcePages)
	c.Status(http.StatusNoContent)
}

// ListMine returns the caller's sites.
//
//	@Summary		List my sites
//	@Description	Return the caller's page sites.
//	@Tags			Pages
//	@ID				pages_list_mine
//	@Produce		json
//	@Security		BearerAuth
//	@Param			limit	query	int	false	"Page size"
//	@Param			offset	query	int	false	"Offset"
//	@Success		200	{object}	dto.GenericResponse[listResp]
//	@Failure		401	{object}	dto.GenericResponse[any]
//	@Router			/api/v2/pages [get]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) ListMine(c *gin.Context) {
	uid, ok := middleware.GetCurrentUserID(c)
	if !ok || uid <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	limit, offset := paginate(c)
	sites, err := h.svc.Mine(c.Request.Context(), uid, limit, offset)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	items := make([]PageSiteResponse, 0, len(sites))
	for i := range sites {
		items = append(items, siteToResponse(&sites[i], nil))
	}
	dto.JSONResponse(c, http.StatusOK, "ok", listResp{Items: items})
}

// ListPublic returns publicly listed sites.
//
//	@Summary		List public sites
//	@Description	Return sites with visibility=public_listed.
//	@Tags			Pages
//	@ID				pages_list_public
//	@Produce		json
//	@Param			limit	query	int	false	"Page size"
//	@Param			offset	query	int	false	"Offset"
//	@Success		200	{object}	dto.GenericResponse[listResp]
//	@Router			/api/v2/pages/public [get]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) ListPublic(c *gin.Context) {
	limit, offset := paginate(c)
	sites, err := h.svc.Public(c.Request.Context(), limit, offset)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	items := make([]PageSiteResponse, 0, len(sites))
	for i := range sites {
		items = append(items, siteToResponse(&sites[i], nil))
	}
	dto.JSONResponse(c, http.StatusOK, "ok", listResp{Items: items})
}

// Detail returns a single site + file list.
//
//	@Summary		Get site detail
//	@Description	Return a single site with its file listing. Private sites are restricted to the owner.
//	@Tags			Pages
//	@ID				pages_detail
//	@Produce		json
//	@Param			id	path	int	true	"Site ID"
//	@Success		200	{object}	dto.GenericResponse[PageSiteResponse]
//	@Failure		403	{object}	dto.GenericResponse[any]
//	@Failure		404	{object}	dto.GenericResponse[any]
//	@Router			/api/v2/pages/{id} [get]
//	@x-api-type		{"portal":"true","sdk":"true"}
func (h *Handler) Detail(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	uid, _ := middleware.GetCurrentUserID(c)
	site, files, err := h.svc.Detail(c.Request.Context(), uid, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	dto.JSONResponse(c, http.StatusOK, "ok", siteToResponse(site, files))
}

// ---- helpers ----

func collectFiles(c *gin.Context) ([]UploadFile, error) {
	form, err := c.MultipartForm()
	if err != nil {
		return nil, err
	}
	var out []UploadFile
	for field, hs := range form.File {
		for _, fh := range hs {
			rel := strings.TrimSpace(fh.Filename)
			if rel == "" {
				rel = field
			}
			body, err := readMultipart(fh)
			if err != nil {
				return nil, err
			}
			ct := fh.Header.Get("Content-Type")
			if ct == "" {
				ct = mime.TypeByExtension(filepath.Ext(rel))
			}
			out = append(out, UploadFile{
				Path:        rel,
				ContentType: ct,
				Body:        body,
			})
		}
	}
	return out, nil
}

func readMultipart(fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

func pathID(c *gin.Context) (int64, error) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func paginate(c *gin.Context) (limit, offset int) {
	if s := c.Query("limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}
	if s := c.Query("offset"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			offset = v
		}
	}
	if limit == 0 {
		limit = 50
	}
	return
}

func siteToResponse(site *PageSite, files []FileEntry) PageSiteResponse {
	return PageSiteResponse{
		ID:         site.ID,
		Slug:       site.Slug,
		Visibility: site.Visibility,
		Title:      site.Title,
		OwnerID:    site.OwnerID,
		SizeBytes:  site.SizeBytes,
		FileCount:  site.FileCount,
		CreatedAt:  site.CreatedAt,
		UpdatedAt:  site.UpdatedAt,
		URL:        "/p/" + site.Slug,
		Files:      files,
	}
}

func writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrForbidden):
		dto.ErrorResponse(c, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrInvalidSlug),
		errors.Is(err, ErrSlugTaken),
		errors.Is(err, ErrInvalidVisibility),
		errors.Is(err, ErrNoFiles),
		errors.Is(err, ErrPathTraversal):
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrFileTooLarge),
		errors.Is(err, ErrTotalTooLarge),
		errors.Is(err, ErrTooManyFiles):
		dto.ErrorResponse(c, http.StatusRequestEntityTooLarge, err.Error())
	default:
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
	}
}

