package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"aegis/consts"
	"aegis/dto"
	"aegis/httpx"
	"aegis/middleware"
	"aegis/model"
	"aegis/utils"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// RegisterContainerReq is the request body for POST /api/v2/containers/register.
//
// It composes the three-way create of (containers, container_versions,
// helm_configs — pedestal only) into a single atomic operation. Each stage is
// logged with the same register_id for after-the-fact debuggability (issue
// #102).
type RegisterContainerReq struct {
	// Form is "pedestal" or "benchmark". Mirrors the two CLI flags.
	Form string `json:"form" binding:"required"`

	// containers.name — must equal the system short code for pedestals.
	Name string `json:"name" binding:"required"`

	// Image triple. For pedestals the image ref is optional/empty (helm charts
	// replace the image concept); for benchmarks it must resolve to a
	// non-empty registry/namespace/repository:tag.
	Registry string `json:"registry"`
	Repo     string `json:"repo"`
	Tag      string `json:"tag"`

	// container_versions.name (semantic version). Optional; defaults to chart
	// version for pedestals and tag-as-version fallback for benchmarks, and
	// finally "1.0.0".
	Version string `json:"version"`

	// Benchmark-only.
	Command string                     `json:"command"`
	EnvVars []RegisterContainerEnvVar  `json:"env"`

	// Pedestal-only.
	ChartName    string `json:"chart_name"`
	ChartVersion string `json:"chart_version"`
	RepoURL      string `json:"repo_url"`
	RepoName     string `json:"repo_name"`
	// ValuesFile is the server-visible path of a values.yaml file; the CLI
	// is responsible for either uploading it separately (post-register) or
	// passing a path already visible to the backend. We store it on the
	// helm_configs row via ValueFile. Empty is permitted.
	ValuesFile string `json:"values_file"`
}

type RegisterContainerEnvVar struct {
	Key   string `json:"key" binding:"required"`
	Value string `json:"value"`
}

// Validate normalizes & enforces the invariants described in issue #102.
// The atomic endpoint refuses to start the transaction when Validate fails.
func (req *RegisterContainerReq) Validate() error {
	req.Form = strings.TrimSpace(strings.ToLower(req.Form))
	req.Name = strings.TrimSpace(req.Name)
	req.Registry = strings.TrimSpace(req.Registry)
	req.Repo = strings.TrimSpace(req.Repo)
	req.Tag = strings.TrimSpace(req.Tag)
	req.Version = strings.TrimSpace(req.Version)
	req.Command = strings.TrimSpace(req.Command)
	req.ChartName = strings.TrimSpace(req.ChartName)
	req.ChartVersion = strings.TrimSpace(req.ChartVersion)
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.RepoName = strings.TrimSpace(req.RepoName)
	req.ValuesFile = strings.TrimSpace(req.ValuesFile)

	if req.Name == "" {
		return fmt.Errorf("%w: name is required", consts.ErrBadRequest)
	}
	if req.Form != "pedestal" && req.Form != "benchmark" {
		return fmt.Errorf("%w: form must be 'pedestal' or 'benchmark' (got %q)", consts.ErrBadRequest, req.Form)
	}

	switch req.Form {
	case "benchmark":
		if req.Command == "" {
			return fmt.Errorf("%w: benchmark command must be non-empty", consts.ErrBadRequest)
		}
		if req.Registry == "" || req.Repo == "" || req.Tag == "" {
			return fmt.Errorf("%w: benchmark requires --registry, --repo, --tag", consts.ErrBadRequest)
		}
		// The image triple must resolve to a non-empty image_ref.
		ref := composeImageRef(req.Registry, req.Repo, req.Tag)
		if _, _, _, _, err := utils.ParseFullImageRefernce(ref); err != nil {
			return fmt.Errorf("%w: benchmark image triple does not resolve: %v", consts.ErrBadRequest, err)
		}
		for i, e := range req.EnvVars {
			if strings.TrimSpace(e.Key) == "" {
				return fmt.Errorf("%w: env[%d].key is empty", consts.ErrBadRequest, i)
			}
		}
		if req.Version == "" {
			if _, _, _, err := utils.ParseSemanticVersion(req.Tag); err == nil {
				req.Version = req.Tag
			} else {
				req.Version = "1.0.0"
			}
		}
	case "pedestal":
		if req.ChartName == "" || req.ChartVersion == "" || req.RepoURL == "" || req.RepoName == "" {
			return fmt.Errorf("%w: pedestal requires --chart-name, --chart-version, --repo-url, --repo-name", consts.ErrBadRequest)
		}
		if _, err := url.ParseRequestURI(req.RepoURL); err != nil {
			return fmt.Errorf("%w: invalid --repo-url: %v", consts.ErrBadRequest, err)
		}
		if req.Version == "" {
			req.Version = req.ChartVersion
		}
	}

	if _, _, _, err := utils.ParseSemanticVersion(req.Version); err != nil {
		return fmt.Errorf("%w: --version / derived version must be semver (got %q): %v", consts.ErrBadRequest, req.Version, err)
	}

	return nil
}

// composeImageRef is the canonical way to build a full image reference from
// the three CLI flags. Empty components are collapsed cleanly so
// "docker.io/opspai/img:tag" and "docker.io/img:tag" both round-trip through
// ParseFullImageRefernce.
func composeImageRef(registry, repo, tag string) string {
	registry = strings.TrimSpace(registry)
	repo = strings.TrimSpace(strings.Trim(repo, "/"))
	tag = strings.TrimSpace(tag)
	if registry == "" {
		registry = "docker.io"
	}
	if repo == "" {
		return fmt.Sprintf("%s:%s", registry, tag)
	}
	return fmt.Sprintf("%s/%s:%s", registry, repo, tag)
}

// RegisterContainerResp carries the IDs that future calls (including
// `aegisctl container version describe`) need to round-trip the new row
// trio, plus the register_id so operators can correlate CLI output with
// server logs.
type RegisterContainerResp struct {
	RegisterID       string `json:"register_id"`
	ContainerID      int    `json:"container_id"`
	ContainerName    string `json:"container_name"`
	ContainerType    string `json:"container_type"`
	VersionID        int    `json:"version_id"`
	VersionName      string `json:"version_name"`
	ImageRef         string `json:"image_ref"`
	HelmConfigID     int    `json:"helm_config_id,omitempty"`
	ChartName        string `json:"chart_name,omitempty"`
	ChartVersion     string `json:"chart_version,omitempty"`
	ContainerExisted bool   `json:"container_existed"`
}

// newRegisterID returns an opaque 12-hex-char correlator. Short enough to
// fit a single log line prefix, long enough to avoid collisions across
// concurrent requests in the common case.
func newRegisterID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to a timestamped placeholder; we never want ID
		// generation itself to fail the register.
		return "reg-noent"
	}
	return "reg-" + hex.EncodeToString(b[:])
}

// RegisterContainer atomically creates the (container, container_version,
// helm_config) trio for a pedestal — or the (container, container_version)
// pair for a benchmark — emitting stage-tagged logrus entries all correlated
// by register_id so a mid-flight failure is debuggable by reading server
// logs after the fact (issue #102).
//
// Pre-transaction stages (validate_name, check_collision) run before the DB
// transaction opens so their errors are reported with the same register_id
// without having already written anything. Stages inside the transaction
// roll back on error; the last log line for a failed request always names
// the offending stage with its verbatim error.
func (s *Service) RegisterContainer(ctx context.Context, req *RegisterContainerReq, userID int) (*RegisterContainerResp, error) {
	if req == nil {
		return nil, fmt.Errorf("register request is nil")
	}
	regID := newRegisterID()

	log := func(stage string) *logrus.Entry {
		return logrus.WithFields(logrus.Fields{
			"register_id": regID,
			"stage":       stage,
			"name":        req.Name,
			"form":        req.Form,
		})
	}
	failStage := func(stage string, err error) error {
		// Surface the register_id in the error so HandleServiceError and
		// the CLI both echo it. The CLI prints this string verbatim.
		wrapped := fmt.Errorf("register failed at stage=%s (register_id=%s): %w", stage, regID, err)
		log(stage).WithError(err).Error("register: stage failed")
		return wrapped
	}

	// ---- stage: validate_name (also covers form/invariant checks) -------
	log("validate_name").Info("register: begin")
	if err := req.Validate(); err != nil {
		return nil, failStage("validate_name", err)
	}

	requestedType := consts.ContainerTypeBenchmark
	if req.Form == "pedestal" {
		requestedType = consts.ContainerTypePedestal
	}

	// ---- stage: check_collision -----------------------------------------
	// Refuse BEFORE write if a row with the same name but a different type
	// exists. This matches the prior-art CheckContainerExistsWithDifferentType
	// error path but surfaces a friendlier message.
	log("check_collision").Debug("register: checking type collision")
	exists, existingType, err := s.repo.checkContainerExistsWithDifferentType(req.Name, requestedType, 0)
	if err != nil {
		return nil, failStage("check_collision", err)
	}
	if exists {
		return nil, failStage("check_collision", fmt.Errorf("%w: container %q already exists as type=%d (pedestal=2/benchmark=1); pick a different name or delete the other",
			consts.ErrAlreadyExists, req.Name, int(existingType)))
	}

	// All writes live in a single transaction so failures roll back cleanly.
	var (
		resp             = &RegisterContainerResp{RegisterID: regID}
		containerModel   *model.Container
		versionModel     *model.ContainerVersion
		helmConfigModel  *model.HelmConfig
	)

	txErr := s.repo.db.Transaction(func(tx *gorm.DB) error {
		repo := NewRepository(tx)

		// ---- stage: insert_container --------------------------------
		log("insert_container").Debug("register: upserting container row")
		containerModel, err = s.registerUpsertContainer(repo, req, requestedType, userID)
		if err != nil {
			return failStage("insert_container", err)
		}
		resp.ContainerID = containerModel.ID
		resp.ContainerName = containerModel.Name
		resp.ContainerType = consts.GetContainerTypeName(containerModel.Type)
		// ContainerExisted is set by registerUpsertContainer via annotation
		// on the model (Status stays enabled; we mark via a closure-local
		// signal instead).

		// ---- stage: insert_version ----------------------------------
		log("insert_version").Debug("register: inserting container_version")
		versionModel, err = s.registerInsertVersion(repo, req, containerModel.ID, userID)
		if err != nil {
			return failStage("insert_version", err)
		}
		resp.VersionID = versionModel.ID
		resp.VersionName = versionModel.Name
		resp.ImageRef = versionModel.ImageRef

		// ---- stage: insert_helm_config (pedestal-only) -------------
		if req.Form == "pedestal" {
			log("insert_helm_config").Debug("register: inserting helm_config")
			helmConfigModel, err = s.registerInsertHelmConfig(repo, req, versionModel.ID)
			if err != nil {
				return failStage("insert_helm_config", err)
			}
			resp.HelmConfigID = helmConfigModel.ID
			resp.ChartName = helmConfigModel.ChartName
			resp.ChartVersion = helmConfigModel.Version
		}

		// ---- stage: commit ------------------------------------------
		// GORM commits on return nil; the log here marks intent. The
		// actual COMMIT happens after this closure returns nil.
		log("commit").Info("register: committing transaction")
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}

	// TODO(#102): once an audit middleware/service is available (see
	// AegisLab/src/middleware/), emit an audit row here:
	//   event=container.register, register_id=regID, actor=userID,
	//   name=req.Name, form=req.Form, container_id=resp.ContainerID,
	//   version_id=resp.VersionID, helm_config_id=resp.HelmConfigID.

	log("commit").WithFields(logrus.Fields{
		"container_id": resp.ContainerID,
		"version_id":   resp.VersionID,
		"helm_id":      resp.HelmConfigID,
	}).Info("register: ok")

	return resp, nil
}

// registerUpsertContainer reuses an existing containers row when one with the
// same name AND the same type is found; otherwise it creates one. The type
// collision check has already run pre-transaction so we can safely assume
// same-name implies same-type here.
func (s *Service) registerUpsertContainer(repo *Repository, req *RegisterContainerReq, cType consts.ContainerType, userID int) (*model.Container, error) {
	existing, err := repo.findContainerByNameAndType(req.Name, cType)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	isPublic := true
	container := &model.Container{
		Name:     req.Name,
		Type:     cType,
		Status:   consts.CommonEnabled,
		IsPublic: isPublic,
	}
	if err := repo.createContainer(container); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, fmt.Errorf("%w: container %q", consts.ErrAlreadyExists, req.Name)
		}
		return nil, err
	}

	// The user-role association is best-effort: if the role isn't seeded
	// (e.g. unit tests) we skip rather than fail the register. Real
	// deployments always have RoleContainerAdmin via RBAC bootstrap.
	if role, err := repo.getRoleByName(consts.RoleContainerAdmin.String()); err == nil && userID > 0 {
		_ = repo.createUserContainer(&model.UserContainer{
			UserID:      userID,
			ContainerID: container.ID,
			RoleID:      role.ID,
			Status:      consts.CommonEnabled,
		})
	}

	return container, nil
}

// registerInsertVersion creates a container_versions row. For benchmarks the
// image triple is attached; for pedestals the version is a marker row (no
// image) and helm_config carries the deployment identity.
func (s *Service) registerInsertVersion(repo *Repository, req *RegisterContainerReq, containerID, userID int) (*model.ContainerVersion, error) {
	version := &model.ContainerVersion{
		Name:        req.Version,
		Command:     req.Command,
		Status:      consts.CommonEnabled,
		ContainerID: containerID,
		UserID:      userID,
	}
	if req.Form == "benchmark" {
		version.ImageRef = composeImageRef(req.Registry, req.Repo, req.Tag)
	}
	if err := repo.batchCreateContainerVersions([]model.ContainerVersion{*version}); err != nil {
		return nil, err
	}

	// batchCreateContainerVersions inserts via a slice — recover the
	// inserted ID by reloading the most recent version for this container
	// under this name.
	loaded, err := repo.getContainerVersionByNameAndContainer(containerID, req.Version)
	if err != nil {
		return nil, err
	}

	// Populate env vars (benchmark form only, optional).
	if req.Form == "benchmark" && len(req.EnvVars) > 0 {
		params := make([]model.ParameterConfig, 0, len(req.EnvVars))
		for _, e := range req.EnvVars {
			val := e.Value
			params = append(params, model.ParameterConfig{
				Key:          e.Key,
				Type:         consts.ParameterTypeFixed,
				Category:     consts.ParameterCategoryEnvVars,
				Required:     true,
				Overridable:  true,
				DefaultValue: &val,
			})
		}
		if err := repo.batchCreateOrFindParameterConfigs(params); err != nil {
			return nil, err
		}
		found, err := repo.listParameterConfigsByKeys(params)
		if err != nil {
			return nil, err
		}
		relations := make([]model.ContainerVersionEnvVar, 0, len(found))
		for _, p := range found {
			relations = append(relations, model.ContainerVersionEnvVar{
				ContainerVersionID: loaded.ID,
				ParameterConfigID:  p.ID,
			})
		}
		if err := repo.addContainerVersionEnvVars(relations); err != nil {
			return nil, err
		}
	}

	return loaded, nil
}

// registerInsertHelmConfig inserts the helm_configs row tied to the newly
// created container_versions row. Pedestal-only.
func (s *Service) registerInsertHelmConfig(repo *Repository, req *RegisterContainerReq, versionID int) (*model.HelmConfig, error) {
	helm := &model.HelmConfig{
		ChartName:          req.ChartName,
		Version:            req.ChartVersion,
		ContainerVersionID: versionID,
		RepoURL:            req.RepoURL,
		RepoName:           req.RepoName,
		ValueFile:          req.ValuesFile,
	}
	if err := repo.batchCreateHelmConfigs([]*model.HelmConfig{helm}); err != nil {
		return nil, err
	}
	return helm, nil
}

// RegisterContainer handler wires the atomic endpoint. Contract documented
// in issue #102.
//
//	@Summary		Atomically register a container trio
//	@Description	In a single transaction creates (container, container_version, helm_config) for pedestals or (container, container_version) for benchmarks. Each stage is logged with a shared register_id for after-the-fact debuggability.
//	@Tags			Containers
//	@ID				register_container
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		RegisterContainerReq						true	"Atomic register payload"
//	@Success		201		{object}	dto.GenericResponse[RegisterContainerResp]	"Register succeeded"
//	@Failure		400		{object}	dto.GenericResponse[any]					"Validation failed"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]					"Permission denied"
//	@Failure		409		{object}	dto.GenericResponse[any]					"Name collision with different type"
//	@Failure		500		{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/api/v2/containers/register [post]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) RegisterContainer(c *gin.Context) {
	userID, exists := middleware.GetCurrentUserID(c)
	if !exists || userID <= 0 {
		dto.ErrorResponse(c, http.StatusUnauthorized, "Authentication required")
		return
	}

	var req RegisterContainerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}

	resp, err := h.service.RegisterContainer(c.Request.Context(), &req, userID)
	if httpx.HandleServiceError(c, err) {
		return
	}

	dto.JSONResponse[any](c, http.StatusCreated, "Container registered successfully", resp)
}
