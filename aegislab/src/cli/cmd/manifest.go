package cmd

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"
	sigsyaml "sigs.k8s.io/yaml"
)

//go:embed manifest_schema.json
var bundledManifestSchema []byte

const chaosServerEnv = "AEGIS_CHAOS_SERVER"

var (
	flagChaosServer  string
	flagFetchSchema  bool
	flagListSystem   string
	flagListService  string
	flagListInstance string
	flagListChartVer string
)

var manifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Validate, import, and inspect aegis-chaos Point Manifests",
	Long: `Thin client for the aegis-chaos manifest API (design doc §6, ADR-0010).

Used from helm post-install hooks shipped with benchmark charts and from
chart-author workflows. See 'aegisctl manifest <subcommand> --help' for details.

The chaos service URL must be supplied via --chaos-server or env
AEGIS_CHAOS_SERVER. helm chart hooks should template it as
http://{{ include "chaos.fullname" . }}.{{ .Release.Namespace }}.svc:{{ .Values.httpPort }}
so the value tracks the actual Release.Name/Namespace (the chaos Service
is named {Release}-chaos per charts/chaos/templates/_helpers.tpl).

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

var manifestListPointsCmd = &cobra.Command{
	Use:   "list-points",
	Short: "List Points currently active for a system",
	Long: `GET /v1beta/systems/{sys}/points with optional service/instance/chart-version
filters. Useful for chart authors to see what manifests are currently active.

Note: in step 1 of the aegis-chaos migration the server-side endpoint is a
501 stub. This subcommand is wired through so chart-author workflows can
land alongside the server work; expect a 501 until the catalog endpoint
arrives in step 3.`,
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
	body, err := sigsyaml.YAMLToJSON(raw)
	if err != nil {
		return fmt.Errorf("parse manifest as YAML/JSON: %w", err)
	}
	var probe struct {
		Metadata struct {
			System string `json:"system"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("decode manifest metadata: %w", err)
	}
	if probe.Metadata.System == "" {
		return usageErrorf("manifest metadata.system is required")
	}
	path := "/v1beta/systems/" + url.PathEscape(probe.Metadata.System) + "/points/import"
	if flagDryRun {
		path += "?dry_run=true"
	}
	respBody, status, err := chaosDoJSON(http.MethodPost, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("aegis-chaos returned %d: %s", status, string(respBody))
	}
	var env struct {
		Data importResultView `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("decode response: %w (body: %s)", err, string(respBody))
	}
	return renderImportResult(&env.Data, probe.Metadata.System)
}

type importResultView struct {
	Upserted   int      `json:"upserted"`
	Superseded int      `json:"superseded"`
	DryRun     bool     `json:"dry_run"`
	PointIDs   []string `json:"point_ids"`
}

func renderImportResult(r *importResultView, system string) error {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(r)
		return nil
	}
	mode := "COMMITTED"
	if r.DryRun {
		mode = "DRY-RUN"
	}
	fmt.Fprintf(os.Stderr, "manifest import (%s) on system=%s\n", mode, system)
	fmt.Fprintf(os.Stderr, "  points upserted:   %d\n", r.Upserted)
	fmt.Fprintf(os.Stderr, "  points superseded: %d\n", r.Superseded)
	headers := []string{"POINT_ID"}
	rows := make([][]string, 0, len(r.PointIDs))
	for _, id := range r.PointIDs {
		rows = append(rows, []string{id})
	}
	output.PrintTable(headers, rows)
	return nil
}

func runManifestListPoints(cmd *cobra.Command, args []string) error {
	if flagListSystem == "" {
		return usageErrorf("--system is required")
	}
	path := "/v1beta/systems/" + url.PathEscape(flagListSystem) + "/points"
	q := url.Values{}
	if flagListService != "" {
		q.Set("service", flagListService)
	}
	if flagListInstance != "" {
		q.Set("instance", flagListInstance)
	}
	if flagListChartVer != "" {
		q.Set("chart_version", flagListChartVer)
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	respBody, status, err := chaosDoJSON(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotImplemented {
		return fmt.Errorf("aegis-chaos %s returned 501 — catalog endpoint lands in step 3; see ADR-0010", path)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("aegis-chaos returned %d: %s", status, string(respBody))
	}
	var env struct {
		Data []pointView `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("decode response: %w (body: %s)", err, string(respBody))
	}
	return renderPointList(env.Data)
}

type pointView struct {
	ID         string         `json:"id"`
	Capability string         `json:"capability_name"`
	Target     map[string]any `json:"target"`
	Status     string         `json:"status"`
	Source     string         `json:"source"`
}

func renderPointList(rows []pointView) error {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(rows)
		return nil
	}
	headers := []string{"POINT_ID", "CAPABILITY", "TARGET", "STATUS", "SOURCE"}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{r.ID, r.Capability, compactTarget(r.Target), r.Status, r.Source})
	}
	output.PrintTable(headers, table)
	return nil
}

func compactTarget(t map[string]any) string {
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
	body, status, err := chaosDoJSON(http.MethodGet, "/v1beta/manifest-schema.json", nil)
	if err != nil {
		return nil, "", fmt.Errorf("fetch schema: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, "", fmt.Errorf("fetch schema: aegis-chaos returned %d: %s", status, string(body))
	}
	return body, "live", nil
}

func resolveChaosServer() (string, error) {
	if flagChaosServer != "" {
		return flagChaosServer, nil
	}
	if v := os.Getenv(chaosServerEnv); v != "" {
		return v, nil
	}
	// Fall through to the federated gateway path. aegis-chaos is
	// reachable through rcabench-gateway under /v1beta/chaos/*, so
	// interactive aegisctl users only need --server (or AEGIS_SERVER).
	// Helm chart hooks should still pass --chaos-server explicitly
	// because they call the in-cluster ClusterIP directly.
	if flagServer != "" {
		return strings.TrimRight(flagServer, "/") + "/v1beta/chaos", nil
	}
	return "", usageErrorf("aegis-chaos URL required: pass --chaos-server, set %s, "+
		"or pass --server (defaults to <server>/v1beta/chaos via the gateway)",
		chaosServerEnv)
}

// chaosHTTPTimeout caps a single chaos-service request. Sized for the
// slowest realistic call (server-side batched UPSERT of a 600-point
// manifest after the import_lock SELECT…FOR UPDATE settles).
const chaosHTTPTimeout = 90 * time.Second

func chaosDoJSON(method, path string, body []byte) ([]byte, int, error) {
	server, err := resolveChaosServer()
	if err != nil {
		return nil, 0, err
	}
	httpClient := &http.Client{
		Transport: client.TransportFor(resolveTLSOptions()),
		Timeout:   chaosHTTPTimeout,
	}
	url := strings.TrimRight(server, "/") + path

	var lastErr error
	var lastStatus int
	var lastBody []byte
	for attempt := 0; attempt < 2; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
		if err != nil {
			return nil, 0, fmt.Errorf("build request: %w", err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		// aegis-chaos auths via TrustedHeaderAuth, same Bearer scheme as the
		// backend. manifest-schema.json is the only unauthenticated endpoint;
		// sending a bearer there is harmless.
		if flagToken != "" {
			req.Header.Set("Authorization", "Bearer "+flagToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt == 0 && isRetryableNetErr(err) {
				time.Sleep(1 * time.Second)
				continue
			}
			return nil, 0, fmt.Errorf("%s %s: %w", method, path, err)
		}
		b, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, resp.StatusCode, fmt.Errorf("read response: %w", readErr)
		}
		lastStatus = resp.StatusCode
		lastBody = b
		if attempt == 0 && resp.StatusCode >= 500 {
			time.Sleep(1 * time.Second)
			continue
		}
		return b, resp.StatusCode, nil
	}
	if lastErr != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", method, path, lastErr)
	}
	return lastBody, lastStatus, nil
}

func isRetryableNetErr(err error) bool {
	var nerr net.Error
	if errors.As(err, &nerr) {
		return nerr.Timeout()
	}
	return false
}

func init() {
	manifestCmd.PersistentFlags().StringVar(&flagChaosServer, "chaos-server", "",
		"aegis-chaos service URL (env: AEGIS_CHAOS_SERVER). When unset, "+
			"defaults to <--server>/v1beta/chaos so interactive aegisctl users can "+
			"reach chaos-service through the federating gateway with just --server. "+
			"Helm chart hooks should still set this explicitly to the in-cluster "+
			"ClusterIP: http://{{ include \"chaos.fullname\" . }}.{{ .Release.Namespace }}.svc:{{ .Values.httpPort }}")

	manifestValidateCmd.Flags().BoolVar(&flagFetchSchema, "fetch-schema", false,
		"Fetch the live JSON Schema from GET /v1beta/manifest-schema.json instead of the bundled copy (ADR-0010)")

	manifestListPointsCmd.Flags().StringVar(&flagListSystem, "system", "", "System name (required)")
	manifestListPointsCmd.Flags().StringVar(&flagListService, "service", "", "Filter by service name")
	manifestListPointsCmd.Flags().StringVar(&flagListInstance, "instance", "", "Filter by service instance")
	manifestListPointsCmd.Flags().StringVar(&flagListChartVer, "chart-version", "", "Filter by chart version")

	manifestCmd.AddCommand(manifestValidateCmd)
	manifestCmd.AddCommand(manifestImportCmd)
	manifestCmd.AddCommand(manifestListPointsCmd)

	rootCmd.AddCommand(manifestCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(manifestImportCmd)
	})
}
