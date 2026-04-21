package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
)

// ---------- helpers ----------

func requireProjectName() (string, error) {
	if flagProject == "" {
		return "", usageErrorf("--project is required")
	}
	return flagProject, nil
}

func resolveProjectIDByName() (int, error) {
	name, err := requireProjectName()
	if err != nil {
		return 0, err
	}
	return newResolver().ProjectID(name)
}

// ---------- inject root ----------

var injectCmd = &cobra.Command{
	Use:   "inject",
	Short: "Manage fault injections",
	Long: `Manage fault injections in AegisLab projects.

The canonical submission path is the guided flow:

  # Step through a guided session; apply when the config is ready
  aegisctl inject guided --reset-config --no-save-config
  aegisctl inject guided --next otel-demo0 --next frontend
  aegisctl inject guided --apply \
    --project pair_diagnosis \
    --pedestal-name ts --pedestal-tag 1.0.0 \
    --benchmark-name otel-demo-bench --benchmark-tag 1.0.0 \
    --interval 10 --pre-duration 5

Read-only / listing commands:

  aegisctl inject list --project pair_diagnosis
  aegisctl inject get <injection-name>
  aegisctl inject search --name-pattern "cpu*" --project pair_diagnosis
  aegisctl inject files <injection-name>
  aegisctl inject download <injection-name> --output-file ./output.tar.gz

NOTE: --project is required for list, search, and guided --apply.
      It accepts project names (resolved to IDs automatically).`,
}

// ---------- inject list ----------

var (
	injectListState     string
	injectListFaultType string
	injectListLabels    string
	injectListPage      int
	injectListSize      int
)

var injectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List fault injections in a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := resolveProjectIDByName()
		if err != nil {
			return err
		}

		c := newClient()
		q := fmt.Sprintf("/api/v2/projects/%d/injections?page=%d&size=%d", pid, injectListPage, injectListSize)
		if injectListState != "" {
			q += "&state=" + injectListState
		}
		if injectListFaultType != "" {
			q += "&fault_type=" + injectListFaultType
		}
		if injectListLabels != "" {
			q += "&labels=" + injectListLabels
		}

		type listItem struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			State     string `json:"state"`
			FaultType string `json:"fault_type"`
			StartTime string `json:"start_time"`
			Labels    []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"labels"`
		}

		var resp client.APIResponse[client.PaginatedData[listItem]]
		if err := c.Get(q, &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		headers := []string{"NAME", "STATE", "FAULT-TYPE", "START-TIME", "LABELS"}
		var rows [][]string
		for _, item := range resp.Data.Items {
			var lbls []string
			for _, l := range item.Labels {
				lbls = append(lbls, l.Key+"="+l.Value)
			}
			rows = append(rows, []string{
				item.Name,
				item.State,
				item.FaultType,
				item.StartTime,
				strings.Join(lbls, ","),
			})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

// ---------- inject get ----------

var injectGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get detailed info about an injection",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := newResolver()
		id, err := r.InjectionID(args[0])
		if err != nil {
			return err
		}

		c := newClient()
		var resp client.APIResponse[any]
		if err := c.Get(fmt.Sprintf("/api/v2/injections/%d", id), &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		if resp.Data == nil {
			return nil
		}

		data, ok := resp.Data.(map[string]any)
		if !ok {
			// Fallback: unknown shape, just JSON it.
			output.PrintJSON(resp.Data)
			return nil
		}

		preferredOrder := []string{
			"name", "id", "state", "fault_type",
			"start_time", "end_time", "project_id", "display_config",
		}
		seen := map[string]bool{}
		headers := []string{"FIELD", "VALUE"}
		var rows [][]string

		appendRow := func(k string) {
			v, exists := data[k]
			if !exists {
				return
			}
			rows = append(rows, []string{k, formatInjectGetValue(v)})
			seen[k] = true
		}

		for _, k := range preferredOrder {
			appendRow(k)
		}

		// Append any remaining scalar keys in sorted order.
		var remaining []string
		for k, v := range data {
			if seen[k] {
				continue
			}
			switch v.(type) {
			case string, float64, float32, int, int32, int64, bool, nil:
				remaining = append(remaining, k)
			}
		}
		sort.Strings(remaining)
		for _, k := range remaining {
			appendRow(k)
		}

		output.PrintTable(headers, rows)
		return nil
	},
}

func formatInjectGetValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return fmt.Sprintf("%t", x)
	case float64:
		// Render whole numbers without decimal noise.
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// ---------- inject search ----------

var (
	injectSearchNamePattern string
	injectSearchLabels      string
)

var injectSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search injections in a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := resolveProjectIDByName()
		if err != nil {
			return err
		}

		body := map[string]any{}
		if injectSearchNamePattern != "" {
			body["name_pattern"] = injectSearchNamePattern
		}
		if injectSearchLabels != "" {
			body["labels"] = injectSearchLabels
		}

		c := newClient()
		var resp client.APIResponse[any]
		if err := c.Post(fmt.Sprintf("/api/v2/projects/%d/injections/search", pid), body, &resp); err != nil {
			return err
		}

		output.PrintJSON(resp.Data)
		return nil
	},
}

// ---------- inject files ----------

var injectFilesCmd = &cobra.Command{
	Use:   "files <name>",
	Short: "List files produced by an injection",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := newResolver()
		id, err := r.InjectionID(args[0])
		if err != nil {
			return err
		}

		type fileItem struct {
			Path string `json:"path"`
			Size string `json:"size"`
			Type string `json:"type"`
		}

		c := newClient()
		var resp client.APIResponse[[]fileItem]
		if err := c.Get(fmt.Sprintf("/api/v2/injections/%d/files", id), &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		headers := []string{"PATH", "SIZE", "TYPE"}
		var rows [][]string
		for _, f := range resp.Data {
			rows = append(rows, []string{f.Path, f.Size, f.Type})
		}
		output.PrintTable(headers, rows)
		return nil
	},
}

// ---------- inject download ----------

var injectDownloadOutput string

var injectDownloadCmd = &cobra.Command{
	Use:   "download <name>",
	Short: "Download injection artifacts to a file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if injectDownloadOutput == "" {
			return usageErrorf("--output-file <path> is required")
		}

		r := newResolver()
		id, err := r.InjectionID(args[0])
		if err != nil {
			return err
		}

		// Build a raw HTTP request (binary download, not JSON).
		url := flagServer + fmt.Sprintf("/api/v2/injections/%d/download", id)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		if flagToken != "" {
			req.Header.Set("Authorization", "Bearer "+flagToken)
		}

		httpClient := &http.Client{Timeout: time.Duration(flagRequestTimeout) * time.Second}
		resp, err := httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("download request failed: %w", err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("download failed (HTTP %d): %s", resp.StatusCode, string(body))
		}

		f, err := os.Create(injectDownloadOutput)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer func() {
			_ = f.Close()
		}()

		n, err := io.Copy(f, resp.Body)
		if err != nil {
			return fmt.Errorf("write output file: %w", err)
		}

		output.PrintInfo(fmt.Sprintf("Downloaded %d bytes to %s", n, injectDownloadOutput))
		return nil
	},
}

// ---------- init ----------

func init() {
	injectListCmd.Flags().StringVar(&injectListState, "state", "", "Filter by state")
	injectListCmd.Flags().StringVar(&injectListFaultType, "fault-type", "", "Filter by fault type")
	injectListCmd.Flags().StringVar(&injectListLabels, "labels", "", "Filter by labels (key=val,...)")
	injectListCmd.Flags().IntVar(&injectListPage, "page", 1, "Page number")
	injectListCmd.Flags().IntVar(&injectListSize, "size", 20, "Page size")

	injectSearchCmd.Flags().StringVar(&injectSearchNamePattern, "name-pattern", "", "Name pattern to search for")
	injectSearchCmd.Flags().StringVar(&injectSearchLabels, "labels", "", "Labels to filter (key=val,...)")

	injectDownloadCmd.Flags().StringVar(&injectDownloadOutput, "output-file", "", "Output file path")

	injectCmd.AddCommand(injectListCmd)
	injectCmd.AddCommand(injectGetCmd)
	injectCmd.AddCommand(injectSearchCmd)
	injectCmd.AddCommand(injectFilesCmd)
	injectCmd.AddCommand(injectDownloadCmd)
}
