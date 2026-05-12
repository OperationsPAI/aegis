package initialization

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"aegis/platform/config"
	"aegis/platform/consts"
	etcd "aegis/platform/etcd"
	redis "aegis/platform/redis"
	"aegis/platform/model"
	container "aegis/module/container"
	dataset "aegis/module/dataset"
	label "aegis/module/label"
	"aegis/service/common"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
)

type permMeta struct {
	action        consts.ActionName
	resourceID    int
	resourceName  consts.ResourceName
	resourceScope consts.ResourceScope
}

func (r permMeta) String() string {
	return fmt.Sprintf("%v %v %v", r.action, r.resourceScope, r.resourceName)
}

func InitializeProducer(db *gorm.DB, publisher *redis.Gateway, etcdGw *etcd.Gateway, listener *common.ConfigUpdateListener) error {
	initialData, err := loadInitialDataFromConfiguredPath()
	if err != nil {
		return fmt.Errorf("failed to load producer initial data: %w", err)
	}

	shouldSeed, err := producerSeedRequired(db, initialData)
	if err != nil {
		return fmt.Errorf("failed to determine producer bootstrap state: %w", err)
	}

	if shouldSeed {
		logrus.Info("Seeding initial system data for producer...")
		if err := initializeProducer(db, initialData); err != nil {
			return fmt.Errorf("failed to initialize system data for producer: %w", err)
		}
		logrus.Info("Successfully seeded initial system data for producer")
	} else {
		logrus.Info("Initial system data for producer already seeded, skipping initialization")
	}

	// Issue #104: even after first-boot seeding, a new permission added to
	// consts.SystemRolePermissions (or contributed via a module's RoleGrants
	// registrar and merged by rbac.AggregatePermissions) must reach the
	// permissions + role_permissions tables. Running ReconcileSystemPermissions
	// unconditionally is the canonical place — it is idempotent and only
	// writes rows that are missing.
	if err := ReconcileSystemPermissions(db); err != nil {
		return fmt.Errorf("failed to reconcile system permissions: %w", err)
	}

	// Best-effort one-shot migration for issue #75:
	//   1. Drain the retired systems table into dynamic_configs + etcd Global
	//   2. Rewrite any Consumer-scoped `injection.system.*` rows to Global
	//   3. Move any stale etcd keys from the Consumer prefix to Global
	// Safe on fresh installs and on already-migrated installs.
	if err := MigrateLegacyInjectionSystem(context.Background(), db, etcdGw); err != nil {
		logrus.WithError(err).Warn("Legacy injection.system migration failed")
	}

	// Activate config listener first so Viper is populated from etcd before
	// InitializeSystems reads it to drive chaos.RegisterSystem.
	// injection.system.* is Global-scoped (issue #75 follow-up), so both
	// producer and consumer pick it up through the standard Global listener.
	common.RegisterGlobalHandlers(publisher)
	if err := activateConfigScope(consts.ConfigScopeProducer, listener); err != nil {
		return err
	}

	// Initialize systems (register with chaos-experiment from etcd, set MetadataStore)
	if err := InitializeSystems(db); err != nil {
		return fmt.Errorf("failed to initialize systems: %w", err)
	}

	return nil
}

func loadInitialDataFromConfiguredPath() (*InitialData, error) {
	dataPath := config.GetString("initialization.data_path")
	filePath := filepath.Join(dataPath, consts.InitialFilename)
	return loadInitialDataFromFile(filePath)
}

func producerSeedRequired(db *gorm.DB, initialData *InitialData) (bool, error) {
	if initialData == nil {
		return false, fmt.Errorf("initial data is nil")
	}
	adminUsername := initialData.AdminUser.Username
	if adminUsername == "" {
		return false, fmt.Errorf("initial data admin user username is empty")
	}

	_, err := newBootstrapStore(db).getUserByUsername(adminUsername)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, consts.ErrNotFound) {
		return true, nil
	}
	return false, err
}

func initializeProducer(db *gorm.DB, initialData *InitialData) error {
	resources := systemResources()

	return withOptimizedDBSettings(db, func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			txStore := newBootstrapStore(tx)

			// Reconcile the RBAC baseline (resources + permissions + roles +
			// role_permissions) via the shared idempotent path so first-boot
			// seeding and every-boot upserts write identical rows. See
			// reconcileSystemPermissionsTx for the idempotency contract.
			if err := reconcileSystemPermissionsTx(txStore, resources); err != nil {
				return fmt.Errorf("failed to reconcile RBAC baseline: %w", err)
			}

			adminUser, err := initializeAdminUser(txStore, initialData)
			if err != nil {
				return fmt.Errorf("failed to initialize admin user: %w", err)
			}

			if err := initializeProjectsAndTeams(txStore, initialData); err != nil {
				return fmt.Errorf("failed to initialize admin user, projects and teams: %w", err)
			}

			if err := initializeUsers(txStore, initialData); err != nil {
				return fmt.Errorf("failed to initialize users: %w", err)
			}

			if err := initializeContainers(tx, initialData, adminUser.ID); err != nil {
				return fmt.Errorf("failed to initialize containers: %w", err)
			}

			if err := initializeDatasets(tx, initialData, adminUser.ID); err != nil {
				return fmt.Errorf("failed to initialize datasets: %w", err)
			}

			if err := initializeExecutionLabels(tx); err != nil {
				return fmt.Errorf("failed to initialize execution labels: %w", err)
			}

			return nil
		})
	})
}

func initializeAdminUser(store *bootstrapStore, data *InitialData) (*model.User, error) {
	adminUser := data.AdminUser.ConvertToDBUser()
	if err := store.createUser(adminUser); err != nil {
		if errors.Is(err, consts.ErrAlreadyExists) {
			return nil, fmt.Errorf("admin user already exists")
		}
		return nil, fmt.Errorf("failed to create admin user: %w", err)
	}

	superAdminRole, err := store.getRoleByName("super_admin")
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return nil, fmt.Errorf("super_admin role not found, ensure system roles are initialized first")
		}
		return nil, fmt.Errorf("failed to get super_admin role: %w", err)
	}

	userRole := model.UserRole{
		UserID: adminUser.ID,
		RoleID: superAdminRole.ID,
	}
	if err := store.createUserRole(&userRole); err != nil {
		if errors.Is(err, consts.ErrAlreadyExists) {
			return nil, fmt.Errorf("admin user already has super_admin role")
		}
		return nil, fmt.Errorf("failed to assign super_admin role to admin user: %w", err)
	}

	return adminUser, nil
}

func initializeProjectsAndTeams(store *bootstrapStore, data *InitialData) error {
	for _, teamData := range data.Teams {
		team := teamData.ConvertToDBTeam()
		if err := store.createTeam(team); err != nil {
			if errors.Is(err, consts.ErrAlreadyExists) {
				return fmt.Errorf("team %s already exists", team.Name)
			}
			return fmt.Errorf("failed to create team %s: %w", team.Name, err)
		}
	}

	for _, projectData := range data.Projects {
		project := projectData.ConvertToDBProject()
		if err := store.createProject(project); err != nil {
			if errors.Is(err, consts.ErrAlreadyExists) {
				return fmt.Errorf("project %s already exists", project.Name)
			}
			return fmt.Errorf("failed to create project %s: %w", project.Name, err)
		}
	}

	return nil
}

func initializeContainers(tx *gorm.DB, data *InitialData, userID int) error {
	dataPath := config.GetString("initialization.data_path")

	for _, containerData := range data.Containers {
		containerModel := containerData.ConvertToDBContainer()
		if containerModel.Type == consts.ContainerTypePedestal {
			system := chaos.SystemType(containerModel.Name)
			if !system.IsValid() {
				return fmt.Errorf("invalid pedestal name: %s", containerModel.Name)
			}
		}

		versions := make([]model.ContainerVersion, 0, len(containerData.Versions))
		for _, versionData := range containerData.Versions {
			version := versionData.ConvertToDBContainerVersion()

			if len(versionData.EnvVars) > 0 {
				params := make([]model.ParameterConfig, 0, len(versionData.EnvVars))
				for _, paramData := range versionData.EnvVars {
					param := paramData.ConvertToDBParameterConfig()
					params = append(params, *param)
				}
				version.EnvVars = params
			}

			if versionData.HelmConfig != nil {
				helmConfig := versionData.HelmConfig.ConvertToDBHelmConfig()
				if len(versionData.HelmConfig.Values) > 0 {
					params := make([]model.ParameterConfig, 0, len(versionData.HelmConfig.Values))
					for _, paramData := range versionData.HelmConfig.Values {
						param := paramData.ConvertToDBParameterConfig()
						params = append(params, *param)
					}
					helmConfig.DynamicValues = params
				}

				version.HelmConfig = helmConfig
			}

			versions = append(versions, *version)
		}

		containerModel.Versions = versions

		createdContainer, err := container.NewRepository(tx).CreateContainerCore(containerModel, userID)
		if err != nil {
			return fmt.Errorf("failed to create container %s: %w", containerData.Name, err)
		}

		if createdContainer.Type == consts.ContainerTypePedestal {
			valuesPath := filepath.Join(dataPath, fmt.Sprintf("%s.yaml", createdContainer.Name))
			if _, statErr := os.Stat(valuesPath); errors.Is(statErr, os.ErrNotExist) {
				// Values file is optional at seed time; operators can push values later via
				// `aegisctl pedestal chart push` / helm values in etcd dynamic config.
				continue
			}
			if err := container.NewRepository(tx).UploadHelmValueFileFromPath(
				containerData.Name,
				containerModel.Versions[0].HelmConfig,
				valuesPath,
			); err != nil {
				return fmt.Errorf("failed to upload helm value file for container %s: %w", containerData.Name, err)
			}
		}
	}

	return nil
}

func initializeDatasets(tx *gorm.DB, data *InitialData, userID int) error {
	for _, datasetData := range data.Datasets {
		datasetModel := datasetData.ConvertToDBDataset()

		versions := make([]model.DatasetVersion, 0, len(datasetData.Versions))
		for _, versionData := range datasetData.Versions {
			version := versionData.ConvertToDBDatasetVersion()
			versions = append(versions, *version)
		}

		_, err := dataset.NewRepository(tx).CreateDatasetCore(datasetModel, versions, userID)
		if err != nil {
			return fmt.Errorf("failed to create dataset %s: %w", datasetData.Name, err)
		}
	}

	return nil
}

func initializeExecutionLabels(tx *gorm.DB) error {
	sourceLabels := []struct {
		value       string
		description string
	}{
		{consts.ExecutionSourceManual, consts.ExecutionManualDescription},
		{consts.ExecutionSourceSystem, consts.ExecutionSystemDescription},
	}

	for _, labelInfo := range sourceLabels {
		_, err := label.NewRepository(tx).CreateLabelCore(tx, &model.Label{
			Key:         consts.ExecutionLabelSource,
			Value:       labelInfo.value,
			Category:    consts.ExecutionCategory,
			Description: labelInfo.description,
		})
		if err != nil {
			return fmt.Errorf("failed to initialize execution label %s=%s: %w",
				consts.ExecutionLabelSource, labelInfo.value, err)
		}
	}

	return nil
}

func initializeUsers(store *bootstrapStore, data *InitialData) error {
	if len(data.Users) == 0 {
		return nil
	}

	role, err := store.getRoleByName(consts.RoleUser.String())
	if err != nil {
		if errors.Is(err, consts.ErrNotFound) {
			return fmt.Errorf("user role not found, ensure system roles are initialized first")
		}
		return fmt.Errorf("failed to get user role: %w", err)
	}

	for _, userData := range data.Users {
		user := userData.ConvertToDBUser()

		if err := store.createUser(user); err != nil {
			if errors.Is(err, consts.ErrAlreadyExists) {
				logrus.Warnf("User %s already exists, skipping", user.Username)
				continue
			}
			return fmt.Errorf("failed to create user %s: %w", user.Username, err)
		}

		if err := store.createUserRole(&model.UserRole{
			UserID: user.ID,
			RoleID: role.ID,
		}); err != nil {
			return fmt.Errorf("failed to assign default role to user %s: %w", user.Username, err)
		}

		// Bind user to specified teams with their roles
		if len(userData.Teams) > 0 {
			for _, teamBinding := range userData.Teams {
				// Get team by name
				team, err := store.getTeamByName(teamBinding.Name)
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return fmt.Errorf("team %s not found for user %s", teamBinding.Name, user.Username)
					}
					return fmt.Errorf("failed to get team %s: %w", teamBinding.Name, err)
				}

				// Get role by name for user-team binding
				teamRole, err := store.getRoleByName(teamBinding.Role)
				if err != nil {
					if errors.Is(err, consts.ErrNotFound) {
						return fmt.Errorf("role %s not found for user %s in team %s", teamBinding.Role, user.Username, teamBinding.Name)
					}
					return fmt.Errorf("failed to get role %s: %w", teamBinding.Role, err)
				}

				// Bind user to team with role
				if err := store.createUserTeam(&model.UserScopedRole{
					UserID:    user.ID,
					RoleID:    teamRole.ID,
					ScopeType: consts.ScopeTypeTeam,
					ScopeID:   fmt.Sprintf("%d", team.ID),
					Status:    consts.CommonEnabled,
				}); err != nil {
					return fmt.Errorf("failed to bind user %s to team %s with role %s: %w", user.Username, teamBinding.Name, teamBinding.Role, err)
				}

				// Bind projects to this team and user if specified
				if len(teamBinding.Projects) > 0 {
					for _, projectBinding := range teamBinding.Projects {
						project, err := store.getProjectByName(projectBinding.Name)
						if err != nil {
							if errors.Is(err, gorm.ErrRecordNotFound) {
								return fmt.Errorf("project %s not found for team %s", projectBinding.Name, teamBinding.Name)
							}
							return fmt.Errorf("failed to get project %s: %w", projectBinding.Name, err)
						}

						// Update project's team_id to bind project to team
						project.TeamID = &team.ID
						if err := store.saveProject(project); err != nil {
							return fmt.Errorf("failed to bind project %s to team %s: %w", projectBinding.Name, teamBinding.Name, err)
						}

						logrus.Infof("Bound project %s to team %s", projectBinding.Name, teamBinding.Name)

						// Get role for user-project binding
						projectRole, err := store.getRoleByName(projectBinding.Role)
						if err != nil {
							if errors.Is(err, consts.ErrNotFound) {
								return fmt.Errorf("role %s not found for user %s in project %s", projectBinding.Role, user.Username, projectBinding.Name)
							}
							return fmt.Errorf("failed to get role %s: %w", projectBinding.Role, err)
						}

						// Bind user to project with role
						if err := store.createUserProject(&model.UserScopedRole{
							UserID:    user.ID,
							RoleID:    projectRole.ID,
							ScopeType: consts.ScopeTypeProject,
							ScopeID:   fmt.Sprintf("%d", project.ID),
							Status:    consts.CommonEnabled,
						}); err != nil {
							return fmt.Errorf("failed to bind user %s to project %s with role %s: %w", user.Username, projectBinding.Name, projectBinding.Role, err)
						}
					}
				}
			}
		} else {
			logrus.Infof("Created user %s without team bindings", user.Username)
		}

		// Bind user to specified projects directly (not through teams)
		if len(userData.Projects) > 0 {
			for _, projectBinding := range userData.Projects {
				// Get project by name
				project, err := store.getProjectByName(projectBinding.Name)
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return fmt.Errorf("project %s not found for user %s", projectBinding.Name, user.Username)
					}
					return fmt.Errorf("failed to get project %s: %w", projectBinding.Name, err)
				}

				// Get role by name
				projectRole, err := store.getRoleByName(projectBinding.Role)
				if err != nil {
					if errors.Is(err, consts.ErrNotFound) {
						return fmt.Errorf("role %s not found for user %s in project %s", projectBinding.Role, user.Username, projectBinding.Name)
					}
					return fmt.Errorf("failed to get role %s: %w", projectBinding.Role, err)
				}

				// Bind user to project with role
				if err := store.createUserProject(&model.UserScopedRole{
					UserID:    user.ID,
					RoleID:    projectRole.ID,
					ScopeType: consts.ScopeTypeProject,
					ScopeID:   fmt.Sprintf("%d", project.ID),
					Status:    consts.CommonEnabled,
				}); err != nil {
					return fmt.Errorf("failed to bind user %s to project %s with role %s: %w", user.Username, projectBinding.Name, projectBinding.Role, err)
				}
			}
		}
	}

	return nil
}
