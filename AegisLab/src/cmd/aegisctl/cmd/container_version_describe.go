package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"
	"aegis/consts"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// containerVersionDescribe types mirror the backend
// ContainerVersionDetailResp (see module/container/api_types.go). Fields not
// needed for the round-trip view (e.g. usage, updated_at) are deliberately
// omitted from the text rendering but remain present in JSON/YAML modes.
type helmConfigDescribe struct {
	ID        int            `json:"id" yaml:"id"`
	Version   string         `json:"version" yaml:"version"`
	ChartName string         `json:"chart_name" yaml:"chart_name"`
	RepoName  string         `json:"repo_name" yaml:"repo_name"`
	RepoURL   string         `json:"repo_url" yaml:"repo_url"`
	LocalPath string         `json:"local_path,omitempty" yaml:"local_path,omitempty"`
	ValueFile string         `json:"value_file,omitempty" yaml:"value_file,omitempty"`
	Values    map[string]any `json:"values,omitempty" yaml:"values,omitempty"`
}

type containerVersionDescribe struct {
	ID         int                 `json:"id" yaml:"id"`
	Name       string              `json:"name" yaml:"name"`
	ImageRef   string              `json:"image_ref" yaml:"image_ref"`
	Usage      int                 `json:"usage" yaml:"usage"`
	UpdatedAt  string              `json:"updated_at" yaml:"updated_at"`
	GithubLink string              `json:"github_link" yaml:"github_link"`
	Command    string              `json:"command" yaml:"command"`
	// EnvVars in the backend response is serialized as a JSON-encoded string
	// (see ContainerVersionDetailResp.EnvVars). We accept either a string or a
	// structured array and always render it as a structured array to callers.
	EnvVars    json.RawMessage     `json:"env_vars,omitempty" yaml:"-"`
	HelmConfig *helmConfigDescribe `json:"helm_config,omitempty" yaml:"helm_config,omitempty"`

	// Parent container name — populated client-side for human-readable output.
	ContainerName string `json:"container_name,omitempty" yaml:"container_name,omitempty"`
	// EnvVarsParsed holds the decoded env_vars payload for YAML output and for
	// text rendering. JSON output keeps the raw server representation.
	EnvVarsParsed []map[string]any `json:"-" yaml:"env_vars,omitempty"`
	// Status is surfaced by the parent container; it's not on the version
	// response, so we fill it from the container detail call.
	Status string `json:"status,omitempty" yaml:"status,omitempty"`
}

const noneSentinel = "<none>"

var containerVersionDescribeFormat string

var containerVersionDescribeCmd = &cobra.Command{
	Use:   "describe <container-name-or-id> <version-name-or-id>",
	Short: "Describe a container version (helm_config, image_ref, env_vars)",
	Long: `Show the full helm_config / image_ref / env_vars triad for a container
version. This is the round-trip of "container register" — use it to inspect why
a pedestal points at a particular chart/image.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()
		r := client.NewResolver(c)

		cid, cname, err := r.ContainerIDOrName(args[0])
		if err != nil {
			return notFoundErrorf("container %q not found: %v", args[0], err)
		}

		// Fetch container detail to resolve version name-or-id and pick up
		// status (not returned by the version detail endpoint).
		var ctrResp client.APIResponse[containerDetail]
		if err := c.Get(consts.APIPathContainer(cid), &ctrResp); err != nil {
			return fmt.Errorf("failed to fetch container detail: %w", err)
		}
		vid, err := resolveContainerVersionID(ctrResp.Data.Versions, args[1])
		if err != nil {
			return notFoundErrorf("container version %q not found on container %q: %v", args[1], cname, err)
		}

		var vResp client.APIResponse[containerVersionDescribe]
		if err := c.Get(consts.APIPathContainerVersion(cid, vid), &vResp); err != nil {
			return fmt.Errorf("failed to fetch container version: %w", err)
		}

		desc := vResp.Data
		desc.ContainerName = cname
		desc.Status = ctrResp.Data.Status
		desc.EnvVarsParsed = parseEnvVars(desc.EnvVars)

		return renderContainerVersionDescribe(os.Stdout, &desc, containerVersionDescribeFormat)
	},
}

// resolveContainerVersionID accepts a numeric id or a version name and returns
// the numeric id by searching the container's version list.
func resolveContainerVersionID(versions []containerVersionItem, arg string) (int, error) {
	if id, err := strconv.Atoi(arg); err == nil && id > 0 {
		for _, v := range versions {
			if v.ID == id {
				return id, nil
			}
		}
		return 0, fmt.Errorf("no version with id %d", id)
	}
	for _, v := range versions {
		if v.Name == arg {
			return v.ID, nil
		}
	}
	return 0, fmt.Errorf("no version named %q", arg)
}

// parseEnvVars tolerates both JSON-encoded strings and structured arrays.
// Returns nil on empty/absent/invalid input so the text renderer can degrade
// gracefully.
func parseEnvVars(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == `""` || trimmed == "null" {
		return nil
	}

	// Try structured array first.
	var asArray []map[string]any
	if err := json.Unmarshal(raw, &asArray); err == nil {
		return asArray
	}
	// Then try JSON-encoded string containing a JSON array.
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		asString = strings.TrimSpace(asString)
		if asString == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(asString), &asArray); err == nil {
			return asArray
		}
	}
	return nil
}

func renderContainerVersionDescribe(w *os.File, desc *containerVersionDescribe, format string) error {
	f := strings.ToLower(strings.TrimSpace(format))
	if f == "" {
		// Fall back to global --output when --format is not set.
		f = string(output.OutputFormat(flagOutput))
	}
	switch f {
	case "json":
		data, err := json.MarshalIndent(desc, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
		return nil
	case "yaml", "yml":
		data, err := yaml.Marshal(desc)
		if err != nil {
			return err
		}
		fmt.Fprint(w, string(data))
		return nil
	default:
		return renderContainerVersionDescribeText(w, desc)
	}
}

func renderContainerVersionDescribeText(w *os.File, desc *containerVersionDescribe) error {
	fmt.Fprintf(w, "Name:        %s\n", stringOrNone(desc.ContainerName))
	fmt.Fprintf(w, "Version:     %s\n", stringOrNone(desc.Name))
	fmt.Fprintf(w, "Version ID:  %d\n", desc.ID)
	if s := strings.TrimSpace(desc.Status); s != "" {
		fmt.Fprintf(w, "Status:      %s\n", s)
	}
	if s := strings.TrimSpace(desc.ImageRef); s != "" {
		fmt.Fprintf(w, "Image Ref:   %s\n", s)
	}
	fmt.Fprintf(w, "GitHub Link: %s\n", stringOrNone(desc.GithubLink))
	if s := strings.TrimSpace(desc.Command); s != "" {
		fmt.Fprintf(w, "Command:     %s\n", s)
	}

	fmt.Fprintln(w, "Helm Config:")
	if desc.HelmConfig == nil {
		fmt.Fprintf(w, "  %s\n", noneSentinel)
	} else {
		h := desc.HelmConfig
		fmt.Fprintf(w, "  Chart Name: %s\n", stringOrNone(h.ChartName))
		fmt.Fprintf(w, "  Version:    %s\n", stringOrNone(h.Version))
		fmt.Fprintf(w, "  Repo Name:  %s\n", stringOrNone(h.RepoName))
		fmt.Fprintf(w, "  Repo URL:   %s\n", stringOrNone(h.RepoURL))
		if s := strings.TrimSpace(h.LocalPath); s != "" {
			fmt.Fprintf(w, "  Local Path: %s\n", s)
		}
		if s := strings.TrimSpace(h.ValueFile); s != "" {
			fmt.Fprintf(w, "  Value File: %s\n", s)
		}
		fmt.Fprintln(w, "  Values:")
		if len(h.Values) == 0 {
			fmt.Fprintf(w, "    %s\n", noneSentinel)
		} else {
			keys := make([]string, 0, len(h.Values))
			for k := range h.Values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(w, "    %s: %v\n", k, h.Values[k])
			}
		}
	}

	fmt.Fprintln(w, "Env Vars:")
	if len(desc.EnvVarsParsed) == 0 {
		fmt.Fprintf(w, "  %s\n", noneSentinel)
	} else {
		for _, ev := range desc.EnvVarsParsed {
			key, _ := ev["key"].(string)
			if key == "" {
				// Fallback — dump the raw entry.
				fmt.Fprintf(w, "  - %v\n", ev)
				continue
			}
			extras := []string{}
			if v, ok := ev["type"]; ok {
				extras = append(extras, fmt.Sprintf("type=%v", v))
			}
			if v, ok := ev["required"]; ok {
				extras = append(extras, fmt.Sprintf("required=%v", v))
			}
			if v, ok := ev["default_value"]; ok && v != nil {
				extras = append(extras, fmt.Sprintf("default=%v", v))
			}
			if len(extras) == 0 {
				fmt.Fprintf(w, "  - %s\n", key)
			} else {
				fmt.Fprintf(w, "  - %s (%s)\n", key, strings.Join(extras, ", "))
			}
		}
	}
	return nil
}

func stringOrNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return noneSentinel
	}
	return s
}
