package observation

// MetricsCatalogResp is the response for GET /injections/:id/metrics/catalog.
type MetricsCatalogResp struct {
	Metrics []MetricCatalogItem `json:"metrics"`
}

type MetricCatalogItem struct {
	Name        string    `json:"name"`
	Unit        string    `json:"unit,omitempty"`
	Description string    `json:"description,omitempty"`
	Dimensions  []string  `json:"dimensions"`
	Quantiles   []float64 `json:"quantiles,omitempty"`
}

// MetricsSeriesReq captures the query parameters for GET /injections/:id/metrics/series.
type MetricsSeriesReq struct {
	Metric  string `form:"metric" binding:"required"`
	Start   string `form:"start"`
	End     string `form:"end"`
	Step    string `form:"step"`
	GroupBy string `form:"group_by"`
	Filter  string `form:"filter"`
}

type MetricsSeriesResp struct {
	Series []MetricSeries `json:"series"`
	Step   string         `json:"step"`
}

type MetricSeries struct {
	Labels map[string]string `json:"labels"`
	Points []MetricPoint     `json:"points"`
}

type MetricPoint struct {
	TS    string  `json:"ts"`
	Value float64 `json:"value"`
}

// ListSpansReq captures GET /injections/:id/spans query parameters.
type ListSpansReq struct {
	Service     string `form:"service"`
	Op          string `form:"op"`
	MinDuration int64  `form:"min_duration"`
	Start       string `form:"start"`
	End         string `form:"end"`
	Status      string `form:"status"`
	Limit       int    `form:"limit"`
	Cursor      string `form:"cursor"`
}

type ListSpansResp struct {
	Spans      []SpanSummary `json:"spans"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type SpanSummary struct {
	TraceID     string `json:"trace_id"`
	RootService string `json:"root_service"`
	RootOp      string `json:"root_op"`
	StartTS     string `json:"start_ts"`
	DurationNS  int64  `json:"duration_ns"`
	Status      string `json:"status"`
	ErrorCount  int64  `json:"error_count"`
}

// SpanTreeResp is the response for GET /injections/:id/spans/:trace_id.
type SpanTreeResp struct {
	Spans []SpanNode `json:"spans"`
}

type SpanNode struct {
	SpanID   string                 `json:"span_id"`
	ParentID string                 `json:"parent_id"`
	Service  string                 `json:"service"`
	Op       string                 `json:"op"`
	StartTS  string                 `json:"start_ts"`
	EndTS    string                 `json:"end_ts"`
	Attrs    map[string]interface{} `json:"attrs,omitempty"`
	Events   []SpanEvent            `json:"events,omitempty"`
	Status   string                 `json:"status"`
}

type SpanEvent struct {
	TS         string                 `json:"ts"`
	Name       string                 `json:"name"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// ServiceMapReq captures GET /injections/:id/service-map query parameters.
type ServiceMapReq struct {
	Window string `form:"window"`
}

type ServiceMapResp struct {
	Nodes []ServiceMapNode `json:"nodes"`
	Edges []ServiceMapEdge `json:"edges"`
}

type ServiceMapNode struct {
	Service   string  `json:"service"`
	SpanCount int64   `json:"span_count"`
	ErrorRate float64 `json:"error_rate"`
}

type ServiceMapEdge struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	CallCount int64   `json:"call_count"`
	ErrorRate float64 `json:"error_rate"`
	P50MS     float64 `json:"p50_ms"`
	P99MS     float64 `json:"p99_ms"`
}
