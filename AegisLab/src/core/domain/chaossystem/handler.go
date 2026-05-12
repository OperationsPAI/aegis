package chaossystem

import (
	"aegis/platform/httpx"
	"net/http"

	"aegis/platform/consts"
	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service HandlerService
}

func NewHandler(service HandlerService) *Handler {
	return &Handler{service: service}
}

// ListChaosSystemsHandler handles listing chaos systems with pagination
//
//	@Summary		List chaos systems
//	@Description	Get a paginated list of registered chaos systems
//	@Tags			Systems
//	@ID				list_chaos_systems
//	@Produce		json
//	@Security		BearerAuth
//	@Param			page	query		int													false	"Page number"	default(1)
//	@Param			size	query		int													false	"Page size"		default(20)
//	@Success		200		{object}	dto.GenericResponse[dto.ListResp[ChaosSystemResp]]	"Systems retrieved successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]							"Invalid request format or parameters"
//	@Failure		401		{object}	dto.GenericResponse[any]							"Authentication required"
//	@Failure		500		{object}	dto.GenericResponse[any]							"Internal server error"
//	@Router			/api/v2/systems [get]
//	@x-api-type		{"admin":"true"}
func (h *Handler) ListSystems(c *gin.Context) {
	var req ListChaosSystemReq
	if err := c.ShouldBindQuery(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}
	resp, err := h.service.ListSystems(c.Request.Context(), &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// GetChaosSystemHandler handles getting a single chaos system by ID
//
//	@Summary		Get chaos system by ID
//	@Description	Get detailed information about a specific chaos system
//	@Tags			Systems
//	@ID				get_chaos_system
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int										true	"System ID"
//	@Success		200	{object}	dto.GenericResponse[ChaosSystemResp]	"System retrieved successfully"
//	@Failure		400	{object}	dto.GenericResponse[any]				"Invalid system ID"
//	@Failure		404	{object}	dto.GenericResponse[any]				"System not found"
//	@Failure		500	{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/systems/{id} [get]
//	@x-api-type		{"admin":"true"}
func (h *Handler) GetSystem(c *gin.Context) {
	id, ok := httpx.ParsePositiveID(c, c.Param(consts.URLPathID), "system ID")
	if !ok {
		return
	}
	resp, err := h.service.GetSystem(c.Request.Context(), id)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// GetChaosSystemChartHandler returns the chart source for a system by short name.
//
//	@Summary		Get chart source for a chaos system by name
//	@Description	Resolves the chart (repo_url/chart_name/version/local_path) bound to the active pedestal ContainerVersion for the given system short code (e.g. "mm", "tea"). Used by aegisctl pedestal chart install to fetch the tgz without walking containers→versions→helm_configs.
//	@Tags			Systems
//	@ID				get_chaos_system_chart_by_name
//	@Produce		json
//	@Security		BearerAuth
//	@Param			name	path		string									true	"System short code"
//	@Param			version	query		string									false	"Container version (semver, e.g. 1.1.2). When omitted, returns the highest-versioned active container_version."
//	@Success		200		{object}	dto.GenericResponse[SystemChartResp]	"Chart retrieved"
//	@Failure		404		{object}	dto.GenericResponse[any]				"System has no active pedestal chart for the requested version"
//	@Failure		500		{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/systems/by-name/{name}/chart [get]
//	@x-api-type		{"admin":"true","sdk":"true"}
func (h *Handler) GetSystemChart(c *gin.Context) {
	name := c.Param("name")
	version := c.Query("version")
	resp, err := h.service.GetSystemChart(c.Request.Context(), name, version)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// CreateChaosSystemHandler handles creating a new chaos system
//
//	@Summary		Create chaos system
//	@Description	Register a new chaos system
//	@Tags			Systems
//	@ID				create_chaos_system
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		CreateChaosSystemReq					true	"System creation request"
//	@Success		201		{object}	dto.GenericResponse[ChaosSystemResp]	"System created successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]				"Invalid request format or parameters"
//	@Failure		401		{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		409		{object}	dto.GenericResponse[any]				"System already exists"
//	@Failure		500		{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/systems [post]
//	@x-api-type		{"admin":"true"}
func (h *Handler) CreateSystem(c *gin.Context) {
	var req CreateChaosSystemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	resp, err := h.service.CreateSystem(c.Request.Context(), &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.JSONResponse(c, http.StatusCreated, "System created successfully", resp)
}

// UpdateChaosSystemHandler handles updating a chaos system
//
//	@Summary		Update chaos system
//	@Description	Update an existing chaos system
//	@Tags			Systems
//	@ID				update_chaos_system
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		int										true	"System ID"
//	@Param			request	body		UpdateChaosSystemReq					true	"System update request"
//	@Success		200		{object}	dto.GenericResponse[ChaosSystemResp]	"System updated successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]				"Invalid request format or parameters"
//	@Failure		404		{object}	dto.GenericResponse[any]				"System not found"
//	@Failure		500		{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/systems/{id} [put]
//	@x-api-type		{"admin":"true"}
func (h *Handler) UpdateSystem(c *gin.Context) {
	id, ok := httpx.ParsePositiveID(c, c.Param(consts.URLPathID), "system ID")
	if !ok {
		return
	}
	var req UpdateChaosSystemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	resp, err := h.service.UpdateSystem(c.Request.Context(), id, &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// DeleteChaosSystemHandler handles deleting a chaos system
//
//	@Summary		Delete chaos system
//	@Description	Soft-delete a chaos system (builtin systems cannot be deleted)
//	@Tags			Systems
//	@ID				delete_chaos_system
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int							true	"System ID"
//	@Success		200	{object}	dto.GenericResponse[any]	"System deleted successfully"
//	@Failure		400	{object}	dto.GenericResponse[any]	"Invalid system ID or builtin system"
//	@Failure		404	{object}	dto.GenericResponse[any]	"System not found"
//	@Failure		500	{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/systems/{id} [delete]
//	@x-api-type		{"admin":"true"}
func (h *Handler) DeleteSystem(c *gin.Context) {
	id, ok := httpx.ParsePositiveID(c, c.Param(consts.URLPathID), "system ID")
	if !ok {
		return
	}
	if httpx.HandleServiceError(c, h.service.DeleteSystem(c.Request.Context(), id)) {
		return
	}
	dto.JSONResponse[any](c, http.StatusOK, "System deleted successfully", nil)
}

// UpsertChaosSystemMetadataHandler handles bulk upserting metadata for a chaos system
//
//	@Summary		Upsert chaos system metadata
//	@Description	Bulk upsert metadata entries for a chaos system
//	@Tags			Systems
//	@ID				upsert_chaos_system_metadata
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		int							true	"System ID"
//	@Param			request	body		BulkUpsertSystemMetadataReq	true	"Metadata upsert request"
//	@Success		200		{object}	dto.GenericResponse[any]	"Metadata upserted successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]	"Invalid request format or parameters"
//	@Failure		404		{object}	dto.GenericResponse[any]	"System not found"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/systems/{id}/metadata [post]
//	@x-api-type		{"admin":"true"}
func (h *Handler) UpsertMetadata(c *gin.Context) {
	id, ok := httpx.ParsePositiveID(c, c.Param(consts.URLPathID), "system ID")
	if !ok {
		return
	}
	var req BulkUpsertSystemMetadataReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Validation failed: "+err.Error())
		return
	}
	if httpx.HandleServiceError(c, h.service.UpsertMetadata(c.Request.Context(), id, &req)) {
		return
	}
	dto.JSONResponse[any](c, http.StatusOK, "Metadata upserted successfully", nil)
}

// ReseedSystemsHandler propagates data.yaml bumps (chart version / chart
// name / new container_version rows / dynamic_config default drift) onto a
// running DB + etcd. Defaults to dry-run.
//
//	@Summary		Reseed systems from data.yaml
//	@Description	Diff the on-disk data.yaml against the live DB + etcd and apply drift. Defaults to dry-run; set `apply=true` to write. Use `name` to limit to one system; `reset_overrides=true` to replace live etcd values that differ from the new default.
//	@Tags			Systems
//	@ID				reseed_chaos_systems
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			request	body		ReseedSystemReq	true	"Reseed request"
//	@Success		200		{object}	dto.GenericResponse[any]	"Reseed report"
//	@Failure		400		{object}	dto.GenericResponse[any]	"Invalid request"
//	@Failure		500		{object}	dto.GenericResponse[any]	"Internal server error"
//	@Router			/api/v2/systems/reseed [post]
//	@x-api-type		{"admin":"true"}
func (h *Handler) ReseedSystems(c *gin.Context) {
	var req ReseedSystemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	resp, err := h.service.ReseedSystems(c.Request.Context(), &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// ListSystemPrerequisitesHandler returns the declared cluster-level
// prerequisites for a system (issue #115). aegisctl consumes this before
// running `helm upgrade --install` against each prereq.
//
//	@Summary		List system prerequisites
//	@Description	List the cluster-level prerequisites declared for a benchmark system (issue #115). Returns [] if the system has none.
//	@Tags			Systems
//	@ID				list_system_prerequisites
//	@Produce		json
//	@Security		BearerAuth
//	@Param			name	path		string											true	"System short code"
//	@Success		200		{object}	dto.GenericResponse[[]SystemPrerequisiteResp]	"Prerequisites retrieved"
//	@Failure		404		{object}	dto.GenericResponse[any]						"System not found"
//	@Failure		500		{object}	dto.GenericResponse[any]						"Internal server error"
//	@Router			/api/v2/systems/by-name/{name}/prerequisites [get]
//	@x-api-type		{"admin":"true","sdk":"true"}
func (h *Handler) ListPrerequisites(c *gin.Context) {
	name := c.Param("name")
	resp, err := h.service.ListPrerequisites(c.Request.Context(), name)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// MarkSystemPrerequisiteHandler lets aegisctl flip the status of a single
// prerequisite (reconciled after a successful helm install, failed on error).
// Backend never shells out to helm — it just tracks state.
//
//	@Summary		Mark a prerequisite status
//	@Description	Flip the status of one prerequisite (pending/reconciled/failed). aegisctl is the sole writer; backend does not shell out to helm.
//	@Tags			Systems
//	@ID				mark_system_prerequisite
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			name	path		string									true	"System short code"
//	@Param			id		path		int										true	"Prerequisite ID"
//	@Param			request	body		MarkPrerequisiteReq						true	"Mark request"
//	@Success		200		{object}	dto.GenericResponse[SystemPrerequisiteResp]	"Status updated"
//	@Failure		400		{object}	dto.GenericResponse[any]				"Invalid request"
//	@Failure		404		{object}	dto.GenericResponse[any]				"Prerequisite not found"
//	@Failure		500		{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/systems/by-name/{name}/prerequisites/{id}/mark [post]
//	@x-api-type		{"admin":"true","sdk":"true"}
func (h *Handler) MarkPrerequisite(c *gin.Context) {
	name := c.Param("name")
	id, ok := httpx.ParsePositiveID(c, c.Param(consts.URLPathID), "prerequisite ID")
	if !ok {
		return
	}
	var req MarkPrerequisiteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request format: "+err.Error())
		return
	}
	resp, err := h.service.MarkPrerequisite(c.Request.Context(), name, id, &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// ListInjectCandidatesHandler returns every reachable (app, chaos_type, target)
// tuple for a system+namespace pair (issue #181). One request replaces the
// N-round-trip walk through `aegisctl inject guided` for adversarial /
// coverage-driven loops.
//
//	@Summary		List inject candidates for a system+namespace
//	@Description	Bulk enumeration of every (app, chaos_type, target) tuple reachable for the given system short code and namespace. Numerical params (latency, cpu_load, ...) are NOT expanded — the caller fills those in before submitting. Replaces the previous N-round-trip walk through `aegisctl inject guided`.
//	@Tags			Systems
//	@ID				list_system_inject_candidates
//	@Produce		json
//	@Security		BearerAuth
//	@Param			name		path		string										true	"System short code"
//	@Param			namespace	query		string										true	"Target namespace (e.g. sockshop1)"
//	@Success		200			{object}	dto.GenericResponse[InjectCandidatesResp]	"Candidates retrieved"
//	@Failure		400			{object}	dto.GenericResponse[any]					"Missing namespace or invalid system code"
//	@Failure		404			{object}	dto.GenericResponse[any]					"System not found"
//	@Failure		500			{object}	dto.GenericResponse[any]					"k8s/resourcelookup failure"
//	@Router			/api/v2/systems/by-name/{name}/inject-candidates [get]
//	@x-api-type		{"admin":"true","sdk":"true"}
func (h *Handler) ListInjectCandidates(c *gin.Context) {
	name := c.Param("name")
	namespace := c.Query("namespace")
	resp, err := h.service.ListInjectCandidates(c.Request.Context(), name, namespace)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// ListChaosSystemMetadataHandler handles listing metadata for a chaos system
//
//	@Summary		List chaos system metadata
//	@Description	List metadata entries for a chaos system, optionally filtered by type
//	@Tags			Systems
//	@ID				list_chaos_system_metadata
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		int											true	"System ID"
//	@Param			type	query		string										false	"Metadata type filter"
//	@Success		200		{object}	dto.GenericResponse[[]SystemMetadataResp]	"Metadata retrieved successfully"
//	@Failure		400		{object}	dto.GenericResponse[any]					"Invalid system ID"
//	@Failure		404		{object}	dto.GenericResponse[any]					"System not found"
//	@Failure		500		{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/api/v2/systems/{id}/metadata [get]
//	@x-api-type		{"admin":"true"}
func (h *Handler) ListMetadata(c *gin.Context) {
	id, ok := httpx.ParsePositiveID(c, c.Param(consts.URLPathID), "system ID")
	if !ok {
		return
	}
	resp, err := h.service.ListMetadata(c.Request.Context(), id, c.Query("type"))
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}
