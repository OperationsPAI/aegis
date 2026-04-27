package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"
	"aegis/consts"

	"github.com/spf13/cobra"
)

func urlQueryEscape(s string) string { return url.QueryEscape(s) }
func bufioScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return sc
}

// resolveDatapackStateFlag accepts either a numeric state (e.g. "6") or a
// symbolic name (e.g. "detector_success") and returns the numeric form expected
// by the API.
func resolveDatapackStateFlag(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if _, err := strconv.Atoi(raw); err == nil {
		return raw, nil
	}
	st := consts.GetDatapackStateByName(raw)
	if st == nil {
		return "", fmt.Errorf("invalid --state %q; valid values: %s", raw, datapackStateFlagHelp())
	}
	return strconv.Itoa(int(*st)), nil
}

func datapackStateFlagHelp() string {
	names := make([]string, 0, len(consts.ValidDatapackStates))
	for s := range consts.ValidDatapackStates {
		names = append(names, fmt.Sprintf("%s (%d)", consts.GetDatapackStateName(s), int(s)))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func pageSizeFlagHelp() string {
	sizes := make([]int, 0, len(consts.ValidPageSizes))
	for s := range consts.ValidPageSizes {
		sizes = append(sizes, int(s))
	}
	sort.Ints(sizes)
	parts := make([]string, len(sizes))
	for i, s := range sizes {
		parts[i] = strconv.Itoa(s)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

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

// newProjectScopedResolver builds a resolver that already knows the current
// --project, so InjectionID lookups go to the project-scoped list endpoint.
func newProjectScopedResolver() (*client.Resolver, error) {
	pid, err := resolveProjectIDByName()
	if err != nil {
		return nil, err
	}
	r := newResolver()
	r.SetProjectScope(pid)
	return r, nil
}

func resolveInjectionID(arg string) (int, error) {
	r, err := newProjectScopedResolver()
	if err != nil {
		return 0, err
	}
	return r.InjectionID(arg)
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

		stateParam, err := resolveDatapackStateFlag(injectListState)
		if err != nil {
			return err
		}

		c := newClient()
		q := fmt.Sprintf("/api/v2/projects/%d/injections?page=%d&size=%d", pid, injectListPage, injectListSize)
		if stateParam != "" {
			q += "&state=" + stateParam
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
		if output.OutputFormat(flagOutput) == output.FormatNDJSON {
			for _, item := range resp.Data.Items {
				data, err := json.Marshal(item)
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, string(data))
			}
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
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStdinItems("inject get", "inject get <name>", args, stdinOptions{
			enabled:  injectGetStdin,
			field:    injectGetStdinField,
			failFast: injectGetStdinFailFast,
		}, runInjectGet)
	},
}

var (
	injectGetStdin         bool
	injectGetStdinField    string
	injectGetStdinFailFast bool
)

func runInjectGet(name string) error {
		id, err := resolveInjectionID(name)
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
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStdinItems("inject files", "inject files <name>", args, stdinOptions{
			enabled:  injectFilesStdin,
			field:    injectFilesStdinField,
			failFast: injectFilesStdinFailFast,
		}, runInjectFiles)
	},
}

var (
	injectFilesStdin         bool
	injectFilesStdinField    string
	injectFilesStdinFailFast bool
)

func runInjectFiles(name string) error {
		id, err := resolveInjectionID(name)
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
}

// ---------- inject download ----------

var (
	injectDownloadOutput        string
	injectDownloadDir           string
	injectDownloadInclude       string
	injectDownloadFilePar       int
	injectDownloadTimeout       int
	injectDownloadStdin         bool
	injectDownloadStdinField    string
	injectDownloadStdinFailFast bool
)

const (
	includeConverted = "converted"
	includeRaw       = "raw"
	includeAll       = "all"
)

func injectIncludeFlagHelp() string {
	return "{converted, raw, all}"
}

// validateIncludeFlag checks --include and returns it normalized.
func validateIncludeFlag(raw string) (string, error) {
	switch raw {
	case includeConverted, includeRaw, includeAll:
		return raw, nil
	case "":
		return includeConverted, nil
	default:
		return "", fmt.Errorf("invalid --include %q; valid values: %s", raw, injectIncludeFlagHelp())
	}
}

// pathMatchesInclude reports whether the given relative path inside a datapack
// should be downloaded under the given include mode.
func pathMatchesInclude(path, include string) bool {
	switch include {
	case includeAll:
		return true
	case includeConverted:
		return strings.HasPrefix(path, "converted/")
	case includeRaw:
		return !strings.HasPrefix(path, "converted/")
	default:
		return false
	}
}

type datapackFileEntry struct {
	Path     string              `json:"path"`
	Children []datapackFileEntry `json:"children,omitempty"`
}

// listInjectionFiles fetches the datapack file tree for the given injection
// and returns the flattened list of leaf file paths (directories are skipped).
func listInjectionFiles(c *client.Client, id int) ([]string, error) {
	type fileTreeResp struct {
		Files []datapackFileEntry `json:"files"`
	}
	var resp client.APIResponse[fileTreeResp]
	if err := c.Get(fmt.Sprintf("/api/v2/injections/%d/files", id), &resp); err != nil {
		return nil, err
	}
	var out []string
	var walk func(items []datapackFileEntry)
	walk = func(items []datapackFileEntry) {
		for _, it := range items {
			if len(it.Children) > 0 {
				walk(it.Children)
			} else {
				out = append(out, it.Path)
			}
		}
	}
	walk(resp.Data.Files)
	return out, nil
}

// downloadInjectionFile streams a single datapack file to disk. Parent
// directories under outDir are created on demand.
func downloadInjectionFile(httpClient *http.Client, server, token string, id int, relPath, outPath string) error {
	if err := os.MkdirAll(filepathDir(outPath), 0o755); err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v2/injections/%d/files/download?path=%s", server, id, queryEscape(relPath))
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// downloadPackToDir downloads a single injection's datapack into outDir,
// honoring the include filter and per-file parallelism. A `.done` marker is
// written under outDir on success so subsequent calls can short-circuit.
func downloadPackToDir(httpClient *http.Client, server, token string, id int, name, outDir, include string, fileParallelism int) error {
	c := newClient()
	files, err := listInjectionFiles(c, id)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}

	var wanted []string
	for _, p := range files {
		if pathMatchesInclude(p, include) {
			wanted = append(wanted, p)
		}
	}
	if len(wanted) == 0 {
		// Touch marker so we don't keep retrying empty packs.
		return touchFile(filepathJoin(outDir, ".done"))
	}

	if fileParallelism < 1 {
		fileParallelism = 1
	}
	type work struct{ rel, dst string }
	jobs := make(chan work, len(wanted))
	errCh := make(chan error, fileParallelism)

	for i := 0; i < fileParallelism; i++ {
		go func() {
			for w := range jobs {
				if err := downloadInjectionFile(httpClient, server, token, id, w.rel, w.dst); err != nil {
					errCh <- fmt.Errorf("%s: %w", w.rel, err)
					return
				}
			}
			errCh <- nil
		}()
	}
	for _, p := range wanted {
		// Strip the include-determined prefix when laying out files locally
		// so `--include converted` doesn't recreate the converted/ wrapper dir.
		rel := p
		if include == includeConverted {
			rel = strings.TrimPrefix(p, "converted/")
		}
		jobs <- work{rel: p, dst: filepathJoin(outDir, rel)}
	}
	close(jobs)
	for i := 0; i < fileParallelism; i++ {
		if e := <-errCh; e != nil {
			return e
		}
	}

	return touchFile(filepathJoin(outDir, ".done"))
}

// Tiny path helpers kept inline so we don't pull "path/filepath" into more
// files than necessary.
func filepathJoin(parts ...string) string {
	return strings.Join(parts, string(os.PathSeparator))
}
func filepathDir(p string) string {
	if i := strings.LastIndex(p, string(os.PathSeparator)); i >= 0 {
		return p[:i]
	}
	return "."
}
func queryEscape(s string) string { return urlQueryEscape(s) }

// touchFile creates an empty marker file (idempotent).
func touchFile(p string) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

var injectDownloadCmd = &cobra.Command{
	Use:   "download <name>",
	Short: "Download an injection's datapack",
	Long: `Download a datapack either as a single zip file (--output-file) or extracted
into a directory (--output-dir). When extracting, --include selects which
subset of the datapack to fetch.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if injectDownloadStdin && injectDownloadOutput != "" {
			return usageErrorf("--stdin requires --output-dir; --output-file only supports a single positional target")
		}
		return runStdinItems("inject download", "inject download <name>", args, stdinOptions{
			enabled:  injectDownloadStdin,
			field:    injectDownloadStdinField,
			failFast: injectDownloadStdinFailFast,
		}, runInjectDownload)
	},
}

func runInjectDownload(name string) error {
		if injectDownloadOutput == "" && injectDownloadDir == "" {
			return usageErrorf("either --output-file <path> or --output-dir <dir> is required")
		}
		if injectDownloadOutput != "" && injectDownloadDir != "" {
			return usageErrorf("--output-file and --output-dir are mutually exclusive")
		}

		include, err := validateIncludeFlag(injectDownloadInclude)
		if err != nil {
			return err
		}

		id, err := resolveInjectionID(name)
		if err != nil {
			return err
		}

		timeoutSec := injectDownloadTimeout
		if timeoutSec <= 0 {
			timeoutSec = flagRequestTimeout
		}
		httpClient := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}

		if injectDownloadDir != "" {
			outDir := filepathJoin(injectDownloadDir, name)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}
			if err := downloadPackToDir(httpClient, flagServer, flagToken, id, name, outDir, include, injectDownloadFilePar); err != nil {
				return fmt.Errorf("download %s: %w", name, err)
			}
			output.PrintInfo(fmt.Sprintf("Downloaded %s (include=%s) to %s", name, include, outDir))
			return nil
		}

		// Legacy path: server-side zip stream into a single file.
		url := flagServer + fmt.Sprintf("/api/v2/injections/%d/download", id)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		if flagToken != "" {
			req.Header.Set("Authorization", "Bearer "+flagToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("download request failed: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("download failed (HTTP %d): %s", resp.StatusCode, string(body))
		}
		f, err := os.Create(injectDownloadOutput)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer func() { _ = f.Close() }()
		n, err := io.Copy(f, resp.Body)
		if err != nil {
			return fmt.Errorf("write output file: %w", err)
		}
		output.PrintInfo(fmt.Sprintf("Downloaded %d bytes to %s", n, injectDownloadOutput))
		return nil
	}

// ---------- inject download-batch ----------

var (
	injectBatchOutputDir string
	injectBatchInclude   string
	injectBatchPackPar   int
	injectBatchFilePar   int
	injectBatchFromStdin bool
	injectBatchState     string
	injectBatchResume    bool
)

var injectDownloadBatchCmd = &cobra.Command{
	Use:   "download-batch [name|id ...]",
	Short: "Download many datapacks in parallel",
	Long: `Download multiple datapacks into --output-dir. Targets can be supplied as
positional arguments (name or numeric id), piped via --from-stdin (one
name/id per line), or selected from the project by --state.

Examples:
  # Download every detector_success datapack in the default project, only the
  # converted/ subtree, with 3 packs in flight at a time.
  aegisctl inject download-batch --state detector_success --output-dir ./data

  # Pipe a custom name list:
  aegisctl inject list --state build_success -o json --size 100 \
    | jq -r '.items[].name' \
    | aegisctl inject download-batch --from-stdin --output-dir ./data

  # Or pipe ids directly (no resolver round-trip):
  jq -r '.items[].id' picks.json \
    | aegisctl inject download-batch --from-stdin --output-dir ./data`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if injectBatchOutputDir == "" {
			return usageErrorf("--output-dir is required")
		}
		include, err := validateIncludeFlag(injectBatchInclude)
		if err != nil {
			return err
		}
		if injectBatchPackPar < 1 {
			injectBatchPackPar = 1
		}
		if injectBatchFilePar < 1 {
			injectBatchFilePar = 1
		}
		if err := os.MkdirAll(injectBatchOutputDir, 0o755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}

		targets, err := collectBatchTargets(args)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return usageErrorf("no targets supplied (use args, --from-stdin, or --state)")
		}

		// Resolver is shared across workers; only by-name targets need it and the
		// internal cache makes repeated misses cheap.
		resolver, err := newProjectScopedResolver()
		if err != nil {
			return err
		}

		httpClient := &http.Client{Timeout: time.Duration(flagRequestTimeout) * time.Second}
		jobs := make(chan batchTarget, len(targets))
		results := make(chan batchResult, len(targets))

		for i := 0; i < injectBatchPackPar; i++ {
			go func() {
				for t := range jobs {
					results <- runBatchTarget(httpClient, resolver, t, include)
				}
			}()
		}
		for _, t := range targets {
			jobs <- t
		}
		close(jobs)

		var ok, skip, fail int
		for i := 0; i < len(targets); i++ {
			r := <-results
			switch r.status {
			case "ok":
				ok++
				output.PrintInfo(fmt.Sprintf("[ok] %s", r.label))
			case "skip":
				skip++
				output.PrintInfo(fmt.Sprintf("[skip] %s (already complete)", r.label))
			default:
				fail++
				output.PrintInfo(fmt.Sprintf("[fail] %s: %v", r.label, r.err))
			}
		}
		output.PrintInfo(fmt.Sprintf("done: ok=%d skip=%d fail=%d / total=%d", ok, skip, fail, len(targets)))
		if fail > 0 {
			return fmt.Errorf("%d datapack(s) failed", fail)
		}
		return nil
	},
}

type batchTarget struct {
	id   int    // 0 means "resolve by name first"
	name string // human-readable label and on-disk dirname
}

type batchResult struct {
	label  string
	status string // ok | skip | fail
	err    error
}

// collectBatchTargets builds the (id, name) list from positional args, stdin,
// or --state, in that order of precedence (first non-empty source wins).
func collectBatchTargets(posArgs []string) ([]batchTarget, error) {
	if len(posArgs) > 0 {
		return parseBatchTokens(posArgs)
	}
	if injectBatchFromStdin {
		var lines []string
		sc := bufioScanner(os.Stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			lines = append(lines, line)
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return parseBatchTokens(lines)
	}
	if injectBatchState != "" {
		return collectBatchTargetsByState(injectBatchState)
	}
	return nil, nil
}

func parseBatchTokens(tokens []string) ([]batchTarget, error) {
	out := make([]batchTarget, 0, len(tokens))
	for _, raw := range tokens {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if id, err := strconv.Atoi(raw); err == nil && id > 0 {
			out = append(out, batchTarget{id: id, name: raw})
			continue
		}
		out = append(out, batchTarget{name: raw})
	}
	return out, nil
}

// collectBatchTargetsByState pages through the project's injection list,
// filtered by the resolved DatapackState.
func collectBatchTargetsByState(stateRaw string) ([]batchTarget, error) {
	stateParam, err := resolveDatapackStateFlag(stateRaw)
	if err != nil {
		return nil, err
	}
	pid, err := resolveProjectIDByName()
	if err != nil {
		return nil, err
	}
	c := newClient()
	const pageSize = 100
	var out []batchTarget
	for page := 1; page <= 1000; page++ {
		path := fmt.Sprintf("/api/v2/projects/%d/injections?page=%d&size=%d", pid, page, pageSize)
		if stateParam != "" {
			path += "&state=" + stateParam
		}
		type listItem struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		var resp client.APIResponse[client.PaginatedData[listItem]]
		if err := c.Get(path, &resp); err != nil {
			return nil, err
		}
		for _, it := range resp.Data.Items {
			out = append(out, batchTarget{id: it.ID, name: it.Name})
		}
		if len(resp.Data.Items) < pageSize {
			break
		}
	}
	return out, nil
}

func runBatchTarget(httpClient *http.Client, resolver *client.Resolver, t batchTarget, include string) batchResult {
	label := t.name
	id := t.id
	if id == 0 {
		got, err := resolver.InjectionID(t.name)
		if err != nil {
			return batchResult{label: label, status: "fail", err: err}
		}
		id = got
	}
	outDir := filepathJoin(injectBatchOutputDir, t.name)
	if injectBatchResume {
		if _, err := os.Stat(filepathJoin(outDir, ".done")); err == nil {
			return batchResult{label: label, status: "skip"}
		}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return batchResult{label: label, status: "fail", err: err}
	}
	if err := downloadPackToDir(httpClient, flagServer, flagToken, id, t.name, outDir, include, injectBatchFilePar); err != nil {
		return batchResult{label: label, status: "fail", err: err}
	}
	return batchResult{label: label, status: "ok"}
}

// ---------- init ----------

func init() {
	injectListCmd.Flags().StringVar(&injectListState, "state", "", "Filter by datapack state (name or numeric id; valid: "+datapackStateFlagHelp()+")")
	injectListCmd.Flags().StringVar(&injectListFaultType, "fault-type", "", "Filter by fault type")
	injectListCmd.Flags().StringVar(&injectListLabels, "labels", "", "Filter by labels (key=val,...)")
	injectListCmd.Flags().IntVar(&injectListPage, "page", 1, "Page number")
	injectListCmd.Flags().IntVar(&injectListSize, "size", 20, "Page size; must be one of "+pageSizeFlagHelp())

	injectSearchCmd.Flags().StringVar(&injectSearchNamePattern, "name-pattern", "", "Name pattern to search for")
	injectSearchCmd.Flags().StringVar(&injectSearchLabels, "labels", "", "Labels to filter (key=val,...)")

	injectDownloadCmd.Flags().StringVar(&injectDownloadOutput, "output-file", "", "Write the server-side zip stream to this path (legacy mode)")
	injectDownloadCmd.Flags().StringVar(&injectDownloadDir, "output-dir", "", "Extract the datapack into this directory (creates <dir>/<name>/)")
	injectDownloadCmd.Flags().StringVar(&injectDownloadInclude, "include", "converted", "Which subset to download when using --output-dir: "+injectIncludeFlagHelp())
	injectDownloadCmd.Flags().IntVar(&injectDownloadFilePar, "parallel-files", 4, "Concurrent file downloads when using --output-dir")
	injectDownloadCmd.Flags().IntVar(&injectDownloadTimeout, "request-timeout-override", 0, "Per-request HTTP timeout in seconds (0 = use global --request-timeout)")
	addStdinFlags(injectGetCmd, &injectGetStdin, &injectGetStdinField, &injectGetStdinFailFast)
	addStdinFlags(injectFilesCmd, &injectFilesStdin, &injectFilesStdinField, &injectFilesStdinFailFast)
	addStdinFlags(injectDownloadCmd, &injectDownloadStdin, &injectDownloadStdinField, &injectDownloadStdinFailFast)

	injectDownloadBatchCmd.Flags().StringVar(&injectBatchOutputDir, "output-dir", "", "Required: directory under which each pack is extracted as <output-dir>/<name>/")
	injectDownloadBatchCmd.Flags().StringVar(&injectBatchInclude, "include", "converted", "Which subset to download per pack: "+injectIncludeFlagHelp())
	injectDownloadBatchCmd.Flags().IntVar(&injectBatchPackPar, "parallel-packs", 3, "How many packs to download in parallel")
	injectDownloadBatchCmd.Flags().IntVar(&injectBatchFilePar, "parallel-files", 4, "How many files to download in parallel within a single pack")
	injectDownloadBatchCmd.Flags().BoolVar(&injectBatchFromStdin, "from-stdin", false, "Read names or numeric ids from stdin (one per line; '#' starts a comment)")
	injectDownloadBatchCmd.Flags().StringVar(&injectBatchState, "state", "", "Shortcut: select all injections in --project with this datapack state (name or numeric id; valid: "+datapackStateFlagHelp()+")")
	injectDownloadBatchCmd.Flags().BoolVar(&injectBatchResume, "resume", true, "Skip packs that already have a .done marker (default true)")

	injectCmd.AddCommand(injectListCmd)
	injectCmd.AddCommand(injectGetCmd)
	injectCmd.AddCommand(injectSearchCmd)
	injectCmd.AddCommand(injectFilesCmd)
	injectCmd.AddCommand(injectDownloadCmd)
	injectCmd.AddCommand(injectDownloadBatchCmd)
}
