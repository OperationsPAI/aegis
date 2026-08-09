package consts

// APIPrefixV2 is the API path prefix for the v2 REST surface. All clients in
// cmd/aegisctl and module/<resource>client should compose paths under this
// prefix so a future /api/v3 migration is a one-line change.
const APIPrefixV2 = "/api/v2"

// Static auth API paths.
const (
	APIPathAuthLogin       = APIPrefixV2 + "/auth/login"
	APIPathAuthRefresh     = APIPrefixV2 + "/auth/refresh"
	APIPathAuthProfile     = APIPrefixV2 + "/auth/profile"
	APIPathAuthAPIKeyToken = APIPrefixV2 + "/auth/api-key/token"
)

// Static collection API paths.
const (
	APIPathContainers   = APIPrefixV2 + "/containers"
	APIPathDatasets     = APIPrefixV2 + "/datasets"
	APIPathExecutions   = APIPrefixV2 + "/executions"
	APIPathEvaluations  = APIPrefixV2 + "/evaluations"
	APIPathInjections   = APIPrefixV2 + "/injections"
	APIPathProjects     = APIPrefixV2 + "/projects"
	APIPathSystems      = APIPrefixV2 + "/systems"
	APIPathTasks        = APIPrefixV2 + "/tasks"
	APIPathTraces       = APIPrefixV2 + "/traces"
	APIPathRateLimiters = APIPrefixV2 + "/rate-limiters"
)

// Static sub-paths.
const (
	APIPathContainersBuild    = APIPathContainers + "/build"
	APIPathContainersRegister = APIPathContainers + "/register"
	APIPathContainerVersions  = APIPrefixV2 + "/container-versions"
	APIPathSystemHealth       = APIPrefixV2 + "/system/health"
	APIPathSystemsReseed      = APIPathSystems + "/reseed"
	APIPathRateLimitersGC     = APIPathRateLimiters + "/gc"
	APIPathEventsPublish      = APIPrefixV2 + "/events:publish"
)

// Path prefixes for keyed sub-trees that callers compose dynamically.
const (
	APIPathBlobBuckets   = APIPrefixV2 + "/blob/buckets/"
	APIPathConfigPrefix  = APIPrefixV2 + "/config/"
	APIPathPedestalHelm  = APIPrefixV2 + "/pedestal/helm"
	APIPathSystemsByName = APIPathSystems + "/by-name"
)

// APIPathContainer returns the path for a single container by ID.
func APIPathContainer(id int) string {
	return APIPathContainers + "/" + itoa(id)
}

// APIPathContainerVersions returns the versions sub-resource path for a container.
func APIPathContainerVersionsFor(containerID int) string {
	return APIPathContainer(containerID) + "/versions"
}

// APIPathContainerVersion returns the path for a specific version of a container.
func APIPathContainerVersion(containerID, versionID int) string {
	return APIPathContainerVersionsFor(containerID) + "/" + itoa(versionID)
}

// APIPathContainerVersionImage returns the image sub-resource path for a container version.
func APIPathContainerVersionImage(versionID int) string {
	return APIPathContainerVersions + "/" + itoa(versionID) + "/image"
}

// APIPathDataset returns the path for a single dataset by ID.
func APIPathDataset(id int) string {
	return APIPathDatasets + "/" + itoa(id)
}

// APIPathDatasetVersions returns the versions sub-resource path for a dataset.
func APIPathDatasetVersions(datasetID int) string {
	return APIPathDataset(datasetID) + "/versions"
}

// APIPathEvaluation returns the path for a single evaluation by ID.
func APIPathEvaluation(id string) string {
	return APIPathEvaluations + "/" + id
}

// APIPathExecution returns the path for a single execution by ID.
func APIPathExecution(id string) string {
	return APIPathExecutions + "/" + id
}

// APIPathInjection returns the path for a single injection by ID.
func APIPathInjection(id int) string {
	return APIPathInjections + "/" + itoa(id)
}

// APIPathInjectionFiles returns the files sub-resource path for an injection.
func APIPathInjectionFiles(injectionID int) string {
	return APIPathInjection(injectionID) + "/files"
}

// APIPathInjectionDownload returns the download sub-resource path for an injection.
func APIPathInjectionDownload(injectionID int) string {
	return APIPathInjection(injectionID) + "/download"
}

// APIPathProject returns the path for a single project by ID.
func APIPathProject(id int) string {
	return APIPathProjects + "/" + itoa(id)
}

// APIPathProjectInjections returns the injections sub-resource path for a project.
func APIPathProjectInjections(projectID int) string {
	return APIPathProject(projectID) + "/injections"
}

// APIPathProjectInjectionsInject returns the inject sub-path for a project.
func APIPathProjectInjectionsInject(projectID int) string {
	return APIPathProjectInjections(projectID) + "/inject"
}

// APIPathProjectInjectionsSearch returns the injections search sub-path for a project.
func APIPathProjectInjectionsSearch(projectID int) string {
	return APIPathProjectInjections(projectID) + "/search"
}

// APIPathProjectExecutions returns the executions sub-resource path for a project.
func APIPathProjectExecutions(projectID int) string {
	return APIPathProject(projectID) + "/executions"
}

// APIPathProjectExecutionsExecute returns the execute sub-path for a project.
func APIPathProjectExecutionsExecute(projectID int) string {
	return APIPathProjectExecutions(projectID) + "/execute"
}

// APIPathSystem returns the path for a single system by ID.
func APIPathSystem(id int) string {
	return APIPathSystems + "/" + itoa(id)
}

// APIPathSystemByNameChart returns the chart sub-path for a named system.
func APIPathSystemByNameChart(name string) string {
	return APIPathSystemsByName + "/" + name + "/chart"
}

// APIPathSystemByNameInjectCandidates returns the inject-candidates sub-path for a named system.
func APIPathSystemByNameInjectCandidates(name string) string {
	return APIPathSystemsByName + "/" + name + "/inject-candidates"
}

// APIPathSystemByNamePrerequisites returns the prerequisites sub-path for a named system.
func APIPathSystemByNamePrerequisites(name string) string {
	return APIPathSystemsByName + "/" + name + "/prerequisites"
}

// APIPathTask returns the path for a single task by ID.
func APIPathTask(id string) string {
	return APIPathTasks + "/" + id
}

// APIPathTaskExpedite returns the expedite sub-path for a task.
func APIPathTaskExpedite(id string) string {
	return APIPathTask(id) + "/expedite"
}

// APIPathTaskLogsWS returns the websocket logs sub-path for a task.
func APIPathTaskLogsWS(id string) string {
	return APIPathTask(id) + "/logs/ws"
}

// APIPathTrace returns the path for a single trace by ID.
func APIPathTrace(id string) string {
	return APIPathTraces + "/" + id
}

// APIPathTraceCancel returns the cancel sub-path for a trace.
func APIPathTraceCancel(id string) string {
	return APIPathTrace(id) + "/cancel"
}

// APIPathTraceStream returns the stream sub-path for a trace.
func APIPathTraceStream(id string) string {
	return APIPathTrace(id) + "/stream"
}

// APIPathPedestalHelmByID returns the pedestal helm path for a system ID.
func APIPathPedestalHelmByID(systemID int) string {
	return APIPathPedestalHelm + "/" + itoa(systemID)
}

// APIPathPedestalHelmReseed returns the reseed sub-path for a pedestal helm system.
func APIPathPedestalHelmReseed(systemID int) string {
	return APIPathPedestalHelmByID(systemID) + "/reseed"
}

// APIPathPedestalHelmVerify returns the verify sub-path for a pedestal helm system.
func APIPathPedestalHelmVerify(systemID int) string {
	return APIPathPedestalHelmByID(systemID) + "/verify"
}

// itoa is a tiny local helper to avoid pulling strconv into every caller of
// the path builders above. Negative IDs are not expected; the package callers
// validate input before building paths.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
