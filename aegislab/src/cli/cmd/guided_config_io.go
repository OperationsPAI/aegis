package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	chaos "aegis/platform/chaos"
	"gopkg.in/yaml.v3"
)

// Local YAML I/O for the aegisctl `inject guided` session file.

func defaultGuidedConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(home, ".aegisctl", "inject-guided.yaml"), nil
}

func loadGuidedConfigFile(path string) (*chaos.ConfigFile, error) {
	if path == "" {
		var err error
		path, err = defaultGuidedConfigPath()
		if err != nil {
			return nil, err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &chaos.ConfigFile{
				Version:        1,
				CurrentContext: "default",
				Contexts:       map[string]chaos.CLIContext{"default": {}},
			}, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg chaos.ConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]chaos.CLIContext{"default": {}}
	}
	if cfg.CurrentContext == "" {
		cfg.CurrentContext = "default"
	}
	return &cfg, nil
}

func saveGuidedConfigFile(path string, cfg *chaos.ConfigFile, snapshot chaos.GuidedConfig) error {
	if path == "" {
		var err error
		path, err = defaultGuidedConfigPath()
		if err != nil {
			return err
		}
	}

	if cfg == nil {
		cfg = &chaos.ConfigFile{Version: 1}
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	cfg.GuidedSession = chaos.GuidedSession{
		Config:    snapshot,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config file: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

// mergeGuidedConfig overlays cliCfg onto the saved session config: a non-empty
// CLI flag overrides the saved value, and changing a key that's higher up the
// discovery tree
// (system / app / chaos_type / class+method / route+http_method /
// database+table+operation) clears the dependent fields below it so partial
// overrides don't leave stale selections behind.
func mergeGuidedConfig(fileCfg *chaos.ConfigFile, cliCfg chaos.GuidedConfig) chaos.GuidedConfig {
	merged := chaos.GuidedConfig{}
	if fileCfg != nil {
		merged = fileCfg.GuidedSession.Config
		if ctx, ok := fileCfg.Contexts[fileCfg.CurrentContext]; ok {
			if merged.System == "" {
				merged.System = ctx.DefaultSystem
			}
			if merged.SystemType == "" {
				merged.SystemType = ctx.DefaultSystemType
			}
			if merged.Namespace == "" {
				merged.Namespace = ctx.DefaultNamespace
			}
		}
	}

	overlayGuidedConfig(&merged, cliCfg)
	return merged
}

func overlayGuidedConfig(dst *chaos.GuidedConfig, src chaos.GuidedConfig) {
	if src.System != "" || src.SystemType != "" || src.Namespace != "" {
		duration := dst.Duration
		*dst = chaos.GuidedConfig{}
		dst.Duration = duration
	}
	if src.System != "" {
		dst.System = src.System
	}
	if src.SystemType != "" {
		dst.SystemType = src.SystemType
	}
	if src.Namespace != "" {
		dst.Namespace = src.Namespace
	}
	if src.App != "" {
		if dst.App != src.App {
			dst.App = ""
			clearGuidedFromChaosType(dst)
		}
		dst.App = src.App
	}
	if src.ChaosType != "" {
		if dst.ChaosType != src.ChaosType {
			clearGuidedFromChaosType(dst)
		}
		dst.ChaosType = src.ChaosType
	}

	if src.Container != "" {
		dst.Container = src.Container
	}
	if src.TargetService != "" {
		dst.TargetService = src.TargetService
	}
	if src.Domain != "" {
		dst.Domain = src.Domain
	}

	if src.Class != "" || src.Method != "" {
		if (src.Class != "" && src.Class != dst.Class) || (src.Method != "" && src.Method != dst.Method) {
			dst.Class = ""
			dst.Method = ""
			dst.MutatorConfig = ""
		}
		if src.Class != "" {
			dst.Class = src.Class
		}
		if src.Method != "" {
			dst.Method = src.Method
		}
	}
	if src.MutatorConfig != "" {
		dst.MutatorConfig = src.MutatorConfig
	}

	if src.Route != "" || src.HTTPMethod != "" {
		if (src.Route != "" && src.Route != dst.Route) || (src.HTTPMethod != "" && src.HTTPMethod != dst.HTTPMethod) {
			dst.Route = ""
			dst.HTTPMethod = ""
			dst.ReplaceMethod = ""
		}
		if src.Route != "" {
			dst.Route = src.Route
		}
		if src.HTTPMethod != "" {
			dst.HTTPMethod = src.HTTPMethod
		}
	}

	if src.Database != "" || src.Table != "" || src.Operation != "" {
		if (src.Database != "" && src.Database != dst.Database) ||
			(src.Table != "" && src.Table != dst.Table) ||
			(src.Operation != "" && src.Operation != dst.Operation) {
			dst.Database = ""
			dst.Table = ""
			dst.Operation = ""
		}
		if src.Database != "" {
			dst.Database = src.Database
		}
		if src.Table != "" {
			dst.Table = src.Table
		}
		if src.Operation != "" {
			dst.Operation = src.Operation
		}
	}
	if src.Duration != nil {
		dst.Duration = src.Duration
	}
	if src.MemorySize != nil {
		dst.MemorySize = src.MemorySize
	}
	if src.MemWorker != nil {
		dst.MemWorker = src.MemWorker
	}
	if src.TimeOffset != nil {
		dst.TimeOffset = src.TimeOffset
	}
	if src.CPULoad != nil {
		dst.CPULoad = src.CPULoad
	}
	if src.CPUWorker != nil {
		dst.CPUWorker = src.CPUWorker
	}
	if src.Latency != nil {
		dst.Latency = src.Latency
	}
	if src.Correlation != nil {
		dst.Correlation = src.Correlation
	}
	if src.Jitter != nil {
		dst.Jitter = src.Jitter
	}
	if src.Loss != nil {
		dst.Loss = src.Loss
	}
	if src.Duplicate != nil {
		dst.Duplicate = src.Duplicate
	}
	if src.Corrupt != nil {
		dst.Corrupt = src.Corrupt
	}
	if src.Rate != nil {
		dst.Rate = src.Rate
	}
	if src.Limit != nil {
		dst.Limit = src.Limit
	}
	if src.Buffer != nil {
		dst.Buffer = src.Buffer
	}
	if src.Direction != "" {
		dst.Direction = src.Direction
	}
	if src.DelayDuration != nil {
		dst.DelayDuration = src.DelayDuration
	}
	if src.LatencyDuration != nil {
		dst.LatencyDuration = src.LatencyDuration
	}
	if src.LatencyMs != nil {
		dst.LatencyMs = src.LatencyMs
	}
	if src.CPUCount != nil {
		dst.CPUCount = src.CPUCount
	}
	if src.ReturnType != "" {
		dst.ReturnType = src.ReturnType
	}
	if src.ReturnValueOpt != "" {
		dst.ReturnValueOpt = src.ReturnValueOpt
	}
	if src.ExceptionOpt != "" {
		dst.ExceptionOpt = src.ExceptionOpt
	}
	if src.MemType != "" {
		dst.MemType = src.MemType
	}
	if src.BodyType != "" {
		dst.BodyType = src.BodyType
	}
	if src.ReplaceMethod != "" {
		dst.ReplaceMethod = src.ReplaceMethod
	}
	if src.StatusCode != nil {
		dst.StatusCode = src.StatusCode
	}
	dst.SaveConfig = src.SaveConfig
	dst.ResetConfig = src.ResetConfig
	dst.Apply = src.Apply
}

func clearGuidedFromChaosType(cfg *chaos.GuidedConfig) {
	cfg.ChaosType = ""
	cfg.Container = ""
	cfg.TargetService = ""
	cfg.Domain = ""
	cfg.Class = ""
	cfg.Method = ""
	cfg.MutatorConfig = ""
	cfg.Route = ""
	cfg.HTTPMethod = ""
	cfg.Database = ""
	cfg.Table = ""
	cfg.Operation = ""
	cfg.MemorySize = nil
	cfg.MemWorker = nil
	cfg.TimeOffset = nil
	cfg.CPULoad = nil
	cfg.CPUWorker = nil
	cfg.Latency = nil
	cfg.Correlation = nil
	cfg.Jitter = nil
	cfg.Loss = nil
	cfg.Duplicate = nil
	cfg.Corrupt = nil
	cfg.Rate = nil
	cfg.Limit = nil
	cfg.Buffer = nil
	cfg.Direction = ""
	cfg.DelayDuration = nil
	cfg.LatencyDuration = nil
	cfg.LatencyMs = nil
	cfg.CPUCount = nil
	cfg.ReturnType = ""
	cfg.ReturnValueOpt = ""
	cfg.ExceptionOpt = ""
	cfg.MemType = ""
	cfg.BodyType = ""
	cfg.ReplaceMethod = ""
	cfg.StatusCode = nil
}
