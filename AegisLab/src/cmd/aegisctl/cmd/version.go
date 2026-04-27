package cmd

import (
	"fmt"
	"os"

	"aegis/cmd/aegisctl/config"
	"aegis/cmd/aegisctl/output"

	"github.com/spf13/cobra"
)

var (
	// These values are populated via ldflags in the build step.
	version             = "dev"
	commit              = "unknown"
	buildTime           = "unknown"
	minServerAPIVersion = "2"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show aegisctl version and build metadata",
	RunE: func(*cobra.Command, []string) error {
		printVersionInfo()
		return nil
	},
}

type versionInfo struct {
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	BuildTime    string `json:"build_time"`
	MinServerAPI string `json:"min_server_api"`
}

func printVersionInfo() {
	info := versionInfoPayload()
	if output.OutputFormat(resolveVersionOutputFormat()) == output.FormatJSON {
		output.PrintJSON(info)
		return
	}

	fmt.Printf("version: %s\n", info.Version)
	fmt.Printf("commit: %s\n", info.Commit)
	fmt.Printf("build_time: %s\n", info.BuildTime)
	fmt.Printf("min_server_api: %s\n", info.MinServerAPI)
}

func versionInfoPayload() versionInfo {
	return versionInfo{
		Version:      normalizedString(version, "dev"),
		Commit:       normalizedString(commit, "unknown"),
		BuildTime:    normalizedBuildTime(buildTime),
		MinServerAPI: normalizedString(minServerAPIVersion, "2"),
	}
}

func resolveVersionOutputFormat() string {
	if flagOutput != "" {
		return flagOutput
	}
	if envOutput := os.Getenv("AEGIS_OUTPUT"); envOutput != "" {
		return envOutput
	}
	if cfg != nil && cfg.Preferences.Output != "" {
		return cfg.Preferences.Output
	}

	loadedCfg, err := config.LoadConfig()
	if err == nil && loadedCfg.Preferences.Output != "" {
		return loadedCfg.Preferences.Output
	}
	return string(output.FormatTable)
}

func normalizedString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func normalizedBuildTime(raw string) string {
	if raw == "" {
		return "unknown"
	}
	return raw
}
