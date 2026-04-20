package cmd

// This file holds the small shared types used by multiple inject/regression
// code paths. They live here (rather than in inject.go or inject_guided.go)
// so that callers — `inject guided --apply`, `regression run`, and the execute
// CLI's YAML spec — can depend on a stable surface without pulling in the
// larger, command-specific files.

// injectSubmitResponse captures the fields we care about from the server's
// SubmitInjectionResp envelope (see src/dto/injection.go).
type injectSubmitResponse struct {
	GroupID       string             `json:"group_id"`
	Items         []injectSubmitItem `json:"items"`
	OriginalCount int                `json:"original_count"`
	Warnings      map[string]any     `json:"warnings,omitempty"`
}

// injectSubmitItem is one element of injectSubmitResponse.Items.
type injectSubmitItem struct {
	Index   int    `json:"index"`
	TraceID string `json:"trace_id"`
	TaskID  string `json:"task_id"`
}

// ContainerRef references a container image with optional overrides. It is
// shared by the execute CLI's YAML spec (see execute.go).
type ContainerRef struct {
	Name    string          `yaml:"name"                 json:"name"`
	Version string          `yaml:"version,omitempty"    json:"version,omitempty"`
	EnvVars []ParameterSpec `yaml:"env_vars,omitempty"   json:"env_vars,omitempty"`
	Payload map[string]any  `yaml:"payload,omitempty"    json:"payload,omitempty"`
}

// LabelItem is a key-value label, shared with the execute CLI's YAML spec.
type LabelItem struct {
	Key   string `yaml:"key"   json:"key"`
	Value string `yaml:"value" json:"value"`
}

// ParameterSpec is a key-value parameter (env var, etc.) used inside a
// ContainerRef envelope.
type ParameterSpec struct {
	Key   string `yaml:"key"   json:"key"`
	Value string `yaml:"value" json:"value"`
}
