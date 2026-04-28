package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"aegis/cmd/aegisctl/client"
)

const (
	streamAllPageSize = 100
	streamAllMaxPages = 1000
)

// streamListAllNDJSON pages basePath at size=streamAllPageSize until the result
// set is exhausted, writing one compact-JSON line per item to stdout. Existing
// filter params should be supplied via baseParams; "page" and "size" entries
// are overwritten on each iteration.
//
// Stops with an error if more than streamAllMaxPages are scanned (safety cap
// against runaway iteration). Pagination metadata is intentionally not emitted
// — under --all the caller wants a flat stream of records.
func streamListAllNDJSON[T any](c *client.Client, basePath string, baseParams map[string]string) error {
	return streamListAllNDJSONFiltered[T](c, basePath, baseParams, nil)
}

// streamListAllNDJSONFiltered is streamListAllNDJSON with an optional
// per-item predicate applied client-side after each page is fetched. Items
// for which keep returns false are dropped silently. Termination still
// follows server-side pagination (a short page ends iteration even if all
// items were filtered out).
func streamListAllNDJSONFiltered[T any](c *client.Client, basePath string, baseParams map[string]string, keep func(T) bool) error {
	if baseParams == nil {
		baseParams = map[string]string{}
	}
	baseParams["size"] = strconv.Itoa(streamAllPageSize)

	enc := json.NewEncoder(os.Stdout)
	for page := 1; page <= streamAllMaxPages; page++ {
		baseParams["page"] = strconv.Itoa(page)
		path := basePath
		if q := buildQueryParams(baseParams); q != "" {
			path += "?" + q
		}

		var resp client.APIResponse[client.PaginatedData[T]]
		if err := c.Get(path, &resp); err != nil {
			return err
		}
		for _, item := range resp.Data.Items {
			if keep != nil && !keep(item) {
				continue
			}
			if err := enc.Encode(item); err != nil {
				return err
			}
		}
		if len(resp.Data.Items) < streamAllPageSize {
			return nil
		}
	}
	return fmt.Errorf("--all stopped at safety cap of %d pages (%d records); narrow filters or paginate manually",
		streamAllMaxPages, streamAllMaxPages*streamAllPageSize)
}
