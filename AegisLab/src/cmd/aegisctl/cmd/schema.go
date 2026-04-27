package cmd

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// schemaFlag describes one local flag on a command.
type schemaFlag struct {
	Name      string `json:"name" yaml:"name"`
	Shorthand string `json:"shorthand" yaml:"shorthand"`
	Type      string `json:"type" yaml:"type"`
	Default   string `json:"default" yaml:"default"`
	Usage     string `json:"usage" yaml:"usage"`
	Required  bool   `json:"required" yaml:"required"`
}

// schemaCommand is a single entry in the schema dump.
type schemaCommand struct {
	Path  string       `json:"path" yaml:"path"`
	Short string       `json:"short" yaml:"short"`
	Flags []schemaFlag `json:"flags" yaml:"flags"`
	Args  string       `json:"args" yaml:"args"`
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
	if cmd.Hidden || cmd.Deprecated != "" {
		return
	}

	entry := schemaCommand{
		Path:  cmd.CommandPath(),
		Short: cmd.Short,
		Flags: collectLocalFlags(cmd),
		Args:  argsDescription(cmd),
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
		flags = append(flags, schemaFlag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
			Usage:     f.Usage,
			Required:  required,
		})
	})
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
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
	schemaCmd.AddCommand(schemaDumpCmd)
}
