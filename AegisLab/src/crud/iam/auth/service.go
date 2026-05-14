package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/jwtkeys"
	"aegis/platform/model"
	user "aegis/crud/iam/user"
	"aegis/platform/tracing"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"gorm.io/gorm"
)

const (
	accessKeySignatureTTL = 5 * time.Minute
	iamTracerName         = "aegis/iam"
)

type Service struct {
	userRepo   *UserRepository
	roleRepo   *RoleRepository
	apiKeyRepo *APIKeyRepository
	tokenStore *TokenStore
	signer     *jwtkeys.Signer
	verifier   *jwtkeys.Verifier
}

func NewService(userRepo *UserRepository, roleRepo *RoleRepository, apiKeyRepo *APIKeyRepository, tokenStore *TokenStore, signer *jwtkeys.Signer, verifier *jwtkeys.Verifier) *Service {
	return &Service{
		userRepo:   userRepo,
		roleRepo:   roleRepo,
		apiKeyRepo: apiKeyRepo,
		tokenStore: tokenStore,
		signer:     signer,
		verifier:   verifier,
	}
}

func (s *Service) Register(ctx context.Context, req *RegisterReq) (*UserInfo, error) {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/auth/register")
	defer span.End()

	if req == nil {
		return nil, fmt.Errorf("register request is nil")
	}

	var createdUser *model.User
	err := s.userRepo.db.Transaction(func(tx *gorm.DB) error {
		userRepo := NewUserRepository(tx)

		if _, err := userRepo.GetByUsername(req.Username); err == nil {
			return fmt.Errorf("%w: username is already taken", consts.ErrAlreadyExists)
		}

		if _, err := userRepo.GetByEmail(req.Email); err == nil {
			return fmt.Errorf("%w: email is already registered", consts.ErrAlreadyExists)
		}

		user := &model.User{
			Username: req.Username,
			Email:    req.Email,
			Password: req.Password,
			IsActive: true,
			Status:   consts.CommonEnabled,
		}

		if err := userRepo.Create(user); err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}

		createdUser = user
		return nil
	})
	if err != nil {
		return nil, err
	}

	return NewUserInfo(createdUser), nil
}

func (s *Service) Login(ctx context.Context, req *LoginReq) (*LoginResp, error) {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/auth/login")
	defer span.End()

	if req == nil {
		return nil, fmt.Errorf("login request is nil")
	}

	var loginedUser *model.User
	var token string
	var expiresAt time.Time

	err := s.userRepo.db.Transaction(func(tx *gorm.DB) error {
		userRepo := NewUserRepository(tx)
		roleRepo := NewRoleRepository(tx)

		user, err := userRepo.GetByUsername(req.Username)
		if err != nil {
			return fmt.Errorf("%w: invalid username or password", consts.ErrAuthenticationFailed)
		}

		if !crypto.VerifyPassword(req.Password, user.Password) {
			return fmt.Errorf("%w: invalid username or password", consts.ErrAuthenticationFailed)
		}

		// Opportunistically migrate legacy SHA-256 records to bcrypt on a
		// successful login. Failure here is non-fatal — we don't want to
		// prevent the user from logging in if the rehash write fails; the
		// next successful login will try again.
		if crypto.NeedsRehash(user.Password) {
			if newHash, rehashErr := crypto.HashPassword(req.Password); rehashErr == nil {
				user.Password = newHash
				if err := userRepo.Update(user); err != nil {
					logrus.Warnf("failed to rehash legacy password for user %d: %v", user.ID, err)
				}
			} else {
				logrus.Warnf("failed to compute bcrypt hash during login migration for user %d: %v", user.ID, rehashErr)
			}
		}

		token, expiresAt, err = s.generateTokenWithRoles(roleRepo, user)
		if err != nil {
			return err
		}

		if err := userRepo.UpdateLoginTime(user.ID); err != nil {
			logrus.Errorf("failed to update last login time for user %d: %v", user.ID, err)
		}

		loginedUser = user
		return nil
	})
	if err != nil {
		return nil, err
	}

	roles, err := s.roleRepo.ListByUserID(loginedUser.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user role: %w", err)
	}

	if len(roles) == 0 {
		return nil, fmt.Errorf("%w: user has no assigned role", consts.ErrPermissionDenied)
	}

	info := NewUserInfo(loginedUser)
	info.Role = roles[0].Name
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(loginedUser.ID))
	tracing.SetSpanAttribute(ctx, "user.role", info.Role)

	return &LoginResp{
		Token:     token,
		ExpiresAt: expiresAt,
		User:      *info,
	}, nil
}

func (s *Service) RefreshToken(ctx context.Context, req *TokenRefreshReq) (*TokenRefreshResp, error) {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/auth/refresh_token")
	defer span.End()

	if req == nil {
		return nil, fmt.Errorf("token refresh request is nil")
	}

	refreshClaims, err := crypto.ParseToken(req.Token, s.verifier.Resolve)
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}

	user, err := s.userRepo.GetByID(refreshClaims.UserID)
	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(user.ID))

	newToken, expiresAt, err := s.generateTokenWithRoles(s.roleRepo, user)
	if err != nil {
		return nil, err
	}

	return &TokenRefreshResp{
		Token:     newToken,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) Logout(ctx context.Context, claims *crypto.Claims) error {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/auth/logout")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(claims.UserID))

	metaData := map[string]any{
		"user_id": claims.UserID,
		"reason":  "User logout",
	}
	if err := s.tokenStore.AddTokenToBlacklist(ctx, claims.ID, claims.ExpiresAt.Time, metaData); err != nil {
		logrus.Errorf("failed to add token to blacklist: %v", err)
		return fmt.Errorf("failed to blacklist token: %w", err)
	}
	return nil
}

func (s *Service) VerifyToken(ctx context.Context, token string) (*crypto.Claims, error) {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/auth/verify_token")
	defer span.End()

	claims, err := crypto.ParseToken(token, s.verifier.Resolve)
	if err != nil {
		return nil, err
	}
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(claims.UserID))

	if s.tokenStore != nil {
		blacklisted, err := s.tokenStore.IsTokenBlacklisted(ctx, claims.ID)
		if err != nil {
			return nil, err
		}
		if blacklisted {
			return nil, fmt.Errorf("%w: token has been revoked", consts.ErrAuthenticationFailed)
		}

		// Per-user revocation: an admin password-reset bumps revoke:user:<id>
		// to mark every pre-existing token invalid. A token survives only if
		// it was issued AFTER the marker timestamp.
		revokedAt, ok, err := s.tokenStore.UserRevokedSince(ctx, claims.UserID)
		if err != nil {
			return nil, err
		}
		if ok {
			if claims.IssuedAt == nil || !claims.IssuedAt.After(revokedAt) {
				return nil, fmt.Errorf("%w: token has been revoked", consts.ErrAuthenticationFailed)
			}
		}
	}

	return claims, nil
}

func (s *Service) VerifyServiceToken(ctx context.Context, token string) (*crypto.ServiceClaims, error) {
	return crypto.ParseServiceToken(token, s.verifier.Resolve)
}

func (s *Service) ChangePassword(ctx context.Context, req *ChangePasswordReq, userID int) error {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/auth/change_password")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(userID))

	if req == nil {
		return fmt.Errorf("change password request is nil")
	}

	return s.userRepo.db.Transaction(func(tx *gorm.DB) error {
		userRepo := NewUserRepository(tx)

		user, err := userRepo.GetByID(userID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: user not found", consts.ErrNotFound)
			}
			return fmt.Errorf("failed to get user: %w", err)
		}

		if !crypto.VerifyPassword(req.OldPassword, user.Password) {
			return fmt.Errorf("invalid old password")
		}

		hashedPassword, err := crypto.HashPassword(req.NewPassword)
		if err != nil {
			return fmt.Errorf("password hashing failed: %w", err)
		}
		user.Password = hashedPassword

		if err := userRepo.Update(user); err != nil {
			return fmt.Errorf("failed to update password: %w", err)
		}

		return nil
	})
}

func (s *Service) GetProfile(ctx context.Context, userID int) (*UserProfileResp, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: user not found", consts.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	resp := NewUserProfileResp(user)
	userContainers, userDatasets, userProjects, err := s.getAllUserResourceRoles(userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user resource roles: %w", err)
	}

	resp.ContainerRoles = userContainers
	resp.DatasetRoles = userDatasets
	resp.ProjectRoles = userProjects

	return resp, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, userID int, req *CreateAPIKeyReq) (*APIKeyWithSecretResp, error) {
	if req == nil {
		return nil, fmt.Errorf("api key create request is nil")
	}
	normalizedScopes, err := normalizeAPIKeyScopes(req.Scopes)
	if err != nil {
		return nil, err
	}

	accessKeyValue, err := generateCredentialValue("pk_", 16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate api key id: %w", err)
	}
	secretKeyValue, err := generateCredentialValue("ks_", 24)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key secret: %w", err)
	}
	secretHash, err := crypto.HashPassword(secretKeyValue)
	if err != nil {
		return nil, fmt.Errorf("failed to hash key secret: %w", err)
	}
	secretCiphertext, err := crypto.EncryptAPIKeySecret(secretKeyValue)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt key secret: %w", err)
	}

	key := &model.APIKey{
		UserID:              userID,
		Name:                req.Name,
		Description:         req.Description,
		KeyID:               accessKeyValue,
		KeySecretHash:       secretHash,
		KeySecretCiphertext: secretCiphertext,
		Scopes:              normalizedScopes,
		ExpiresAt:           req.ExpiresAt,
		Status:              consts.CommonEnabled,
	}
	if err := s.apiKeyRepo.Create(key); err != nil {
		return nil, err
	}

	resp := &APIKeyWithSecretResp{
		APIKeyInfo: *NewAPIKeyInfo(key),
		KeySecret:  secretKeyValue,
	}
	return resp, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, userID int, req *ListAPIKeyReq) (*ListAPIKeyResp, error) {
	if req == nil {
		return nil, fmt.Errorf("api key list request is nil")
	}

	limit, offset := req.ToGormParams()
	keys, total, err := s.apiKeyRepo.ListByUserID(userID, limit, offset)
	if err != nil {
		return nil, err
	}

	items := make([]APIKeyInfo, 0, len(keys))
	for i := range keys {
		items = append(items, *NewAPIKeyInfo(&keys[i]))
	}

	return &ListAPIKeyResp{
		Items:      items,
		Pagination: *req.ConvertToPaginationInfo(total),
	}, nil
}

func (s *Service) GetAPIKey(ctx context.Context, userID, accessKeyID int) (*APIKeyInfo, error) {
	key, err := s.apiKeyRepo.GetByIDForUser(accessKeyID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: api key not found", consts.ErrNotFound)
		}
		return nil, err
	}
	return NewAPIKeyInfo(key), nil
}

func (s *Service) DeleteAPIKey(ctx context.Context, userID, accessKeyID int) error {
	key, err := s.apiKeyRepo.GetByIDForUser(accessKeyID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: api key not found", consts.ErrNotFound)
		}
		return err
	}

	key.Status = consts.CommonDeleted
	return s.apiKeyRepo.Update(key)
}

func (s *Service) DisableAPIKey(ctx context.Context, userID, accessKeyID int) error {
	return s.setAPIKeyStatus(userID, accessKeyID, consts.CommonDisabled)
}

func (s *Service) EnableAPIKey(ctx context.Context, userID, accessKeyID int) error {
	return s.setAPIKeyStatus(userID, accessKeyID, consts.CommonEnabled)
}

func (s *Service) RevokeAPIKey(ctx context.Context, userID, accessKeyID int) error {
	key, err := s.apiKeyRepo.GetByIDForUser(accessKeyID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: api key not found", consts.ErrNotFound)
		}
		return err
	}
	if key.RevokedAt != nil {
		return nil
	}

	now := time.Now()
	key.RevokedAt = &now
	key.Status = consts.CommonDisabled
	return s.apiKeyRepo.Update(key)
}

func (s *Service) RotateAPIKey(ctx context.Context, userID, accessKeyID int) (*APIKeyWithSecretResp, error) {
	key, err := s.apiKeyRepo.GetByIDForUser(accessKeyID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: api key not found", consts.ErrNotFound)
		}
		return nil, err
	}
	if key.RevokedAt != nil {
		return nil, fmt.Errorf("%w: revoked api key cannot be rotated", consts.ErrBadRequest)
	}

	secretKeyValue, err := generateCredentialValue("ks_", 24)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key secret: %w", err)
	}
	secretHash, err := crypto.HashPassword(secretKeyValue)
	if err != nil {
		return nil, fmt.Errorf("failed to hash key secret: %w", err)
	}
	secretCiphertext, err := crypto.EncryptAPIKeySecret(secretKeyValue)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt key secret: %w", err)
	}

	key.KeySecretHash = secretHash
	key.KeySecretCiphertext = secretCiphertext
	if err := s.apiKeyRepo.Update(key); err != nil {
		return nil, err
	}

	return &APIKeyWithSecretResp{
		APIKeyInfo: *NewAPIKeyInfo(key),
		KeySecret:  secretKeyValue,
	}, nil
}

func (s *Service) ExchangeAPIKeyToken(ctx context.Context, req *APIKeyTokenReq, method, path string) (*APIKeyTokenResp, error) {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/auth/exchange_api_key_token")
	defer span.End()

	if req == nil {
		return nil, fmt.Errorf("api key token request is nil")
	}

	key, err := s.apiKeyRepo.GetByKeyID(req.KeyID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: invalid key id or key secret", consts.ErrAuthenticationFailed)
		}
		return nil, err
	}

	if key.Status != consts.CommonEnabled {
		return nil, fmt.Errorf("%w: api key is disabled", consts.ErrAuthenticationFailed)
	}
	if key.RevokedAt != nil {
		return nil, fmt.Errorf("%w: api key is revoked", consts.ErrAuthenticationFailed)
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("%w: api key is expired", consts.ErrAuthenticationFailed)
	}
	timestampUnix, err := req.TimestampUnix()
	if err != nil {
		return nil, fmt.Errorf("%w: invalid request timestamp", consts.ErrAuthenticationFailed)
	}
	now := time.Now()
	requestTime := time.Unix(timestampUnix, 0)
	if requestTime.Before(now.Add(-accessKeySignatureTTL)) || requestTime.After(now.Add(accessKeySignatureTTL)) {
		return nil, fmt.Errorf("%w: request timestamp is outside the allowed window", consts.ErrAuthenticationFailed)
	}

	secretKey, err := crypto.DecryptAPIKeySecret(key.KeySecretCiphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt api key secret: %w", err)
	}
	if !crypto.VerifyAPIKeyRequestSignature(secretKey, req.CanonicalString(method, path), req.Signature) {
		return nil, fmt.Errorf("%w: invalid api key signature", consts.ErrAuthenticationFailed)
	}
	if err := s.tokenStore.ReserveAPIKeyNonce(ctx, key.KeyID, req.Nonce, accessKeySignatureTTL); err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(key.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: api key owner not found", consts.ErrAuthenticationFailed)
		}
		return nil, err
	}
	if !user.IsActive || user.Status != consts.CommonEnabled {
		return nil, fmt.Errorf("%w: api key owner is inactive", consts.ErrAuthenticationFailed)
	}
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(user.ID))

	token, expiresAt, err := s.generateAPIKeyTokenWithRoles(s.roleRepo, user, key.ID, key.Scopes)
	if err != nil {
		return nil, err
	}

	if err := s.apiKeyRepo.UpdateLastUsedAt(key.ID, time.Now()); err != nil {
		logrus.WithError(err).Warn("failed to update api key last used time")
	}

	return &APIKeyTokenResp{
		Token:     token,
		TokenType: consts.TokenTypeBearer,
		ExpiresAt: expiresAt,
		AuthType:  "api_key",
		KeyID:     key.KeyID,
	}, nil
}

func (s *Service) generateTokenWithRoles(roleRepo *RoleRepository, user *model.User) (string, time.Time, error) {
	roles, err := roleRepo.ListByUserID(user.ID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to get user roles: %w", err)
	}

	isAdmin := false
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		roleNames = append(roleNames, role.Name)
		if role.Name == string(consts.RoleSuperAdmin) || role.Name == string(consts.RoleAdmin) {
			isAdmin = true
		}
	}

	token, expiresAt, err := crypto.GenerateToken(user.ID, user.Username, user.Email, user.IsActive, isAdmin, roleNames, s.signer.PrivateKey, s.signer.Kid)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate token: %w", err)
	}

	return token, expiresAt, nil
}

func (s *Service) generateAPIKeyTokenWithRoles(roleRepo *RoleRepository, user *model.User, apiKeyID int, apiKeyScopes []string) (string, time.Time, error) {
	roles, err := roleRepo.ListByUserID(user.ID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to get user roles: %w", err)
	}

	isAdmin := false
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		roleNames = append(roleNames, role.Name)
		if role.Name == string(consts.RoleSuperAdmin) || role.Name == string(consts.RoleAdmin) {
			isAdmin = true
		}
	}

	token, expiresAt, err := crypto.GenerateAPIKeyToken(user.ID, user.Username, user.Email, user.IsActive, isAdmin, roleNames, apiKeyID, apiKeyScopes, s.signer.PrivateKey, s.signer.Kid)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate api key token: %w", err)
	}

	return token, expiresAt, nil
}

func (s *Service) getAllUserResourceRoles(userID int) ([]user.UserContainerInfo, []user.UserDatasetInfo, []user.UserProjectInfo, error) {
	userContainers, err := s.userRepo.ListContainerRoles(userID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to list user-container roles: %w", err)
	}
	containerRoles := make([]user.UserContainerInfo, 0, len(userContainers))
	for _, uc := range userContainers {
		containerRoles = append(containerRoles, *user.NewUserContainerInfo(&uc))
	}

	userDatasets, err := s.userRepo.ListDatasetRoles(userID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to list user-dataset roles: %w", err)
	}
	datasetRoles := make([]user.UserDatasetInfo, 0, len(userDatasets))
	for _, ud := range userDatasets {
		datasetRoles = append(datasetRoles, *user.NewUserDatasetInfo(&ud))
	}

	userProjects, err := s.userRepo.ListProjectRoles(userID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to list user-project roles: %w", err)
	}
	projectRoles := make([]user.UserProjectInfo, 0, len(userProjects))
	for _, up := range userProjects {
		projectRoles = append(projectRoles, *user.NewUserProjectInfo(&up))
	}

	return containerRoles, datasetRoles, projectRoles, nil
}

func (s *Service) setAPIKeyStatus(userID, accessKeyID int, status consts.StatusType) error {
	key, err := s.apiKeyRepo.GetByIDForUser(accessKeyID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: api key not found", consts.ErrNotFound)
		}
		return err
	}
	if status == consts.CommonEnabled && key.RevokedAt != nil {
		return fmt.Errorf("%w: revoked api key cannot be re-enabled", consts.ErrBadRequest)
	}

	key.Status = status
	return s.apiKeyRepo.Update(key)
}

func generateCredentialValue(prefix string, randomBytes int) (string, error) {
	buf := make([]byte, randomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}
