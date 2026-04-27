package client

import (
	"fmt"
	"strconv"
	"strings"
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
	sep := "?"
	if strings.Contains(basePath, "?") {
		sep = "&"
	}

	for page := 1; page <= maxResolvePages; page++ {
		path := fmt.Sprintf("%s%spage=%d&size=%d", basePath, sep, page, pageSize)
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

// ProjectID resolves a project name to its numeric ID.
func (r *Resolver) ProjectID(name string) (int, error) {
	return resolve(r, "project", "/api/v2/projects", name,
		func(p projectItem) (int, string) { return p.ID, p.Name })
}

// InjectionID resolves an injection name to its numeric ID. Injections live
// under a project, so the caller must resolve a project first via
// SetProjectScope before calling this.
func (r *Resolver) InjectionID(name string) (int, error) {
	if r.projectID == 0 {
		return 0, fmt.Errorf("resolve injection %q: project scope not set (call SetProjectScope or pass --project)", name)
	}
	return resolve(r, "injection",
		fmt.Sprintf("/api/v2/projects/%d/injections", r.projectID),
		name, func(i injectionItem) (int, string) { return i.ID, i.Name })
}

// SetProjectScope tells the resolver which project to scope project-scoped
// lookups (currently only injections) to. It is idempotent.
func (r *Resolver) SetProjectScope(projectID int) {
	r.projectID = projectID
}

// ContainerID resolves a container name to its numeric ID.
func (r *Resolver) ContainerID(name string) (int, error) {
	return resolve(r, "container", "/api/v2/containers", name,
		func(c containerItem) (int, string) { return c.ID, c.Name })
}

// DatasetID resolves a dataset name to its numeric ID.
func (r *Resolver) DatasetID(name string) (int, error) {
	return resolve(r, "dataset", "/api/v2/datasets", name,
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
		name, err := resolveByID(r, "/api/v2/projects", id,
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
		name, err := resolveByID(r, "/api/v2/containers", id,
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
		name, err := resolveByID(r, "/api/v2/datasets", id,
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
