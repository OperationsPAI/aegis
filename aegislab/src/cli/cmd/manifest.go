package cmd

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	sigsyaml "sigs.k8s.io/yaml"
)

//go:embed manifest_schema.json
var bundledManifestSchema []byte

var (
	flagFetchSchema  bool
	flagListSystem   string
	flagListService  string
	flagListInstance string
	flagListChartVer string

	flagImportDirConcurrency int
	flagImportDirKeepGoing   bool

	flagImportChartVersion string
)

var manifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Validate, import, and inspect aegis-chaos Point Manifests",
	Long: `Thin client for the aegis-chaos manifest API (design doc §6, ADR-0010).

Used from helm post-install hooks shipped with benchmark charts and from
chart-author workflows. See 'aegisctl manifest <subcommand> --help' for details.

The chaos service is reached through --server / AEGIS_SERVER (the rcabench
gateway federates /v1beta/chaos to aegis-chaos). The legacy --chaos-server
flag and AEGIS_CHAOS_SERVER env var are accepted as deprecated aliases for
chart-hook back-compat; helm hooks should migrate to --server.

Note: 'manifest validate' without --fetch-schema runs entirely offline and
does NOT require a server.`,
}

var manifestValidateCmd = &cobra.Command{
	Use:   "validate <file>",
	Short: "Offline-validate a Point Manifest against the bundled JSON Schema",
	Long: `Validate a Point Manifest YAML against the JSON Schema bundled in
aegisctl. Pass "-" to read the manifest from stdin.

The bundled schema is the structural envelope guard (apiVersion, kind,
metadata required fields, points shape). It does NOT cross-check Capability
target/param schemas — for those, run 'aegisctl manifest import --dry-run',
which goes through the live server-side validator.

Use --fetch-schema to pull the live schema from
GET /v1beta/manifest-schema.json instead of the bundled copy. This catches
Capabilities added after aegisctl was last released (ADR-0010).`,
	Args: cobra.ExactArgs(1),
	RunE: runManifestValidate,
}

var manifestImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import a Point Manifest into aegis-chaos",
	Long: `POST a Point Manifest to /v1beta/systems/{sys}/points/import.

The target system is read from manifest metadata.system; no --system flag is
required. Pass --dry-run to append ?dry_run=true — the server runs full
validation inside a rolled-back transaction and returns the supersede impact
so chart authors can preview before committing.

The replace_scope (service|system|none) is read from spec.replace_scope in
the manifest itself, per ADR-0009.`,
	Args: cobra.ExactArgs(1),
	RunE: runManifestImport,
}

var manifestImportDirCmd = &cobra.Command{
	Use:   "import-dir <root>",
	Short: "Batch-import every PointManifest YAML under a directory",
	Long: `Walk <root> recursively for *.yaml / *.yml files. Each file with
apiVersion=aegis-chaos/v1beta and kind=PointManifest is imported through the
same /v1beta/systems/{sys}/points/import endpoint as 'manifest import'; other
files are skipped (and counted).

--concurrency caps in-flight imports. --keep-going continues on per-file
failure instead of aborting; the final exit status is non-zero whenever any
file failed.`,
	Args: cobra.ExactArgs(1),
	RunE: runManifestImportDir,
}

var manifestListPointsCmd = &cobra.Command{
	Use:   "list-points",
	Short: "List Points currently active for a system",
	Long: `GET /v1beta/systems/{sys}/points with optional service/instance/chart-version
filters. Useful for chart authors to see what manifests are currently active.`,
	Args: requireNoArgs,
	RunE: runManifestListPoints,
}

func runManifestValidate(cmd *cobra.Command, args []string) error {
	raw, err := readManifestSource(args[0])
	if err != nil {
		return err
	}
	docJSON, err := sigsyaml.YAMLToJSON(raw)
	if err != nil {
		return fmt.Errorf("parse manifest as YAML/JSON: %w", err)
	}
	var doc any
	if err := json.Unmarshal(docJSON, &doc); err != nil {
		return fmt.Errorf("decode manifest into generic JSON: %w", err)
	}

	schemaBytes, schemaSrc, err := loadManifestSchema()
	if err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	var schemaDoc any
	if err := json.Unmarshal(schemaBytes, &schemaDoc); err != nil {
		return fmt.Errorf("decode schema from %s: %w", schemaSrc, err)
	}
	if err := compiler.AddResource("file:///manifest-schema.json", schemaDoc); err != nil {
		return fmt.Errorf("register schema: %w", err)
	}
	schema, err := compiler.Compile("file:///manifest-schema.json")
	if err != nil {
		return fmt.Errorf("compile schema from %s: %w", schemaSrc, err)
	}
	if err := schema.Validate(doc); err != nil {
		return renderValidationError(err, schemaSrc)
	}
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "OK: manifest conforms to %s schema\n", schemaSrc)
	}
	return nil
}

func renderValidationError(err error, schemaSrc string) error {
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return fmt.Errorf("validate against %s: %w", schemaSrc, err)
	}
	leaves := flattenValidationLeaves(verr)
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		payload := map[string]any{
			"schema_source": schemaSrc,
			"errors":        leaves,
		}
		output.PrintJSON(payload)
		return silentExit(2)
	}
	fmt.Fprintf(os.Stderr, "manifest failed %s schema validation:\n", schemaSrc)
	for _, l := range leaves {
		fmt.Fprintf(os.Stderr, "  - %s: %s\n", l["instance_path"], l["message"])
	}
	return silentExit(2)
}

func flattenValidationLeaves(ve *jsonschema.ValidationError) []map[string]string {
	var leaves []map[string]string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			leaves = append(leaves, map[string]string{
				"instance_path": "/" + strings.Join(e.InstanceLocation, "/"),
				"message":       e.Error(),
			})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if len(leaves) == 0 {
		leaves = append(leaves, map[string]string{
			"instance_path": "/",
			"message":       ve.Error(),
		})
	}
	return leaves
}

func runManifestImport(cmd *cobra.Command, args []string) error {
	raw, err := readManifestSource(args[0])
	if err != nil {
		return err
	}
	req, system, err := manifestToImportReq(raw)
	if err != nil {
		return err
	}
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	result, err := importManifest(ctx, cli, system, req, flagDryRun)
	if err != nil {
		return err
	}
	return renderImportResult(result, system)
}

func manifestToImportReq(raw []byte) (apiclient.ChaosChaosImportPointsReq, string, error) {
	body, err := sigsyaml.YAMLToJSON(raw)
	if err != nil {
		return apiclient.ChaosChaosImportPointsReq{}, "", fmt.Errorf("parse manifest as YAML/JSON: %w", err)
	}
	var req apiclient.ChaosChaosImportPointsReq
	if err := json.Unmarshal(body, &req); err != nil {
		return apiclient.ChaosChaosImportPointsReq{}, "", fmt.Errorf("decode manifest into typed request: %w", err)
	}
	if req.Metadata == nil || req.Metadata.System == nil || *req.Metadata.System == "" {
		return apiclient.ChaosChaosImportPointsReq{}, "", usageErrorf("manifest metadata.system is required")
	}
	if flagImportChartVersion != "" {
		req.Metadata.SetChartVersion(flagImportChartVersion)
	}
	return req, *req.Metadata.System, nil
}

func importManifest(ctx context.Context, cli *apiclient.APIClient, system string, req apiclient.ChaosChaosImportPointsReq, dryRun bool) (*apiclient.ChaosChaosImportPointsResp, error) {
	call := cli.ChaosAPI.ChaosImportPoints(ctx, system).ChaosChaosImportPointsReq(req)
	if dryRun {
		call = call.DryRun(true)
	}
	resp, _, err := call.Execute()
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Data == nil {
		return nil, fmt.Errorf("aegis-chaos returned an empty envelope")
	}
	return resp.Data, nil
}

func renderImportResult(r *apiclient.ChaosChaosImportPointsResp, system string) error {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(r)
		return nil
	}
	mode := "COMMITTED"
	if r.GetDryRun() {
		mode = "DRY-RUN"
	}
	fmt.Fprintf(os.Stderr, "manifest import (%s) on system=%s\n", mode, system)
	fmt.Fprintf(os.Stderr, "  points upserted:   %d\n", r.GetUpserted())
	fmt.Fprintf(os.Stderr, "  points superseded: %d\n", r.GetSuperseded())
	headers := []string{"POINT_ID"}
	ids := r.GetPointIds()
	rows := make([][]string, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []string{id})
	}
	output.PrintTable(headers, rows)
	return nil
}

type importDirOutcome struct {
	File       string
	Skipped    bool
	SkipReason string
	Err        error
	Upserted   int32
	Superseded int32
}

func runManifestImportDir(_ *cobra.Command, args []string) error {
	root := args[0]
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return usageErrorf("%s is not a directory", root)
	}

	var files []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml":
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}

	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}

	conc := flagImportDirConcurrency
	if conc <= 0 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	outcomes := make([]importDirOutcome, len(files))
	var firstFailure atomic.Value

	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	g, gctx := errgroup.WithContext(groupCtx)

	start := time.Now()
	for i, f := range files {
		i, f := i, f
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()

			outcome := importDirFile(gctx, cli, f)
			outcomes[i] = outcome
			if outcome.Err != nil {
				firstFailure.CompareAndSwap(nil, outcome.Err)
				if !flagImportDirKeepGoing {
					return outcome.Err
				}
			}
			if !flagQuiet {
				printImportDirProgress(outcome)
			}
			return nil
		})
	}
	groupErr := g.Wait()
	elapsed := time.Since(start)

	files0, imported, skipped, failed := 0, 0, 0, 0
	var totUps, totSups int32
	for _, o := range outcomes {
		if o.File == "" {
			continue
		}
		files0++
		switch {
		case o.Skipped:
			skipped++
		case o.Err != nil:
			failed++
		default:
			imported++
			totUps += o.Upserted
			totSups += o.Superseded
		}
	}
	renderImportDirSummary(importDirSummary{
		Files: files0, Imported: imported, Skipped: skipped, Failed: failed,
		Upserted: totUps, Superseded: totSups, Elapsed: elapsed,
		Outcomes: outcomes,
	})

	if groupErr != nil && !flagImportDirKeepGoing {
		return fmt.Errorf("import-dir aborted: %w", groupErr)
	}
	if failed > 0 {
		if v := firstFailure.Load(); v != nil {
			return fmt.Errorf("import-dir: %d file(s) failed; first error: %w", failed, v.(error))
		}
		return fmt.Errorf("import-dir: %d file(s) failed", failed)
	}
	return nil
}

func importDirFile(ctx context.Context, cli *apiclient.APIClient, file string) importDirOutcome {
	o := importDirOutcome{File: file}
	raw, err := os.ReadFile(file)
	if err != nil {
		o.Err = fmt.Errorf("read: %w", err)
		return o
	}
	docJSON, err := sigsyaml.YAMLToJSON(raw)
	if err != nil {
		o.Skipped = true
		o.SkipReason = "not valid YAML"
		return o
	}
	var probe struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
	}
	if err := json.Unmarshal(docJSON, &probe); err != nil {
		o.Skipped = true
		o.SkipReason = "not a Kubernetes-style document"
		return o
	}
	if probe.APIVersion != "aegis-chaos/v1beta" || probe.Kind != "PointManifest" {
		o.Skipped = true
		o.SkipReason = fmt.Sprintf("apiVersion=%q kind=%q (want aegis-chaos/v1beta/PointManifest)", probe.APIVersion, probe.Kind)
		return o
	}
	req, system, err := manifestToImportReq(raw)
	if err != nil {
		o.Err = err
		return o
	}
	resp, err := importManifest(ctx, cli, system, req, flagDryRun)
	if err != nil {
		o.Err = err
		return o
	}
	o.Upserted = resp.GetUpserted()
	o.Superseded = resp.GetSuperseded()
	return o
}

func printImportDirProgress(o importDirOutcome) {
	switch {
	case o.Skipped:
		fmt.Fprintf(os.Stderr, "  skip   %s (%s)\n", o.File, o.SkipReason)
	case o.Err != nil:
		fmt.Fprintf(os.Stderr, "  FAIL   %s: %v\n", o.File, o.Err)
	default:
		fmt.Fprintf(os.Stderr, "  import %s (upserted=%d superseded=%d)\n", o.File, o.Upserted, o.Superseded)
	}
}

type importDirSummary struct {
	Files, Imported, Skipped, Failed int
	Upserted, Superseded             int32
	Elapsed                          time.Duration
	Outcomes                         []importDirOutcome
}

func renderImportDirSummary(s importDirSummary) {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		out := map[string]any{
			"files":      s.Files,
			"imported":   s.Imported,
			"skipped":    s.Skipped,
			"failed":     s.Failed,
			"upserted":   s.Upserted,
			"superseded": s.Superseded,
			"elapsed_ms": s.Elapsed.Milliseconds(),
			"results":    importDirJSONResults(s.Outcomes),
		}
		output.PrintJSON(out)
		return
	}
	headers := []string{"METRIC", "VALUE"}
	rows := [][]string{
		{"files", fmt.Sprintf("%d", s.Files)},
		{"imported", fmt.Sprintf("%d", s.Imported)},
		{"skipped", fmt.Sprintf("%d", s.Skipped)},
		{"failed", fmt.Sprintf("%d", s.Failed)},
		{"upserted_total", fmt.Sprintf("%d", s.Upserted)},
		{"superseded_total", fmt.Sprintf("%d", s.Superseded)},
		{"elapsed", s.Elapsed.Round(time.Millisecond).String()},
	}
	output.PrintTable(headers, rows)
}

func importDirJSONResults(outcomes []importDirOutcome) []map[string]any {
	out := make([]map[string]any, 0, len(outcomes))
	for _, o := range outcomes {
		if o.File == "" {
			continue
		}
		r := map[string]any{"file": o.File}
		switch {
		case o.Skipped:
			r["status"] = "skipped"
			r["reason"] = o.SkipReason
		case o.Err != nil:
			r["status"] = "failed"
			r["error"] = o.Err.Error()
		default:
			r["status"] = "imported"
			r["upserted"] = o.Upserted
			r["superseded"] = o.Superseded
		}
		out = append(out, r)
	}
	return out
}

func runManifestListPoints(cmd *cobra.Command, args []string) error {
	if flagListSystem == "" {
		return usageErrorf("--system is required")
	}
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}
	req := cli.ChaosAPI.ChaosListSystemPoints(ctx, flagListSystem)
	if flagListService != "" {
		req = req.Service(flagListService)
	}
	if flagListInstance != "" || flagListChartVer != "" {
		fmt.Fprintln(os.Stderr,
			"warning: --instance / --chart-version are no longer supported here; "+
				"use 'aegisctl chaos points list' for richer server-side filtering.")
	}
	resp, _, err := req.Execute()
	if err != nil {
		return err
	}
	if resp == nil || resp.Data == nil {
		return fmt.Errorf("aegis-chaos returned an empty envelope")
	}
	return renderManifestPointList(resp.Data)
}

func renderManifestPointList(data *apiclient.ChaosChaosListPointsResp) error {
	pts := data.Points
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(pts)
		return nil
	}
	headers := []string{"POINT_ID", "CAPABILITY", "TARGET", "STATUS", "SOURCE"}
	table := make([][]string, 0, len(pts))
	for _, p := range pts {
		table = append(table, []string{
			strDeref(p.Id), strDeref(p.CapabilityName),
			compactPointTarget(p.Target), strDeref(p.Status), strDeref(p.Source),
		})
	}
	output.PrintTable(headers, table)
	return nil
}

func compactPointTarget(t map[string]interface{}) string {
	if len(t) == 0 {
		return "{}"
	}
	b, err := json.Marshal(t)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func readManifestSource(path string) ([]byte, error) {
	if path == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return raw, nil
}

func loadManifestSchema() ([]byte, string, error) {
	if !flagFetchSchema {
		return bundledManifestSchema, "bundled", nil
	}
	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return nil, "", err
	}
	schema, _, err := cli.ChaosAPI.ChaosManifestSchema(ctx).Execute()
	if err != nil {
		return nil, "", fmt.Errorf("fetch schema: %w", err)
	}
	body, err := json.Marshal(schema)
	if err != nil {
		return nil, "", fmt.Errorf("re-marshal fetched schema: %w", err)
	}
	return body, "live", nil
}

func init() {
	manifestCmd.PersistentFlags().StringVar(&flagChaosServer, "chaos-server", "",
		"DEPRECATED: aegis-chaos URL (env: AEGIS_CHAOS_SERVER). Use --server / "+
			"AEGIS_SERVER instead; the gateway federates /v1beta/chaos. Kept as an "+
			"alias for helm chart hook back-compat.")

	manifestValidateCmd.Flags().BoolVar(&flagFetchSchema, "fetch-schema", false,
		"Fetch the live JSON Schema from GET /v1beta/manifest-schema.json instead of the bundled copy (ADR-0010)")

	manifestListPointsCmd.Flags().StringVar(&flagListSystem, "system", "", "System name (required)")
	manifestListPointsCmd.Flags().StringVar(&flagListService, "service", "", "Filter by service name")
	manifestListPointsCmd.Flags().StringVar(&flagListInstance, "instance", "", "Filter by service instance (client-side)")
	manifestListPointsCmd.Flags().StringVar(&flagListChartVer, "chart-version", "", "Filter by chart version (use 'aegisctl chaos points list' for server-side filtering)")

	manifestImportDirCmd.Flags().IntVar(&flagImportDirConcurrency, "concurrency", 4, "Max parallel imports")
	manifestImportDirCmd.Flags().BoolVar(&flagImportDirKeepGoing, "keep-going", false, "Continue past per-file failures (exit non-zero if any failed)")

	for _, c := range []*cobra.Command{manifestImportCmd, manifestImportDirCmd} {
		c.Flags().StringVar(&flagImportChartVersion, "chart-version", "",
			"Override metadata.chart_version on every imported manifest. Chart-bound "+
				"hooks pass the consumer chart's {{ .Chart.Version }} so points bind to "+
				"the real chart version instead of the file literal (point-manifest-spec §6/§7).")
	}

	manifestCmd.AddCommand(manifestValidateCmd)
	manifestCmd.AddCommand(manifestImportCmd)
	manifestCmd.AddCommand(manifestImportDirCmd)
	manifestCmd.AddCommand(manifestListPointsCmd)

	rootCmd.AddCommand(manifestCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(manifestImportCmd)
		markDryRunSupported(manifestImportDirCmd)
	})
}
