package cmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// schemaFlag describes one local flag on a command.
type schemaFlag struct {
	Name       string   `json:"name" yaml:"name"`
	Shorthand  string   `json:"shorthand" yaml:"shorthand"`
	Type       string   `json:"type" yaml:"type"`
	Default    string   `json:"default" yaml:"default"`
	Usage      string   `json:"usage" yaml:"usage"`
	Required   bool     `json:"required" yaml:"required"`
	EnumValues []string `json:"enum_values" yaml:"enum_values"`
}

// schemaCommand is a single entry in the schema dump.
type schemaCommand struct {
	Path       string       `json:"path" yaml:"path"`
	Short      string       `json:"short" yaml:"short"`
	Flags      []schemaFlag `json:"flags" yaml:"flags"`
	Args       string       `json:"args" yaml:"args"`
	Deprecated bool         `json:"deprecated" yaml:"deprecated"`
}

// schemaDocument is the top-level document emitted by `aegisctl schema dump`.
type schemaDocument struct {
	Version   string            `json:"version" yaml:"version"`
	Commands  []schemaCommand   `json:"commands" yaml:"commands"`
	ExitCodes map[string]string `json:"exit_codes" yaml:"exit_codes"`
}

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Introspect the aegisctl command tree",
}

var schemaDumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Emit the full command tree and exit-code table as a machine-readable document",
	Long: `Dump the full aegisctl command tree including every subcommand, its local
flags, and the documented exit-code table.

Output is JSON by default. Use --output yaml to emit YAML instead. This command
does not require authentication and does not talk to the server.`,
	Args: requireNoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		doc := buildSchemaDocument()

		if flagOutput == "yaml" {
			enc := yaml.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent(2)
			if err := enc.Encode(doc); err != nil {
				return fmt.Errorf("encode yaml: %w", err)
			}
			return enc.Close()
		}

		// Default to JSON for everything else (table, json, empty).
		buf, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(buf))
		return nil
	},
}

func buildSchemaDocument() schemaDocument {
	var cmds []schemaCommand
	collectSchemaCommands(rootCmd, &cmds)
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Path < cmds[j].Path })

	return schemaDocument{
		Version:  "1",
		Commands: cmds,
		ExitCodes: map[string]string{
			"0":  "Success",
			"1":  "Unexpected",
			"2":  "Usage / validation error",
			"3":  "Authentication / authorization failure",
			"4":  "Missing environment / dependency",
			"5":  "Terminal workflow failure",
			"6":  "Timeout",
			"7":  "Not found (HTTP 404 on GET by name/ID)",
			"8":  "Conflict (HTTP 409; resource already exists / state conflict)",
			"9":  "Injection submission deduplicated by server (no trace_id produced)",
			"10": "Server unavailable / server-side runtime error (HTTP 5xx)",
			"11": "Response payload decode failure",
		},
	}
}

func collectSchemaCommands(cmd *cobra.Command, out *[]schemaCommand) {
	if cmd == nil {
		return
	}
	if cmd.Hidden {
		return
	}

	entry := schemaCommand{
		Path:       cmd.CommandPath(),
		Short:      cmd.Short,
		Flags:      collectLocalFlags(cmd),
		Args:       argsDescription(cmd),
		Deprecated: cmd.Deprecated != "",
	}
	*out = append(*out, entry)

	for _, child := range cmd.Commands() {
		collectSchemaCommands(child, out)
	}
}

func collectLocalFlags(cmd *cobra.Command) []schemaFlag {
	var flags []schemaFlag
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		required := false
		if annotations := f.Annotations[cobra.BashCompOneRequiredFlag]; len(annotations) > 0 && annotations[0] == "true" {
			required = true
		}
		enumValues := collectFlagEnumValues(f.Usage)
		if enumValues == nil {
			enumValues = []string{}
		}
		flags = append(flags, schemaFlag{
			Name:       f.Name,
			Shorthand:  f.Shorthand,
			Type:       f.Value.Type(),
			Default:    f.DefValue,
			Usage:      f.Usage,
			Required:   required,
			EnumValues: enumValues,
		})
	})
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

var (
	enumValueKeywordRE = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:valid values?|possible values|must be one of|valid:|valid values?:?)\s*[:;]?\s*([^\n.)]+)`),
		regexp.MustCompile(`(?i)(?:can be one of|allowed values?|values? are)\s*[:;]?\s*([^\n.)]+)`),
	}
	parenthesizedRE = regexp.MustCompile(`\(([^()]*)\)`)
	pipeValueRE     = regexp.MustCompile(`\b([A-Za-z0-9._-]+(?:\s*\|\s*[A-Za-z0-9._-]+)+)\b`)
	quotedValueRE   = regexp.MustCompile(`'([^']+)'(?:\s*(?:/|\|)\s*'[^']+')+`)
)

func collectFlagEnumValues(usage string) []string {
	for _, re := range enumValueKeywordRE {
		matches := re.FindAllStringSubmatch(usage, -1)
		for _, match := range matches {
			values := splitEnumValueCandidates(match[1])
			if isLikelyEnumValueSet(values) {
				return values
			}
		}
	}

	for _, match := range parenthesizedRE.FindAllStringSubmatch(usage, -1) {
		values := splitEnumValueCandidates(match[1])
		if isLikelyEnumValueSet(values) {
			return values
		}
	}

	quotedValues := quotedValueRE.FindStringSubmatch(usage)
	if len(quotedValues) > 0 {
		return splitQuotedEnumValues(quotedValues[0])
	}

	for _, match := range pipeValueRE.FindAllStringSubmatch(usage, -1) {
		values := splitEnumValueCandidates(match[1])
		if isLikelyEnumValueSet(values) {
			return values
		}
	}

	return nil
}

func splitEnumValueCandidates(raw string) []string {
	parts := []string{raw}

	if strings.Contains(raw, "|") {
		parts = strings.Split(raw, "|")
	} else if strings.Contains(raw, ",") {
		parts = strings.Split(raw, ",")
	} else if strings.Contains(raw, " / ") || strings.Contains(raw, "/") {
		parts = strings.Split(raw, "/")
	}

	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		value = strings.Trim(value, " ()")
		value = strings.Trim(value, `"'`)
		if !isLikelyEnumValue(value) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func splitQuotedEnumValues(raw string) []string {
	parts := quotedValueRE.FindAllStringSubmatch(raw, -1)
	if len(parts) == 0 {
		return nil
	}

	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		value := strings.TrimSpace(p[1])
		if !isLikelyEnumValue(value) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func isLikelyEnumValue(value string) bool {
	if value == "" {
		return false
	}
	if strings.ContainsAny(value, " \t\n\r") {
		return false
	}
	if strings.HasSuffix(value, ";") || strings.HasPrefix(value, ";") {
		return false
	}
	switch value {
	case "id", "name", "required", "file", "required)", "required;":
		return false
	}
	return true
}

func isLikelyEnumValueSet(values []string) bool {
	if len(values) < 2 {
		return false
	}
	return len(values) == len(uniqueEnumValues(values))
}

func uniqueEnumValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func argsDescription(cmd *cobra.Command) string {
	if cmd.Use == "" {
		return ""
	}
	// Strip the leading command name; anything after is the args signature.
	for i, r := range cmd.Use {
		if r == ' ' {
			return cmd.Use[i+1:]
		}
	}
	return ""
}

func init() {
	registerOutputFormats(schemaDumpCmd, output.OutputFormat("yaml"))
	schemaCmd.AddCommand(schemaDumpCmd)
}
