package client

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"aegis/consts"
)

// Resolver resolves human-readable names (e.g. "train-ticket") to numeric IDs
// by calling the list APIs. Results are cached in memory for the lifetime of the
// Resolver instance.
type Resolver struct {
	client    *Client
	cache     map[string]int // "project:train-ticket" -> 42
	projectID int            // set by SetProjectScope; 0 means unset
}

// NewResolver creates a Resolver backed by the given Client.
func NewResolver(c *Client) *Resolver {
	return &Resolver{
		client: c,
		cache:  make(map[string]int),
	}
}

// Minimal item structs used for list-API deserialization.

type projectItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type injectionItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type containerItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type datasetItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type notFoundResolution struct {
	Type        string   `json:"type"`
	Resource    string   `json:"resource"`
	Query       string   `json:"query"`
	ProjectID   int      `json:"project_id,omitempty"`
	Suggestions []string `json:"suggestions"`
}

// NotFoundError is returned when a resolver cannot find a resource by name.
// It is intentionally JSON-serializable so stderr can print machine-readable
// clues like `type`, `query`, and nearest suggestions.
type NotFoundError struct {
	Payload notFoundResolution
}

func (e *NotFoundError) Error() string {
	b, err := json.Marshal(e.Payload)
	if err != nil {
		return "not found"
	}
	return string(b)
}

// resolve is the generic resolution helper. It calls the list endpoint, finds
// items matching the given name, and caches the result. The endpoint is
// paginated automatically (page=1..N, size=100) until the name is found, the
// result set is exhausted, or maxResolvePages pages have been scanned (a
// safety cap that prevents pathological iteration on huge collections).
func resolve[T any](r *Resolver, kind, basePath, name string, extract func(T) (int, string)) (int, error) {
	cacheKey := kind + ":" + name
	if id, ok := r.cache[cacheKey]; ok {
		return id, nil
	}

	const pageSize = 100
	const maxResolvePages = 100 // hard cap: 10 000 items
	for page := 1; page <= maxResolvePages; page++ {
		path := fmt.Sprintf("%s?page=%d&size=%d", basePath, page, pageSize)
		var resp APIResponse[PaginatedData[T]]
		if err := r.client.Get(path, &resp); err != nil {
			return 0, fmt.Errorf("resolve %s %q: %w", kind, name, err)
		}
		for _, item := range resp.Data.Items {
			id, itemName := extract(item)
			if itemName == name {
				r.cache[cacheKey] = id
				return id, nil
			}
		}
		if len(resp.Data.Items) < pageSize {
			break
		}
	}
	return 0, fmt.Errorf("resolve %s %q: not found", kind, name)
}

func nearestSuggestions(query string, candidates []string, limit int) []string {
	if len(candidates) == 0 || limit <= 0 {
		return nil
	}

	type scoredItem struct {
		value    string
		distance int
	}

	scoredItems := make([]scoredItem, 0, len(candidates))
	for _, c := range candidates {
		scoredItems = append(scoredItems, scoredItem{value: c, distance: levenshtein(query, c)})
	}

	sort.Slice(scoredItems, func(i, j int) bool {
		if scoredItems[i].distance == scoredItems[j].distance {
			return scoredItems[i].value < scoredItems[j].value
		}
		return scoredItems[i].distance < scoredItems[j].distance
	})

	if len(scoredItems) > limit {
		scoredItems = scoredItems[:limit]
	}

	out := make([]string, 0, len(scoredItems))
	for _, s := range scoredItems {
		out = append(out, s.value)
	}
	return out
}

func levenshtein(a, b string) int {
	aLen := len(a)
	bLen := len(b)

	if aLen == 0 {
		return bLen
	}
	if bLen == 0 {
		return aLen
	}

	dp := make([]int, bLen+1)
	next := make([]int, bLen+1)
	for j := 0; j <= bLen; j++ {
		dp[j] = j
	}

	for i := 1; i <= aLen; i++ {
		next[0] = i
		ai := a[i-1]
		for j := 1; j <= bLen; j++ {
			cost := 0
			if ai != b[j-1] {
				cost = 1
			}
			insert := next[j-1] + 1
			delete := dp[j] + 1
			replace := dp[j-1] + cost
			next[j] = min3(insert, delete, replace)
		}
		dp, next = next, dp
	}

	return dp[bLen]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// ProjectID resolves a project name to its numeric ID.
func (r *Resolver) ProjectID(name string) (int, error) {
	return resolve(r, "project", consts.APIPathProjects, name,
		func(p projectItem) (int, string) { return p.ID, p.Name })
}

// InjectionID resolves an injection name to its numeric ID. Injections live
// under a project, so the caller must resolve a project first via
// SetProjectScope before calling this.
func (r *Resolver) InjectionID(name string) (int, error) {
	if id, err := strconv.Atoi(name); err == nil && id > 0 {
		return id, nil
	}
	if r.projectID == 0 {
		return 0, fmt.Errorf("resolve injection %q: project scope not set (call SetProjectScope or pass --project)", name)
	}

	cacheKey := fmt.Sprintf("injection:%d:%s", r.projectID, name)
	if id, ok := r.cache[cacheKey]; ok {
		return id, nil
	}

	basePath := consts.APIPathProjectInjections(r.projectID)
	const pageSize = 100
	const maxResolvePages = 100

	allNames := make([]string, 0, pageSize)

	for page := 1; page <= maxResolvePages; page++ {
		path := fmt.Sprintf("%s?page=%d&size=%d", basePath, page, pageSize)
		var resp APIResponse[PaginatedData[injectionItem]]
		if err := r.client.Get(path, &resp); err != nil {
			return 0, fmt.Errorf("resolve %s %q: %w", "injection", name, err)
		}
		for _, item := range resp.Data.Items {
			id, itemName := item.ID, item.Name
			allNames = append(allNames, itemName)
			if itemName == name {
				r.cache[cacheKey] = id
				return id, nil
			}
		}
		if len(resp.Data.Items) < pageSize {
			break
		}
	}

	return 0, &NotFoundError{Payload: notFoundResolution{
		Type:        "not_found",
		Resource:    "injection",
		Query:       name,
		ProjectID:   r.projectID,
		Suggestions: nearestSuggestions(name, allNames, 3),
	}}
}

// SetProjectScope tells the resolver which project to scope project-scoped
// lookups (currently only injections) to. It is idempotent.
func (r *Resolver) SetProjectScope(projectID int) {
	r.projectID = projectID
}

// ContainerID resolves a container name to its numeric ID.
func (r *Resolver) ContainerID(name string) (int, error) {
	return resolve(r, "container", consts.APIPathContainers, name,
		func(c containerItem) (int, string) { return c.ID, c.Name })
}

// DatasetID resolves a dataset name to its numeric ID.
func (r *Resolver) DatasetID(name string) (int, error) {
	return resolve(r, "dataset", consts.APIPathDatasets, name,
		func(d datasetItem) (int, string) { return d.ID, d.Name })
}

// resolveByID looks up a resource by its numeric ID via GET /{path}/{id} and
// returns its name. The response is expected to conform to APIResponse[T] where
// T contains `id` and `name` fields.
func resolveByID[T any](r *Resolver, path string, id int, extract func(T) (int, string)) (string, error) {
	var resp APIResponse[T]
	if err := r.client.Get(fmt.Sprintf("%s/%d", path, id), &resp); err != nil {
		return "", err
	}
	_, name := extract(resp.Data)
	if name == "" {
		return "", fmt.Errorf("resource %d has no name", id)
	}
	return name, nil
}

// ProjectIDOrName accepts either a numeric ID or a project name and returns
// both the numeric ID and the project name.
func (r *Resolver) ProjectIDOrName(arg string) (int, string, error) {
	if id, err := strconv.Atoi(arg); err == nil && id > 0 {
		name, err := resolveByID(r, consts.APIPathProjects, id,
			func(p projectItem) (int, string) { return p.ID, p.Name })
		if err != nil {
			return 0, "", err
		}
		return id, name, nil
	}
	id, err := r.ProjectID(arg)
	if err != nil {
		return 0, "", err
	}
	return id, arg, nil
}

// ContainerIDOrName accepts either a numeric ID or a container name and
// returns both the numeric ID and the container name.
func (r *Resolver) ContainerIDOrName(arg string) (int, string, error) {
	if id, err := strconv.Atoi(arg); err == nil && id > 0 {
		name, err := resolveByID(r, consts.APIPathContainers, id,
			func(c containerItem) (int, string) { return c.ID, c.Name })
		if err != nil {
			return 0, "", err
		}
		return id, name, nil
	}
	id, err := r.ContainerID(arg)
	if err != nil {
		return 0, "", err
	}
	return id, arg, nil
}

// DatasetIDOrName accepts either a numeric ID or a dataset name and returns
// both the numeric ID and the dataset name.
func (r *Resolver) DatasetIDOrName(arg string) (int, string, error) {
	if id, err := strconv.Atoi(arg); err == nil && id > 0 {
		name, err := resolveByID(r, consts.APIPathDatasets, id,
			func(d datasetItem) (int, string) { return d.ID, d.Name })
		if err != nil {
			return 0, "", err
		}
		return id, name, nil
	}
	id, err := r.DatasetID(arg)
	if err != nil {
		return 0, "", err
	}
	return id, arg, nil
}
