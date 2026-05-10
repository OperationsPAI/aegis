package observation

import (
	"net/http"

	"aegis/consts"
	"aegis/dto"
	"aegis/httpx"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service HandlerService
}

func NewHandler(service HandlerService) *Handler {
	return &Handler{service: service}
}

func parsePositiveID(c *gin.Context, key, label string) (int, bool) {
	return httpx.ParsePositiveID(c, c.Param(key), label)
}

// GetMetricsCatalog returns the discoverable metric catalog of an injection's datapack.
//
//	@Summary		Get metrics catalog
//	@Description	Discover available metrics in the injection's datapack
//	@Tags			Observation
//	@ID				get_observation_metrics_catalog
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		int										true	"Injection ID"
//	@Success		200	{object}	dto.GenericResponse[MetricsCatalogResp]	"Metrics catalog"
//	@Failure		400	{object}	dto.GenericResponse[any]				"Invalid injection ID"
//	@Failure		401	{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		403	{object}	dto.GenericResponse[any]				"Permission denied"
//	@Failure		404	{object}	dto.GenericResponse[any]				"Datapack not found or not ready"
//	@Failure		500	{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/injections/{id}/metrics/catalog [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) GetMetricsCatalog(c *gin.Context) {
	id, ok := parsePositiveID(c, consts.URLPathID, "injection ID")
	if !ok {
		return
	}
	resp, err := h.service.GetMetricsCatalog(c.Request.Context(), id)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// GetMetricsSeries returns time-series data for a metric in an injection's datapack.
//
//	@Summary		Get metric time series
//	@Description	Time series for a metric, optionally bucketed and grouped
//	@Tags			Observation
//	@ID				get_observation_metrics_series
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		int										true	"Injection ID"
//	@Param			metric		query		string									true	"Metric name"
//	@Param			start		query		string									false	"RFC3339 start"
//	@Param			end			query		string									false	"RFC3339 end"
//	@Param			step		query		string									false	"Step duration (e.g. 30s, 1m)"
//	@Param			group_by	query		string									false	"Dimension name to group by"
//	@Param			filter		query		string									false	"dim=value filter"
//	@Success		200			{object}	dto.GenericResponse[MetricsSeriesResp]	"Metric time series"
//	@Failure		400			{object}	dto.GenericResponse[any]				"Invalid request"
//	@Failure		401			{object}	dto.GenericResponse[any]				"Authentication required"
//	@Failure		403			{object}	dto.GenericResponse[any]				"Permission denied"
//	@Failure		404			{object}	dto.GenericResponse[any]				"Datapack not found or not ready"
//	@Failure		500			{object}	dto.GenericResponse[any]				"Internal server error"
//	@Router			/api/v2/injections/{id}/metrics/series [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) GetMetricsSeries(c *gin.Context) {
	id, ok := parsePositiveID(c, consts.URLPathID, "injection ID")
	if !ok {
		return
	}
	var req MetricsSeriesReq
	if err := c.ShouldBindQuery(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	resp, err := h.service.GetMetricsSeries(c.Request.Context(), id, &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// ListSpans returns trace summaries for the injection's datapack.
//
//	@Summary		List trace summaries
//	@Description	Trace summaries for an injection (one row per trace_id, root span)
//	@Tags			Observation
//	@ID				list_observation_spans
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id				path		int									true	"Injection ID"
//	@Param			service			query		string								false	"Filter by root service"
//	@Param			op				query		string								false	"Filter by root operation"
//	@Param			min_duration	query		int									false	"Minimum duration in ms"
//	@Param			start			query		string								false	"RFC3339 start"
//	@Param			end				query		string								false	"RFC3339 end"
//	@Param			status			query		string								false	"ok|error"
//	@Param			limit			query		int									false	"Page size, default 50, max 500"
//	@Param			cursor			query		string								false	"Opaque pagination cursor"
//	@Success		200				{object}	dto.GenericResponse[ListSpansResp]	"Trace summaries"
//	@Failure		400				{object}	dto.GenericResponse[any]			"Invalid request"
//	@Failure		401				{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403				{object}	dto.GenericResponse[any]			"Permission denied"
//	@Failure		404				{object}	dto.GenericResponse[any]			"Datapack not found or not ready"
//	@Failure		500				{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/api/v2/injections/{id}/spans [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) ListSpans(c *gin.Context) {
	id, ok := parsePositiveID(c, consts.URLPathID, "injection ID")
	if !ok {
		return
	}
	var req ListSpansReq
	if err := c.ShouldBindQuery(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	resp, err := h.service.ListSpans(c.Request.Context(), id, &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// GetSpanTree returns the full span tree for one trace.
//
//	@Summary		Get span tree
//	@Description	Full span tree for one trace_id in the injection's datapack
//	@Tags			Observation
//	@ID				get_observation_span_tree
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id			path		int									true	"Injection ID"
//	@Param			trace_id	path		string								true	"Trace ID"
//	@Success		200			{object}	dto.GenericResponse[SpanTreeResp]	"Span tree"
//	@Failure		400			{object}	dto.GenericResponse[any]			"Invalid request"
//	@Failure		401			{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403			{object}	dto.GenericResponse[any]			"Permission denied"
//	@Failure		404			{object}	dto.GenericResponse[any]			"Datapack not found or trace not found"
//	@Failure		500			{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/api/v2/injections/{id}/spans/{trace_id} [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) GetSpanTree(c *gin.Context) {
	id, ok := parsePositiveID(c, consts.URLPathID, "injection ID")
	if !ok {
		return
	}
	traceID := c.Param("trace_id")
	if traceID == "" {
		dto.ErrorResponse(c, http.StatusBadRequest, "trace_id is required")
		return
	}
	resp, err := h.service.GetSpanTree(c.Request.Context(), id, traceID)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}

// GetServiceMap returns service dependency edges aggregated from spans.
//
//	@Summary		Get service map
//	@Description	Service dependency edges and node summaries
//	@Tags			Observation
//	@ID				get_observation_service_map
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		int									true	"Injection ID"
//	@Param			window	query		string								false	"fault|normal|both (default fault)"
//	@Success		200		{object}	dto.GenericResponse[ServiceMapResp]	"Service map"
//	@Failure		400		{object}	dto.GenericResponse[any]			"Invalid request"
//	@Failure		401		{object}	dto.GenericResponse[any]			"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]			"Permission denied"
//	@Failure		404		{object}	dto.GenericResponse[any]			"Datapack not found or not ready"
//	@Failure		500		{object}	dto.GenericResponse[any]			"Internal server error"
//	@Router			/api/v2/injections/{id}/service-map [get]
//	@x-api-type		{"sdk":"true"}
func (h *Handler) GetServiceMap(c *gin.Context) {
	id, ok := parsePositiveID(c, consts.URLPathID, "injection ID")
	if !ok {
		return
	}
	var req ServiceMapReq
	if err := c.ShouldBindQuery(&req); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	resp, err := h.service.GetServiceMap(c.Request.Context(), id, &req)
	if httpx.HandleServiceError(c, err) {
		return
	}
	dto.SuccessResponse(c, resp)
}
