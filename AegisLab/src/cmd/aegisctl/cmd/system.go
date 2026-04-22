package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegis/cmd/aegisctl/client"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// systemCmd groups subcommands that manage aegis benchmark-system registration
// (the 7 `injection.system.<name>.*` keys that live in etcd + dynamic_configs).
var systemCmd = &cobra.Command{
	Use:     "system",
	Aliases: []string{"sys"},
	Short:   "Register benchmark systems from data.yaml into etcd + dynamic_configs",
	Long: `Every new benchmark integration requires 7 matching entries: one in etcd
(/rcabench/config/global/injection.system.<name>.*) and one in the
dynamic_configs DB table. Missing either half causes the system to show
as "0 enabled" in backend logs and inject submits to fail with
"system does not match any registered namespace pattern".

These subcommands drive the existing backend /api/v2/systems endpoint
(which atomically writes both halves) from a data.yaml seed entry so the
seven keys always land together.`,
}

// --- Shared types ---

// seedDynamicConfig mirrors the `dynamic_configs` list entry in
// AegisLab/data/initial_data/{prod,staging}/data.yaml. Only the fields
// aegisctl needs are decoded; unknown fields are silently dropped.
type seedDynamicConfig struct {
	Key          string `yaml:"key"`
	DefaultValue string `yaml:"default_value"`
	ValueType    int    `yaml:"value_type"`
	Scope        int    `yaml:"scope"`
	Category     string `yaml:"category"`
	Description  string `yaml:"description"`
	IsSecret     bool   `yaml:"is_secret"`
}

// seedDoc is the top-level shape of data.yaml. We ignore everything except
// dynamic_configs.
type seedDoc struct {
	DynamicConfigs []seedDynamicConfig `yaml:"dynamic_configs"`
}

// systemSeed is the aggregated 7-key view of one benchmark system extracted
// from a data.yaml document. Callers are expected to run validateSystemSeed
// before using these values.
type systemSeed struct {
	Name           string
	Count          int
	NsPattern      string
	ExtractPattern string
	DisplayName    string
	AppLabelKey    string
	IsBuiltin      bool
	Status         int
	Description    string
	// Raw is keyed by the final "field" (count/ns_pattern/...) so error
	// messages can reference the exact row that failed validation.
	Raw map[string]seedDynamicConfig
}

// expectedSystemFields enumerates the 7 required keys (suffix after
// "injection.system.<name>."). The value is the expected `value_type` enum
// (0=string, 1=bool, 2=int, 3=float).
var expectedSystemFields = map[string]int{
	"count":           2,
	"ns_pattern":      0,
	"extract_pattern": 0,
	"display_name":    0,
	"app_label_key":   0,
	"is_builtin":      1,
	"status":          2,
}

// systemScopeGlobal is the required scope for injection.system.* rows
// (matches consts.ConfigScopeGlobal on the server).
const systemScopeGlobal = 2

// --- Backend API response shapes ---
//
// Mirrors the chaossystem module's ChaosSystemResp without importing the
// server package to keep aegisctl build-independent of backend internals.

type chaosSystemResp struct {
	ID             int       `json:"id"`
	Name           string    `json:"name"`
	DisplayName    string    `json:"display_name"`
	NsPattern      string    `json:"ns_pattern"`
	ExtractPattern string    `json:"extract_pattern"`
	AppLabelKey    string    `json:"app_label_key"`
	Count          int       `json:"count"`
	Description    string    `json:"description"`
	IsBuiltin      bool      `json:"is_builtin"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type createSystemReq struct {
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	NsPattern      string `json:"ns_pattern"`
	ExtractPattern string `json:"extract_pattern"`
	AppLabelKey    string `json:"app_label_key,omitempty"`
	Count          int    `json:"count"`
	Description    string `json:"description,omitempty"`
}

// --- system register ---

var (
	systemRegisterFromSeed string
	systemRegisterName     string
	systemRegisterEnv      string
	systemRegisterForce    bool
)

var systemRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a benchmark system from a data.yaml seed entry",
	Long: `Parse the dynamic_configs list in a data.yaml file, validate the 7
injection.system.<name>.* entries for the requested system, and POST them
to /api/v2/systems. The backend writes the dynamic_configs rows and the
matching etcd keys under /rcabench/config/global/ in one call.

--env prod|staging is a convenience resolver: when --from-seed points at a
directory (or at data/initial_data/), it picks the file under that subdir.
If --from-seed is a full file path it is used as-is.

If the system is already registered the command fails unless --force is
set, in which case the prior entry is deleted first and then re-created.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		if strings.TrimSpace(systemRegisterFromSeed) == "" {
			return usageErrorf("--from-seed is required")
		}
		if strings.TrimSpace(systemRegisterName) == "" {
			return usageErrorf("--name is required")
		}

		seedPath, err := resolveSeedPath(systemRegisterFromSeed, systemRegisterEnv)
		if err != nil {
			return err
		}

		doc, err := loadSeedDoc(seedPath)
		if err != nil {
			return err
		}
		seed, err := extractSystemSeed(doc, systemRegisterName)
		if err != nil {
			return err
		}
		if err := validateSystemSeed(seed); err != nil {
			return err
		}

		c := newClient()

		// If --force, delete any pre-existing registration first.
		if systemRegisterForce {
			if existing, lookupErr := findSystemByName(c, seed.Name); lookupErr == nil && existing != nil {
				if err := deleteSystemByID(c, existing.ID); err != nil {
					return fmt.Errorf("force re-register: delete existing system %q (id %d) failed: %w", existing.Name, existing.ID, err)
				}
				output.PrintInfo(fmt.Sprintf("Deleted pre-existing system %q (id %d) before re-register", existing.Name, existing.ID))
			}
		} else {
			if existing, lookupErr := findSystemByName(c, seed.Name); lookupErr == nil && existing != nil {
				return conflictErrorf("system %q already registered (id %d); re-run with --force to replace", existing.Name, existing.ID)
			}
		}

		req := createSystemReq{
			Name:           seed.Name,
			DisplayName:    seed.DisplayName,
			NsPattern:      seed.NsPattern,
			ExtractPattern: seed.ExtractPattern,
			AppLabelKey:    seed.AppLabelKey,
			Count:          seed.Count,
			Description:    seed.Description,
		}
		var resp client.APIResponse[chaosSystemResp]
		if err := c.Post("/api/v2/systems", req, &resp); err != nil {
			// Any error here leaves nothing behind because the backend's
			// CreateSystem bails on the first failed row/put; no client-side
			// rollback is needed for partial writes inside one request.
			return fmt.Errorf("register system %q: %w", seed.Name, err)
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("Registered system %q (id %d) with 7 etcd + dynamic_config entries.",
			resp.Data.Name, resp.Data.ID))
		if !seed.IsBuiltin && resp.Data.IsBuiltin {
			output.PrintInfo("Note: backend stored is_builtin=true; seed requested false. Update via API if needed.")
		}
		return nil
	},
}

// --- system list ---

var systemListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List registered benchmark systems",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()

		var resp client.APIResponse[client.PaginatedData[chaosSystemResp]]
		if err := c.Get("/api/v2/systems?page=1&size=100", &resp); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		items := append([]chaosSystemResp(nil), resp.Data.Items...)
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		rows := make([][]string, 0, len(items))
		for _, s := range items {
			rows = append(rows, []string{
				s.Name,
				s.DisplayName,
				s.NsPattern,
				strconv.Itoa(s.Count),
				strconv.FormatBool(s.IsBuiltin),
			})
		}
		output.PrintTable([]string{"Name", "Display", "NsPattern", "Count", "Builtin"}, rows)
		return nil
	},
}

// --- system unregister ---

var (
	systemUnregisterName string
	systemUnregisterYes  bool
)

var systemUnregisterCmd = &cobra.Command{
	Use:   "unregister",
	Short: "Remove the 7 etcd keys + dynamic_config rows for a system",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		if strings.TrimSpace(systemUnregisterName) == "" {
			return usageErrorf("--name is required")
		}
		c := newClient()

		existing, err := findSystemByName(c, systemUnregisterName)
		if err != nil {
			return err
		}
		if existing == nil {
			return notFoundErrorf("system %q is not registered", systemUnregisterName)
		}

		if err := confirmDeletion("system", existing.Name, existing.ID, systemUnregisterYes); err != nil {
			return err
		}
		if err := deleteSystemByID(c, existing.ID); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("Unregistered system %q (id %d)", existing.Name, existing.ID))
		return nil
	},
}

// --- system enable / disable ---
//
// Both are thin wrappers around PUT /api/v2/systems/{id} with a single
// `status` field. We intentionally do NOT add a dedicated enable/disable
// sub-route on the backend — the generic update endpoint already handles the
// mutation, and keeping the API surface narrow reduces drift.

var systemDisableYes bool

// setSystemStatusReq is the minimal PUT body the CLI sends. Using a tight
// struct (instead of a map) makes the JSON shape explicit and lets the cobra
// test pin the wire format.
type setSystemStatusReq struct {
	Status int `json:"status"`
}

// setSystemStatus resolves name -> id and issues PUT /api/v2/systems/{id}
// with the given status. Shared by `enable` and `disable`.
func setSystemStatus(c *client.Client, name string, status int) (*chaosSystemResp, error) {
	existing, err := findSystemByName(c, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		known, lookupErr := listSystemNames(c)
		if lookupErr != nil || len(known) == 0 {
			return nil, notFoundErrorf("system %q is not registered", name)
		}
		return nil, notFoundErrorf("system %q is not registered; known systems: %s",
			name, strings.Join(known, ", "))
	}
	var resp client.APIResponse[chaosSystemResp]
	if err := c.Put(fmt.Sprintf("/api/v2/systems/%d", existing.ID), setSystemStatusReq{Status: status}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// listSystemNames returns a sorted list of registered system names for use
// in error messages. Never fatal: callers fall back to a generic message.
func listSystemNames(c *client.Client) ([]string, error) {
	var resp client.APIResponse[client.PaginatedData[chaosSystemResp]]
	if err := c.Get("/api/v2/systems?page=1&size=100", &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Data.Items))
	for _, s := range resp.Data.Items {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names, nil
}

var systemEnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Enable a registered benchmark system (status=1)",
	Long: `Flip injection.system.<name>.status to 1 via PUT /api/v2/systems/{id}.
Name is resolved to the backend system ID by listing /api/v2/systems.
Builtin systems cannot be enabled/disabled through this endpoint.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		name := strings.TrimSpace(args[0])
		if name == "" {
			return usageErrorf("system name is required")
		}
		c := newClient()
		updated, err := setSystemStatus(c, name, 1)
		if err != nil {
			return err
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(updated)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("Enabled system %q (id %d)", updated.Name, updated.ID))
		return nil
	},
}

var systemDisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable a registered benchmark system (status=0)",
	Long: `Flip injection.system.<name>.status to 0 via PUT /api/v2/systems/{id}.
Disabled systems stay visible in list responses so they can be re-enabled.
Builtin systems cannot be enabled/disabled through this endpoint.
Use --yes to skip the confirmation prompt.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		name := strings.TrimSpace(args[0])
		if name == "" {
			return usageErrorf("system name is required")
		}
		c := newClient()
		existing, err := findSystemByName(c, name)
		if err != nil {
			return err
		}
		if existing == nil {
			known, lookupErr := listSystemNames(c)
			if lookupErr != nil || len(known) == 0 {
				return notFoundErrorf("system %q is not registered", name)
			}
			return notFoundErrorf("system %q is not registered; known systems: %s",
				name, strings.Join(known, ", "))
		}
		// Disable is reversible (unlike unregister) but still user-visible, so
		// gate it behind the same TTY/--yes contract the delete commands use.
		if err := confirmDisable(existing.Name, existing.ID, systemDisableYes); err != nil {
			return err
		}
		var resp client.APIResponse[chaosSystemResp]
		if err := c.Put(fmt.Sprintf("/api/v2/systems/%d", existing.ID), setSystemStatusReq{Status: 0}, &resp); err != nil {
			return err
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("Disabled system %q (id %d)", resp.Data.Name, resp.Data.ID))
		return nil
	},
}

// confirmDisable mirrors confirmDeletion but phrased for a reversible flip.
// Kept local to system.go so project.go's confirmDeletion stays narrowly
// focused on delete flows.
func confirmDisable(name string, id int, yes bool) error {
	if yes {
		return nil
	}
	if flagNonInteractive {
		return usageErrorf("refusing to disable without --yes in non-interactive mode")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return usageErrorf("refusing to disable without --yes when stdin is not a TTY")
	}
	fmt.Fprintf(os.Stderr, "Disable system %s (id %d)? [y/N] ", name, id)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line != "y" && line != "yes" {
		return usageErrorf("aborted by user")
	}
	return nil
}

// --- Helpers ---

// resolveSeedPath lets callers pass either an exact file path, a directory,
// or the initial_data root with --env to choose prod|staging.
func resolveSeedPath(raw, env string) (string, error) {
	raw = strings.TrimSpace(raw)
	info, err := os.Stat(raw)
	if err != nil {
		return "", fmt.Errorf("--from-seed %q: %w", raw, err)
	}
	if !info.IsDir() {
		return raw, nil
	}
	if env == "" {
		return "", usageErrorf("--from-seed points to a directory (%s); --env prod|staging is required to resolve data.yaml", raw)
	}
	switch env {
	case "prod", "staging":
	default:
		return "", usageErrorf("--env must be 'prod' or 'staging' (got %q)", env)
	}
	candidate := strings.TrimRight(raw, string(os.PathSeparator)) + string(os.PathSeparator) + env + string(os.PathSeparator) + "data.yaml"
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Also support raw == <env-dir> directly.
	direct := strings.TrimRight(raw, string(os.PathSeparator)) + string(os.PathSeparator) + "data.yaml"
	if _, err := os.Stat(direct); err == nil {
		return direct, nil
	}
	return "", fmt.Errorf("could not locate data.yaml under %s for env=%s", raw, env)
}

func loadSeedDoc(path string) (*seedDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read seed %q: %w", path, err)
	}
	var doc seedDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse seed %q: %w", path, err)
	}
	return &doc, nil
}

// extractSystemSeed pulls every row whose key begins with
// "injection.system.<name>." out of the document and bundles them into a
// systemSeed. Missing rows surface in validateSystemSeed.
func extractSystemSeed(doc *seedDoc, name string) (*systemSeed, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, usageErrorf("--name is required")
	}
	prefix := "injection.system." + name + "."
	seed := &systemSeed{Name: name, Raw: map[string]seedDynamicConfig{}}
	for _, row := range doc.DynamicConfigs {
		if !strings.HasPrefix(row.Key, prefix) {
			continue
		}
		field := strings.TrimPrefix(row.Key, prefix)
		if _, ok := expectedSystemFields[field]; !ok {
			// Unknown extra field; not currently fatal but we skip it so
			// validation focuses on the seven canonical ones.
			continue
		}
		seed.Raw[field] = row
	}
	if len(seed.Raw) == 0 {
		return nil, notFoundErrorf("no injection.system.%s.* entries found in seed", name)
	}
	return seed, nil
}

// validateSystemSeed checks the 7-key contract (presence + value_type +
// scope) and populates typed fields on the seed.
func validateSystemSeed(seed *systemSeed) error {
	var missing []string
	for field := range expectedSystemFields {
		if _, ok := seed.Raw[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("seed for system %q is missing key(s): injection.system.%s.{%s}",
			seed.Name, seed.Name, strings.Join(missing, ","))
	}

	var problems []string
	for field, expected := range expectedSystemFields {
		row := seed.Raw[field]
		if row.ValueType != expected {
			problems = append(problems, fmt.Sprintf(
				"injection.system.%s.%s: value_type=%d, expected %d",
				seed.Name, field, row.ValueType, expected))
		}
		if row.Scope != systemScopeGlobal {
			problems = append(problems, fmt.Sprintf(
				"injection.system.%s.%s: scope=%d, expected %d (Global)",
				seed.Name, field, row.Scope, systemScopeGlobal))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("seed for system %q has invalid row(s):\n  - %s",
			seed.Name, strings.Join(problems, "\n  - "))
	}

	// Parse typed values. value_type already validated, so conversion errors
	// here mean the default_value itself is malformed.
	count, err := strconv.Atoi(strings.TrimSpace(seed.Raw["count"].DefaultValue))
	if err != nil {
		return fmt.Errorf("injection.system.%s.count default_value %q is not an int: %w",
			seed.Name, seed.Raw["count"].DefaultValue, err)
	}
	status, err := strconv.Atoi(strings.TrimSpace(seed.Raw["status"].DefaultValue))
	if err != nil {
		return fmt.Errorf("injection.system.%s.status default_value %q is not an int: %w",
			seed.Name, seed.Raw["status"].DefaultValue, err)
	}
	isBuiltin, err := strconv.ParseBool(strings.TrimSpace(seed.Raw["is_builtin"].DefaultValue))
	if err != nil {
		return fmt.Errorf("injection.system.%s.is_builtin default_value %q is not a bool: %w",
			seed.Name, seed.Raw["is_builtin"].DefaultValue, err)
	}

	seed.Count = count
	seed.Status = status
	seed.IsBuiltin = isBuiltin
	seed.NsPattern = seed.Raw["ns_pattern"].DefaultValue
	seed.ExtractPattern = seed.Raw["extract_pattern"].DefaultValue
	seed.DisplayName = seed.Raw["display_name"].DefaultValue
	seed.AppLabelKey = seed.Raw["app_label_key"].DefaultValue
	// Prefer the anchor (count) row's description, falling back to display_name's.
	if d := seed.Raw["count"].Description; d != "" {
		seed.Description = d
	} else {
		seed.Description = seed.Raw["display_name"].Description
	}
	return nil
}

// findSystemByName returns nil, nil when no system with that name is
// registered (distinguished from lookup errors).
func findSystemByName(c *client.Client, name string) (*chaosSystemResp, error) {
	var resp client.APIResponse[client.PaginatedData[chaosSystemResp]]
	if err := c.Get("/api/v2/systems?page=1&size=100", &resp); err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			return nil, nil
		}
		return nil, err
	}
	for i := range resp.Data.Items {
		if resp.Data.Items[i].Name == name {
			return &resp.Data.Items[i], nil
		}
	}
	return nil, nil
}

func deleteSystemByID(c *client.Client, id int) error {
	var resp client.APIResponse[any]
	return c.Delete(fmt.Sprintf("/api/v2/systems/%d", id), &resp)
}

// --- init ---

func init() {
	systemRegisterCmd.Flags().StringVar(&systemRegisterFromSeed, "from-seed", "", "Path to data.yaml (file, env dir, or initial_data/ root) (required)")
	systemRegisterCmd.Flags().StringVar(&systemRegisterName, "name", "", "Short code of the system to register, e.g. ts, otel-demo, hs (required)")
	systemRegisterCmd.Flags().StringVar(&systemRegisterEnv, "env", "", "When --from-seed is a directory: 'prod' or 'staging'")
	systemRegisterCmd.Flags().BoolVar(&systemRegisterForce, "force", false, "Replace an existing registration instead of erroring")

	systemUnregisterCmd.Flags().StringVar(&systemUnregisterName, "name", "", "Short code of the system to remove (required)")
	systemUnregisterCmd.Flags().BoolVar(&systemUnregisterYes, "yes", false, "Skip confirmation prompt")
	systemUnregisterCmd.Flags().BoolVar(&systemUnregisterYes, "force", false, "Alias for --yes")

	systemDisableCmd.Flags().BoolVar(&systemDisableYes, "yes", false, "Skip confirmation prompt")
	systemDisableCmd.Flags().BoolVar(&systemDisableYes, "force", false, "Alias for --yes")

	systemCmd.AddCommand(systemRegisterCmd)
	systemCmd.AddCommand(systemListCmd)
	systemCmd.AddCommand(systemUnregisterCmd)
	systemCmd.AddCommand(systemEnableCmd)
	systemCmd.AddCommand(systemDisableCmd)

	systemReseedCmd.Flags().StringVar(&systemReseedName, "name", "", "Short code of a single system to reseed (empty = all systems)")
	systemReseedCmd.Flags().StringVar(&systemReseedEnv, "env", "", "Environment: 'prod' or 'staging' (server resolves relative to initialization.data_path)")
	systemReseedCmd.Flags().StringVar(&systemReseedDataPath, "data-path", "", "Override server-side initialization.data_path (advanced)")
	systemReseedCmd.Flags().BoolVar(&systemReseedApply, "apply", false, "Actually write changes (default is dry-run for safety)")
	systemReseedCmd.Flags().BoolVar(&systemReseedResetOverrides, "reset-overrides", false, "Replace live etcd values that differ from the new default")
	systemCmd.AddCommand(systemReseedCmd)
}

// --- system reseed ---------------------------------------------------------

var (
	systemReseedName           string
	systemReseedEnv            string
	systemReseedDataPath       string
	systemReseedApply          bool
	systemReseedResetOverrides bool
)

// reseedActionResp mirrors initialization.ReseedAction without importing the
// backend package — keeps the CLI binary buildable without cgo / the backend
// build tags.
type reseedActionResp struct {
	Layer    string `json:"layer"`
	System   string `json:"system"`
	Key      string `json:"key"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	Note     string `json:"note"`
	Applied  bool   `json:"applied"`
}

type reseedReportResp struct {
	Env             string             `json:"env"`
	DryRun          bool               `json:"dry_run"`
	ResetOverrides  bool               `json:"reset_overrides"`
	SystemFilter    string             `json:"system_filter"`
	SeedPath        string             `json:"seed_path"`
	Actions         []reseedActionResp `json:"actions"`
	PreservedCount  int                `json:"preserved_overrides"`
	NewVersions     int                `json:"new_versions"`
	DefaultsUpdated int                `json:"defaults_updated"`
	EtcdPublished   int                `json:"etcd_published"`
}

type reseedSystemReqBody struct {
	Name           string `json:"name,omitempty"`
	Env            string `json:"env,omitempty"`
	DataPath       string `json:"data_path,omitempty"`
	Apply          bool   `json:"apply"`
	ResetOverrides bool   `json:"reset_overrides"`
}

var systemReseedCmd = &cobra.Command{
	Use:   "reseed",
	Short: "Propagate data.yaml bumps (chart / version / defaults) to DB + etcd",
	Long: `Diff the server-side data.yaml against the live DB + etcd and apply drift.
Defaults to DRY-RUN for safety — pass --apply to actually write.

What is written:
  - container_versions: a new version INSERTs a new row (history preserved;
    existing versions are never mutated).
  - helm_configs: a new version gets its helm_config. Chart drift on an
    already-seeded version is reported but NOT applied — bump the version
    name in data.yaml to honor the history contract.
  - dynamic_configs: default_value drift is UPDATEd in DB.
  - etcd: if the live etcd value equals the old default (i.e. no operator
    override), follow it forward. A live value that differs from both the
    old and new default is treated as a user override and preserved unless
    --reset-overrides is passed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		c := newClient()
		body := reseedSystemReqBody{
			Name:           strings.TrimSpace(systemReseedName),
			Env:            strings.TrimSpace(systemReseedEnv),
			DataPath:       strings.TrimSpace(systemReseedDataPath),
			Apply:          systemReseedApply,
			ResetOverrides: systemReseedResetOverrides,
		}
		var resp client.APIResponse[reseedReportResp]
		if err := c.Post("/api/v2/systems/reseed", body, &resp); err != nil {
			return fmt.Errorf("reseed: %w (hint: retry with --apply=false to diff without writing; check backend logs for which subsystem failed)", err)
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		printReseedReport(&resp.Data)
		return nil
	},
}

// printReseedReport renders the engine report as a human-readable table
// grouped by layer. Empty action list is an explicit "in sync" message so
// ops can distinguish it from an error.
func printReseedReport(r *reseedReportResp) {
	mode := "APPLY"
	if r.DryRun {
		mode = "DRY-RUN"
	}
	output.PrintInfo(fmt.Sprintf("reseed %s (seed=%s%s)", mode, r.SeedPath, func() string {
		if r.SystemFilter != "" {
			return " filter=" + r.SystemFilter
		}
		return ""
	}()))

	if len(r.Actions) == 0 {
		output.PrintInfo("No drift — DB + etcd already in sync with data.yaml.")
		return
	}

	// Sort: layer, then system, then key.
	actions := append([]reseedActionResp(nil), r.Actions...)
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Layer != actions[j].Layer {
			return actions[i].Layer < actions[j].Layer
		}
		if actions[i].System != actions[j].System {
			return actions[i].System < actions[j].System
		}
		return actions[i].Key < actions[j].Key
	})

	rows := make([][]string, 0, len(actions))
	for _, a := range actions {
		status := "plan"
		if a.Applied {
			status = "applied"
		} else if r.DryRun {
			status = "would-apply"
		} else if a.Note != "" && strings.Contains(a.Note, "preserved") {
			status = "preserved"
		} else if a.Note != "" && strings.Contains(a.Note, "bump container_version") {
			status = "skipped"
		}
		rows = append(rows, []string{
			a.Layer,
			a.System,
			truncCell(a.Key, 48),
			truncCell(a.OldValue, 28),
			truncCell(a.NewValue, 28),
			status,
			truncCell(a.Note, 48),
		})
	}
	output.PrintTable([]string{"Layer", "System", "Key", "Old", "New", "Status", "Note"}, rows)
	output.PrintInfo(fmt.Sprintf("Summary: new_versions=%d defaults_updated=%d etcd_published=%d preserved_overrides=%d",
		r.NewVersions, r.DefaultsUpdated, r.EtcdPublished, r.PreservedCount))
	if r.DryRun && r.PreservedCount == 0 && len(actions) > 0 {
		output.PrintInfo("Re-run with --apply to write the planned changes.")
	}
	if r.PreservedCount > 0 && !r.ResetOverrides {
		output.PrintInfo(fmt.Sprintf("%d etcd override(s) preserved; re-run with --reset-overrides to replace.", r.PreservedCount))
	}
}

// truncCell keeps a cell readable when the value embeds a long chart URL or
// regex without hiding it entirely.
func truncCell(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
