package sso

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/dto"
	"aegis/platform/httpx"
	"aegis/platform/jwtkeys"
	"aegis/platform/middleware"
	"aegis/platform/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterServiceAccountRoutes mounts the /v1/service-accounts/* admin
// endpoints on the SSO process's gin engine. Auth flows through JWTAuth +
// the per-handler requireAdminOrService gate.
func RegisterServiceAccountRoutes(engine *gin.Engine, h *ServiceAccountHandler) {
	v1 := engine.Group("/v1", middleware.JWTAuth())
	{
		v1.POST("/service-accounts/:name/issue", h.Issue)
		v1.POST("/service-accounts/:name/revoke", h.Revoke)
	}
}

const (
	defaultSALifetimeDays = 365
	maxSALifetimeDays     = 365 * 5
)

// ServiceAccountRepository owns CRUD on model.ServiceAccount. Pure SQL — the
// signer / token format lives in ServiceAccountService.
type ServiceAccountRepository struct {
	db *gorm.DB
}

func NewServiceAccountRepository(db *gorm.DB) *ServiceAccountRepository {
	return &ServiceAccountRepository{db: db}
}

func (r *ServiceAccountRepository) GetByName(ctx context.Context, name string) (*model.ServiceAccount, error) {
	var sa model.ServiceAccount
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&sa).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, consts.ErrNotFound
		}
		return nil, err
	}
	return &sa, nil
}

func (r *ServiceAccountRepository) Revoke(ctx context.Context, name string, at time.Time) error {
	res := r.db.WithContext(ctx).Model(&model.ServiceAccount{}).
		Where("name = ? AND revoked_at IS NULL", name).
		Update("revoked_at", at)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Distinguish "not found" from "already revoked" with a second query
		// so the API can surface 404 vs 409.
		existing, err := r.GetByName(ctx, name)
		if err != nil {
			return err
		}
		if existing.RevokedAt != nil {
			return fmt.Errorf("%w: service account %s already revoked", consts.ErrConflict, name)
		}
		return consts.ErrNotFound
	}
	return nil
}

// ServiceAccountService mints tokens for non-revoked service accounts using
// the SSO signing key.
type ServiceAccountService struct {
	repo   *ServiceAccountRepository
	signer *jwtkeys.Signer
}

func NewServiceAccountService(repo *ServiceAccountRepository, signer *jwtkeys.Signer) *ServiceAccountService {
	return &ServiceAccountService{repo: repo, signer: signer}
}

type IssueServiceAccountTokenResp struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func parseScopes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *ServiceAccountService) Issue(ctx context.Context, name string, lifetimeDays int) (*IssueServiceAccountTokenResp, error) {
	if lifetimeDays <= 0 {
		lifetimeDays = defaultSALifetimeDays
	}
	if lifetimeDays > maxSALifetimeDays {
		return nil, fmt.Errorf("%w: lifetime_days must be <= %d", consts.ErrBadRequest, maxSALifetimeDays)
	}
	sa, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if sa.RevokedAt != nil {
		return nil, fmt.Errorf("%w: service account %s is revoked", consts.ErrConflict, name)
	}
	lifetime := time.Duration(lifetimeDays) * 24 * time.Hour
	tok, exp, err := crypto.GenerateServiceAccountToken(sa.Name, parseScopes(sa.Scopes), lifetime, s.signer.PrivateKey, s.signer.Kid)
	if err != nil {
		return nil, err
	}
	return &IssueServiceAccountTokenResp{Token: tok, ExpiresAt: exp}, nil
}

func (s *ServiceAccountService) Revoke(ctx context.Context, name string) error {
	return s.repo.Revoke(ctx, name, time.Now())
}

// ServiceAccountHandler exposes /v1/service-accounts/{name}/* admin endpoints.
type ServiceAccountHandler struct {
	service *ServiceAccountService
}

func NewServiceAccountHandler(service *ServiceAccountService) *ServiceAccountHandler {
	return &ServiceAccountHandler{service: service}
}

type IssueServiceAccountTokenReq struct {
	LifetimeDays int `json:"lifetime_days"`
}

func (h *ServiceAccountHandler) Issue(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	name := c.Param("name")
	if name == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "service account name is required")
		return
	}
	var req IssueServiceAccountTokenReq
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
			return
		}
	}
	resp, err := h.service.Issue(c.Request.Context(), name, req.LifetimeDays)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

func (h *ServiceAccountHandler) Revoke(c *gin.Context) {
	if !requireAdminOrService(c) {
		return
	}
	name := c.Param("name")
	if name == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "service account name is required")
		return
	}
	if err := h.service.Revoke(c.Request.Context(), name); err != nil {
		if httpx.HandleServiceError(c, err) {
			return
		}
	}
	c.Status(http.StatusNoContent)
}
