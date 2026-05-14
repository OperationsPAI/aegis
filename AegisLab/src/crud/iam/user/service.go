package user

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/dto"
	"aegis/platform/model"
	rbac "aegis/crud/iam/rbac"
	"aegis/platform/tracing"

	"go.opentelemetry.io/otel"
	"gorm.io/gorm"
)

const iamTracerName = "aegis/iam"

// SessionRevoker invalidates every still-live access token owned by a user.
// Implemented by *auth.TokenStore and bound to the user service via fx so
// admin password resets can kick existing sessions. Optional — when nil the
// service falls back to "no session revocation" (e.g. unit tests).
type SessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID int) error
}

type Service struct {
	repo    *Repository
	revoker SessionRevoker
}

func NewService(repo *Repository, revoker SessionRevoker) *Service {
	return &Service{repo: repo, revoker: revoker}
}

func (s *Service) GetByID(_ context.Context, userID int) (*model.User, error) {
	return s.repo.getUserByID(userID)
}

func (s *Service) GetByUsername(_ context.Context, username string) (*model.User, error) {
	return s.repo.getUserByUsername(username)
}

func (s *Service) GetByEmail(_ context.Context, email string) (*model.User, error) {
	return s.repo.getUserByEmail(email)
}

func (s *Service) GetByIDs(_ context.Context, ids []int) ([]*model.User, error) {
	return s.repo.GetByIDs(ids)
}

// ListRoleNames returns the active role names granted to a user.
func (s *Service) ListRoleNames(_ context.Context, userID int) ([]string, error) {
	return s.repo.ListRoleNames(userID)
}

func (s *Service) CreateUser(ctx context.Context, req *CreateUserReq) (*UserResp, error) {
	_, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/create")
	defer span.End()
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	user := &model.User{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
		FullName: req.FullName,
		Phone:    req.Phone,
		Avatar:   req.Avatar,
		Status:   consts.CommonEnabled,
		IsActive: true,
	}

	if err := s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		return repo.createUserIfUnique(user)
	}); err != nil {
		return nil, err
	}

	return NewUserResp(user), nil
}

func (s *Service) DeleteUser(ctx context.Context, userID int) error {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/delete")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(userID))
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.ensureActiveRecordExists(&model.User{}, userID, "user"); err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				return fmt.Errorf("%w: user not found", consts.ErrNotFound)
			}
			return fmt.Errorf("failed to get user: %w", err)
		}

		rows, err := repo.deleteUserCascade(userID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return fmt.Errorf("%w: user id %d not found", consts.ErrNotFound, userID)
		}
		return nil
	})
}

func (s *Service) GetUserDetail(ctx context.Context, userID int) (*UserDetailResp, error) {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/get_detail")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(userID))
	user, err := s.repo.getUserDetailBase(userID)
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("%w: user with ID %d not found", consts.ErrNotFound, userID)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	resp := NewUserDetailResp(user)

	globalRoles, permissions, userContainers, userDatasets, userProjects, err := s.repo.loadUserDetailRelations(user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user detail relations: %w", err)
	}
	resp.GlobalRoles = make([]rbac.RoleResp, len(globalRoles))
	for i, role := range globalRoles {
		resp.GlobalRoles[i] = *rbac.NewRoleResp(&role)
	}

	resp.Permissions = make([]rbac.PermissionResp, len(permissions))
	for i, permission := range permissions {
		resp.Permissions[i] = *rbac.NewPermissionResp(&permission)
	}

	containerRoles, datasetRoles, projectRoles := buildUserResourceRoles(userContainers, userDatasets, userProjects)
	resp.ContainerRoles = containerRoles
	resp.DatasetRoles = datasetRoles
	resp.ProjectRoles = projectRoles

	return resp, nil
}

func (s *Service) ListUsers(ctx context.Context, req *ListUserReq) (*dto.ListResp[UserResp], error) {
	return s.ListUsersScoped(ctx, req, nil)
}

// ListUsersScoped is ListUsers restricted to users with grants in any of
// viewScopes services (used by SSO delegated service admins, Task #13).
// Empty viewScopes is equivalent to ListUsers.
func (s *Service) ListUsersScoped(ctx context.Context, req *ListUserReq, viewScopes []string) (*dto.ListResp[UserResp], error) {
	_, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/list")
	defer span.End()
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	limit, offset := req.ToGormParams()
	users, total, err := s.repo.ListUserViewsScoped(limit, offset, req.IsActive, req.Status, viewScopes)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	items := make([]UserResp, len(users))
	for i, user := range users {
		items[i] = *NewUserResp(&user)
	}

	return &dto.ListResp[UserResp]{
		Items:      items,
		Pagination: req.ConvertToPaginationInfo(total),
	}, nil
}

func (s *Service) UpdateUser(ctx context.Context, req *UpdateUserReq, userID int) (*UserResp, error) {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/update")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(userID))
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	var updatedUser *model.User
	err := s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		user, err := repo.updateMutableUser(userID, func(existingUser *model.User) {
			req.PatchUserModel(existingUser)
		})
		if err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				return fmt.Errorf("%w: user not found", consts.ErrNotFound)
			}
			return fmt.Errorf("failed to get user: %w", err)
		}

		updatedUser = user
		return nil
	})
	if err != nil {
		return nil, err
	}

	return NewUserResp(updatedUser), nil
}

// ResetPassword rewrites the target user's password hash and, when a session
// revoker is wired, invalidates every still-live token owned by that user so
// the new password is the only way back in.
func (s *Service) ResetPassword(ctx context.Context, targetUserID int, newPassword string) (*ResetPasswordResp, error) {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/reset_password")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(targetUserID))

	if targetUserID <= 0 {
		return nil, fmt.Errorf("%w: invalid user id", consts.ErrBadRequest)
	}
	if strength, suggestions, err := crypto.ValidatePasswordStrength(newPassword); err != nil {
		return nil, fmt.Errorf("%w: %v", consts.ErrBadRequest, err)
	} else if strength == crypto.WeakPassword {
		msg := "password is too weak"
		if len(suggestions) > 0 {
			msg = msg + ": " + suggestions[0]
		}
		return nil, fmt.Errorf("%w: %s", consts.ErrBadRequest, msg)
	}

	hashed, err := crypto.HashPassword(newPassword)
	if err != nil {
		return nil, fmt.Errorf("password hashing failed: %w", err)
	}

	var updatedUser *model.User
	if err := s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		u, err := repo.updateMutableUser(targetUserID, func(existing *model.User) {
			existing.Password = hashed
		})
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: user not found", consts.ErrNotFound)
			}
			return fmt.Errorf("failed to update password: %w", err)
		}
		updatedUser = u
		return nil
	}); err != nil {
		return nil, err
	}

	sessionsRevoked := false
	if s.revoker != nil {
		if revErr := s.revoker.RevokeAllForUser(ctx, targetUserID); revErr != nil {
			// Password is already rewritten — surface the revocation failure
			// rather than silently leave stale sessions live.
			return nil, fmt.Errorf("password updated but session revocation failed: %w", revErr)
		}
		sessionsRevoked = true
	}

	return &ResetPasswordResp{
		UserID:            updatedUser.ID,
		Username:          updatedUser.Username,
		SessionsRevoked:   sessionsRevoked,
		PasswordUpdatedAt: updatedUser.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

func (s *Service) AssignRole(ctx context.Context, userID, roleID int) error {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/assign_role")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(userID))
	tracing.SetSpanAttribute(ctx, "role.id", strconv.Itoa(roleID))
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.assignGlobalRole(userID, roleID); err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				if userErr := repo.ensureActiveRecordExists(&model.User{}, userID, "user"); userErr != nil {
					return fmt.Errorf("%w: user not found", consts.ErrNotFound)
				}
				return fmt.Errorf("%w: role not found", consts.ErrNotFound)
			}
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: user already has this role", consts.ErrAlreadyExists)
			}
			return err
		}
		return nil
	})
}

func (s *Service) RemoveRole(ctx context.Context, userID, roleID int) error {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/remove_role")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(userID))
	tracing.SetSpanAttribute(ctx, "role.id", strconv.Itoa(roleID))
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.removeGlobalRole(userID, roleID); err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				if userErr := repo.ensureActiveRecordExists(&model.User{}, userID, "user"); userErr != nil {
					return fmt.Errorf("%w: user not found", consts.ErrNotFound)
				}
				return fmt.Errorf("%w: role not found", consts.ErrNotFound)
			}
			return err
		}
		return nil
	})
}

func (s *Service) AssignPermissions(ctx context.Context, req *AssignUserPermissionReq, userID int) error {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/assign_permissions")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(userID))
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		userPermissions, err := repo.buildUserPermissions(userID, req.Items)
		if err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				return fmt.Errorf("%w: failed to resolve permission assignment targets", consts.ErrNotFound)
			}
			return err
		}

		if err := repo.batchCreateUserPermissions(userPermissions); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: user already has one or more of these permissions", consts.ErrAlreadyExists)
			}
			return fmt.Errorf("failed to assign permissions to user: %w", err)
		}
		return nil
	})
}

func (s *Service) RemovePermissions(ctx context.Context, req *RemoveUserPermissionReq, userID int) error {
	ctx, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/remove_permissions")
	defer span.End()
	tracing.SetSpanAttribute(ctx, "user.id", strconv.Itoa(userID))
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.batchDeleteUserPermissions(userID, req.PermissionIDs); err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				return fmt.Errorf("%w: failed to resolve user or permissions", consts.ErrNotFound)
			}
			return fmt.Errorf("failed to remove permissions from user: %w", err)
		}
		return nil
	})
}

func (s *Service) AssignContainer(ctx context.Context, userID, containerID, roleID int) error {
	_, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/assign_container")
	defer span.End()
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.assignContainerRole(userID, containerID, roleID); err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				return fmt.Errorf("%w: user/container/role not found", consts.ErrNotFound)
			}
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: user already assigned to this container", consts.ErrAlreadyExists)
			}
			return err
		}
		return nil
	})
}

func (s *Service) RemoveContainer(ctx context.Context, userID, containerID int) error {
	_, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/remove_container")
	defer span.End()
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		rows, err := repo.removeContainerRole(userID, containerID)
		if err != nil {
			return fmt.Errorf("failed to remove user from container: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: user is not assigned to this container", consts.ErrNotFound)
		}
		return nil
	})
}

func (s *Service) AssignDataset(ctx context.Context, userID, datasetID, roleID int) error {
	_, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/assign_dataset")
	defer span.End()
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.assignDatasetRole(userID, datasetID, roleID); err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				return fmt.Errorf("%w: user/dataset/role not found", consts.ErrNotFound)
			}
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: user already assigned to this dataset", consts.ErrAlreadyExists)
			}
			return err
		}
		return nil
	})
}

func (s *Service) RemoveDataset(ctx context.Context, userID, datasetID int) error {
	_, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/remove_dataset")
	defer span.End()
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		rows, err := repo.removeDatasetRole(userID, datasetID)
		if err != nil {
			return fmt.Errorf("failed to remove user from dataset: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: user is not assigned to this dataset", consts.ErrNotFound)
		}
		return nil
	})
}

func (s *Service) AssignProject(ctx context.Context, userID, projectID, roleID int) error {
	_, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/assign_project")
	defer span.End()
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		if err := repo.assignProjectRole(userID, projectID, roleID); err != nil {
			if errors.Is(err, consts.ErrNotFound) {
				return fmt.Errorf("%w: user/project/role not found", consts.ErrNotFound)
			}
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: user already assigned to this project", consts.ErrAlreadyExists)
			}
			return err
		}
		return nil
	})
}

func (s *Service) RemoveProject(ctx context.Context, userID, projectID int) error {
	_, span := otel.Tracer(iamTracerName).Start(ctx, "iam/user/remove_project")
	defer span.End()
	return s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)
		rows, err := repo.removeProjectRole(userID, projectID)
		if err != nil {
			return fmt.Errorf("failed to remove user from project: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: user is not assigned to this project", consts.ErrNotFound)
		}
		return nil
	})
}

func buildUserResourceRoles(userContainers []model.UserScopedRole, userDatasets []model.UserScopedRole, userProjects []model.UserScopedRole) ([]UserContainerInfo, []UserDatasetInfo, []UserProjectInfo) {
	containerRoles := make([]UserContainerInfo, 0, len(userContainers))
	for _, uc := range userContainers {
		containerRoles = append(containerRoles, *NewUserContainerInfo(&uc))
	}

	datasetRoles := make([]UserDatasetInfo, 0, len(userDatasets))
	for _, ud := range userDatasets {
		datasetRoles = append(datasetRoles, *NewUserDatasetInfo(&ud))
	}

	projectRoles := make([]UserProjectInfo, 0, len(userProjects))
	for _, up := range userProjects {
		projectRoles = append(projectRoles, *NewUserProjectInfo(&up))
	}

	return containerRoles, datasetRoles, projectRoles
}
