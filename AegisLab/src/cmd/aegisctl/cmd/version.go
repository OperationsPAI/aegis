package cmd

import (
	"fmt"
	"time"

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
	if output.OutputFormat(flagOutput) == output.FormatJSON {
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

func normalizedString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func normalizedBuildTime(raw string) string {
	if raw == "" {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return raw
}
