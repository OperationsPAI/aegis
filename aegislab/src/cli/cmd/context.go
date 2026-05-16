package cmd

import (
	"fmt"

	"aegis/cli/config"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage connection contexts",
}

// --- context set ---

var contextSetName string
var contextSetServer string
var contextSetDefaultProject string
var contextSetUsername string
var contextSetPasswordStdin bool
var contextSetCACert string
var contextSetInsecure bool

var contextSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Create or update a context",
	RunE: func(cmd *cobra.Command, args []string) error {
		if contextSetName == "" {
			return fmt.Errorf("--name is required")
		}

		ctx, exists := cfg.Contexts[contextSetName]
		if !exists {
			ctx = config.Context{}
		}

		if contextSetServer != "" {
			ctx.Server = contextSetServer
		}
		if contextSetDefaultProject != "" {
			ctx.DefaultProject = contextSetDefaultProject
		}
		if contextSetUsername != "" {
			ctx.Username = contextSetUsername
		}
		if contextSetPasswordStdin {
			password, err := readPassword(cmd.InOrStdin())
			if err != nil {
				return err
			}
			ctx.Password = password
		}
		if contextSetCACert != "" {
			ctx.CACert = expandPath(contextSetCACert)
		}
		if cmd.Flags().Lookup("insecure-skip-tls-verify").Changed {
			ctx.Insecure = contextSetInsecure
		}

		cfg.Contexts[contextSetName] = ctx

		if err := config.SaveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		action := "Updated"
		if !exists {
			action = "Created"
		}
		output.PrintInfo(fmt.Sprintf("%s context %q", action, contextSetName))
		return nil
	},
}

// --- context use ---

var contextUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch to a named context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := config.SetCurrentContext(cfg, name); err != nil {
			return err
		}
		if err := config.SaveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		output.PrintInfo(fmt.Sprintf("Switched to context %q", name))
		return nil
	},
}

// --- context list ---

var contextListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all contexts",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(cfg.Contexts) == 0 {
			output.PrintInfo("No contexts configured. Run 'aegisctl auth login' or 'aegisctl context set'.")
			return nil
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(cfg.Contexts)
			return nil
		}

		headers := []string{"Current", "Name", "Server", "Project"}
		var rows [][]string
		for name, ctx := range cfg.Contexts {
			current := ""
			if name == cfg.CurrentContext {
				current = "*"
			}
			rows = append(rows, []string{current, name, ctx.Server, ctx.DefaultProject})
		}

		output.PrintTable(headers, rows)
		return nil
	},
}

func init() {
	contextSetCmd.Flags().StringVar(&contextSetName, "name", "", "Context name (required)")
	contextSetCmd.Flags().StringVar(&contextSetServer, "server", "", "Server URL")
	contextSetCmd.Flags().StringVar(&contextSetDefaultProject, "default-project", "", "Default project ID")
	contextSetCmd.Flags().StringVar(&contextSetUsername, "username", "", "Stored username for unattended re-login")
	contextSetCmd.Flags().BoolVar(&contextSetPasswordStdin, "password-stdin", false, "Read stored password from stdin (paired with --username)")
	contextSetCmd.Flags().StringVar(&contextSetCACert, "ca-cert", "", "Absolute path to a PEM file with extra trusted CA(s)")
	contextSetCmd.Flags().BoolVar(&contextSetInsecure, "insecure-skip-tls-verify", false, "Persist insecure-skip-tls-verify=true on this context (DEV ONLY)")

	contextCmd.AddCommand(contextSetCmd)
	contextCmd.AddCommand(contextUseCmd)
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextTrustCmd)
}
