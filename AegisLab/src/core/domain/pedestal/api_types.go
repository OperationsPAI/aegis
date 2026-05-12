package pedestal

// ---------------------- Pedestal Helm DTOs ------------------

// PedestalHelmConfigResp represents a full helm_configs row for CLI/API consumers.
type PedestalHelmConfigResp struct {
	ID                 int    `json:"id"`
	ContainerVersionID int    `json:"container_version_id"`
	ChartName          string `json:"chart_name"`
	Version            string `json:"version"`
	RepoURL            string `json:"repo_url"`
	RepoName           string `json:"repo_name"`
	ValueFile          string `json:"value_file"`
	LocalPath          string `json:"local_path"`
	Checksum           string `json:"checksum"`
}

// UpsertPedestalHelmConfigReq is the body for PUT /api/v2/pedestal/helm/:container_version_id
type UpsertPedestalHelmConfigReq struct {
	ChartName string `json:"chart_name" binding:"required"`
	Version   string `json:"version" binding:"required"`
	RepoURL   string `json:"repo_url" binding:"required"`
	RepoName  string `json:"repo_name" binding:"required"`
	ValueFile string `json:"value_file"`
	LocalPath string `json:"local_path"`
}

// PedestalHelmVerifyCheck is a single step in the verify pipeline.
type PedestalHelmVerifyCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// PedestalHelmVerifyResp is the aggregated verify response.
type PedestalHelmVerifyResp struct {
	OK     bool                      `json:"ok"`
	Checks []PedestalHelmVerifyCheck `json:"checks"`
}

// ---------------------- Pedestal Helm Reseed DTOs ----------------------

// ReseedHelmConfigReq is the body for POST
// /api/v2/pedestal/helm/{container_version_id}/reseed.
//
// Defaults: Apply=false (the server runs a dry-run unless Apply=true). This
// mirrors `aegisctl system reseed` so a misfired POST never writes.
type ReseedHelmConfigReq struct {
	// Env selects prod / staging when the server-side initialization.data_path
	// points at the initial_data root. Empty falls back to whatever the file
	// at DataPath (or `initialization.data_path`) contains.
	Env string `json:"env,omitempty"`
	// DataPath optionally overrides server-side initialization.data_path
	// (e.g. for one-off file paths produced by aegisctl). Server-side admins
	// only — the handler still resolves this against ResolveSeedPath.
	DataPath string `json:"data_path,omitempty"`
	// Apply: when false, the response is a dry-run plan. When true, mutations
	// are committed to the DB.
	Apply bool `json:"apply"`
	// Prune: when true, helm_config_values links whose key disappeared from
	// the seed are removed. Default false to avoid surprising deletions.
	Prune bool `json:"prune,omitempty"`
}

// ReseedActionResp mirrors initialization.ReseedAction without forcing the
// CLI / external clients to depend on the initialization package.
type ReseedActionResp struct {
	Layer    string `json:"layer"`
	System   string `json:"system"`
	Key      string `json:"key"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	Note     string `json:"note"`
	Applied  bool   `json:"applied"`
}

// ReseedHelmConfigResp is the aggregated reseed report returned by the
// pedestal helm reseed endpoint.
type ReseedHelmConfigResp struct {
	DryRun       bool               `json:"dry_run"`
	SystemFilter string             `json:"system_filter"`
	SeedPath     string             `json:"seed_path"`
	Actions      []ReseedActionResp `json:"actions"`
}
