package chaos

import (
	"context"
	"errors"
	"net/http"

	"aegis/crud/chaos/guided"

	localchaos "aegis/platform/chaos"
	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
)

// guided walkers reach into a live kubernetes cluster + resourcelookup cache;
// handler tests swap them out via these package-level seams. The guided
// package's GuidedConfig / GuidedResponse / FieldSpec / FieldOption / Preview
// types are aliases of the localchaos ones (see crud/chaos/guided/types.go),
// so the handler and the walker share a single wire type with no conversion.
var (
	testGuidedResolve   func(ctx context.Context, cfg localchaos.GuidedConfig) (*localchaos.GuidedResponse, error)
	testGuidedApplyNext func(response *localchaos.GuidedResponse, rawValue string) (localchaos.GuidedConfig, error)
	testGuidedEnumerate func(ctx context.Context, system, namespace string) ([]localchaos.GuidedConfig, error)
)

func resolveGuided(ctx context.Context, cfg localchaos.GuidedConfig) (*localchaos.GuidedResponse, error) {
	if testGuidedResolve != nil {
		return testGuidedResolve(ctx, cfg)
	}
	return guided.Resolve(ctx, cfg)
}

func applyGuidedNext(response *localchaos.GuidedResponse, rawValue string) (localchaos.GuidedConfig, error) {
	if testGuidedApplyNext != nil {
		return testGuidedApplyNext(response, rawValue)
	}
	return guided.ApplyNextSelection(response, rawValue)
}

func enumerateGuided(ctx context.Context, system, namespace string) ([]localchaos.GuidedConfig, error) {
	if testGuidedEnumerate != nil {
		return testGuidedEnumerate(ctx, system, namespace)
	}
	return guided.EnumerateAllCandidates(ctx, system, namespace)
}

func guidedResponseToDTO(resp *localchaos.GuidedResponse) ChaosGuidedResolveResp {
	if resp == nil {
		return ChaosGuidedResolveResp{}
	}
	return ChaosGuidedResolveResp{
		Mode:         resp.Mode,
		Stage:        resp.Stage,
		Config:       resp.Config,
		Resolved:     resp.Resolved,
		Next:         resp.Next,
		Preview:      resp.Preview,
		ApplyPayload: resp.ApplyPayload,
		Result:       resp.Result,
		CanApply:     resp.CanApply,
		Warnings:     resp.Warnings,
		Errors:       resp.Errors,
		Resources:    resp.Resources,
		Meta:         resp.Meta,
	}
}

// @Summary		Resolve the next step of a chaos guided walkthrough
// @Description	Walk one step of the guided state machine for chaos injection authoring.
// @Tags			Chaos
// @ID				chaos_guided_resolve
// @Accept			json
// @Produce		json
// @Security		BearerAuth
// @Param			request	body		ChaosGuidedResolveReq						true	"Current guided config"
// @Success		200		{object}	dto.GenericResponse[ChaosGuidedResolveResp]	"Next field / preview / can_apply"
// @Failure		400		{object}	dto.GenericResponse[any]					"Invalid request"
// @Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
// @Failure		500		{object}	dto.GenericResponse[any]					"Resolve failed"
// @Router			/v1beta/guided/resolve [post]
// @x-api-type		{"sdk":"true"}
func (h *Handler) GuidedResolve(c *gin.Context) {
	var req ChaosGuidedResolveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := resolveGuided(c.Request.Context(), req.Config)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, guidedResponseToDTO(resp))
}

// @Summary		Apply a guided next-step selection
// @Description	Merge a single field selection into the current GuidedConfig.
// @Tags			Chaos
// @ID				chaos_guided_apply_next
// @Accept			json
// @Produce		json
// @Security		BearerAuth
// @Param			request	body		ChaosGuidedApplyNextReq						true	"Current config + selection"
// @Success		200		{object}	dto.GenericResponse[ChaosGuidedApplyNextResp]	"Merged config"
// @Failure		400		{object}	dto.GenericResponse[any]					"Invalid request / selection"
// @Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
// @Failure		500		{object}	dto.GenericResponse[any]					"Resolve failed"
// @Router			/v1beta/guided/apply-next [post]
// @x-api-type		{"sdk":"true"}
func (h *Handler) GuidedApplyNext(c *gin.Context) {
	var req ChaosGuidedApplyNextReq
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := resolveGuided(c.Request.Context(), req.Current)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	merged, err := applyGuidedNext(resp, req.Value)
	if err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	dto.SuccessResponse(c, ChaosGuidedApplyNextResp{Config: merged})
}

// @Summary		Enumerate all guided candidates for a system
// @Description	Walk the guided enumeration tree and return one GuidedConfig per leaf.
// @Tags			Chaos
// @ID				chaos_list_system_candidates
// @Produce		json
// @Security		BearerAuth
// @Param			sys			path		string										true	"System name"
// @Param			namespace	query		string										false	"Override the kubernetes namespace (defaults to the system's ns_pattern)"
// @Success		200			{object}	dto.GenericResponse[ChaosSystemCandidatesResp]	"All candidate configs"
// @Failure		401			{object}	dto.GenericResponse[any]					"Authentication required"
// @Failure		404			{object}	dto.GenericResponse[any]					"System not found"
// @Failure		500			{object}	dto.GenericResponse[any]					"Enumerate failed"
// @Router			/v1beta/systems/{sys}/candidates [get]
// @x-api-type		{"sdk":"true"}
func (h *Handler) ListSystemCandidates(c *gin.Context) {
	sysName := c.Param("sys")
	if sysName == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "system name required")
		return
	}
	sys, err := h.Mgr.GetSystem(c.Request.Context(), sysName)
	if err != nil {
		if errors.Is(err, ErrSystemNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, err.Error())
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	namespace := c.Query("namespace")
	if namespace == "" {
		namespace = sys.NsPattern
	}
	candidates, err := enumerateGuided(c.Request.Context(), sysName, namespace)
	if err != nil {
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	dto.SuccessResponse(c, ChaosSystemCandidatesResp{
		System:     sysName,
		Namespace:  namespace,
		Candidates: candidates,
	})
}

// @Summary		Destroy a chaos injection by caller task_id
// @Description	Resolve task_id → (injection|batch) and run the appropriate destroy path.
// @Tags			Chaos
// @ID				chaos_delete_injection_by_task
// @Produce		json
// @Security		BearerAuth
// @Param			taskID	path		string									true	"Caller-supplied task_id (from caller_metadata.task_id)"
// @Success		200		{object}	dto.GenericResponse[ChaosTaskInjectionRef]	"Resolved + destroyed"
// @Failure		401		{object}	dto.GenericResponse[any]				"Authentication required"
// @Failure		404		{object}	dto.GenericResponse[any]				"No injection / batch carries this task_id"
// @Failure		500		{object}	dto.GenericResponse[any]				"Lookup or destroy failed"
// @Router			/v1beta/injections/by-task/{taskID} [delete]
// @x-api-type		{"sdk":"true"}
func (h *Handler) DeleteInjectionByTask(c *gin.Context) {
	taskID := c.Param("taskID")
	if taskID == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "taskID required")
		return
	}
	ref, err := h.Mgr.LookupTaskInjection(c.Request.Context(), taskID)
	if err != nil {
		if errors.Is(err, ErrInjectionNotFound) {
			dto.ErrorResponse(c, http.StatusNotFound, err.Error())
			return
		}
		dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := ChaosTaskInjectionRef{TaskID: taskID, IsBatch: ref.IsBatch}
	if ref.IsBatch {
		batch, err := h.Mgr.DeleteInjectionBatch(c.Request.Context(), ref.ID)
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrBatchNotFound) {
				code = http.StatusNotFound
			}
			dto.ErrorResponse(c, code, err.Error())
			return
		}
		resp := batchToDTO(batch)
		out.Batch = &resp
		dto.SuccessResponse(c, out)
		return
	}
	inj, err := h.Mgr.DeleteInjection(c.Request.Context(), ref.ID)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrInjectionNotFound) {
			code = http.StatusNotFound
		}
		dto.ErrorResponse(c, code, err.Error())
		return
	}
	resp := injectionToDTO(*inj)
	out.Injection = &resp
	dto.SuccessResponse(c, out)
}
