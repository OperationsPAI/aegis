package cmd

import (
	"github.com/spf13/cobra"
)

// dryRunSupportedPaths records the full command paths (e.g. "aegisctl execute
// submit") that honor the global --dry-run flag. Commands not in this set must
// reject --dry-run at PersistentPreRunE time to avoid "silent no-op" bugs where
// agents think a run was dry but it wasn't.
var dryRunSupportedPaths = map[string]bool{}

// markDryRunSupported registers a command as honoring --dry-run. Call this in
// the command's init() after the command has been attached to its parent so
// CommandPath() resolves correctly.
func markDryRunSupported(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	dryRunSupportedPaths[cmd.CommandPath()] = true
}

// isDryRunSupported returns true if the given command path has opted in to
// --dry-run handling.
func isDryRunSupported(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	return dryRunSupportedPaths[cmd.CommandPath()]
}

// markPedestalDryRunSupported walks the pedestal subcommand tree (which lives
// in pedestal.go, a file this package is not permitted to edit directly) and
// marks any command whose RunE honors --dry-run. Currently only
// `aegisctl pedestal helm verify` does.
func markPedestalDryRunSupported() {
	for _, top := range rootCmd.Commands() {
		if top.Name() != "pedestal" {
			continue
		}
		// Walk depth-first; mark leaf commands known to honor --dry-run.
		var walk func(c *cobra.Command)
		walk = func(c *cobra.Command) {
			// `pedestal helm verify` is the only known consumer today.
			if c.Name() == "verify" && c.Parent() != nil && c.Parent().Name() == "helm" {
				markDryRunSupported(c)
			}
			for _, child := range c.Commands() {
				walk(child)
			}
		}
		walk(top)
	}
}
