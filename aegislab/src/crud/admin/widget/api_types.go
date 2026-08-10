package widget

type PingResp struct {
	Module             string `json:"module"`
	RouteRegistered    bool   `json:"route_registered"`
	SelfRegisteredVia  string `json:"self_registered_via"`
	FrameworkFilesEdit bool   `json:"framework_files_edit"`
}
