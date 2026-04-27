package stdin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	defaultScanBuffer = 64 * 1024
	maxScanBuffer     = 1024 * 1024
)

type ParseConfig struct {
	Field          string
	FallbackFields []string
}

func DefaultFields(commandPath string) []string {
	switch strings.TrimSpace(commandPath) {
	case "inject get", "inject files", "inject download":
		return []string{"name"}
	case "trace get", "trace watch":
		return []string{"id"}
	case "task get", "task logs":
		return []string{"id"}
	case "wait":
		return []string{"trace_id", "id"}
	default:
		return nil
	}
}

func Parse(r io.Reader, cfg ParseConfig) ([]string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, defaultScanBuffer), maxScanBuffer)

	var (
		items    []string
		modeJSON bool
		modeSet  bool
		lineNo   int
	)
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		if !modeSet {
			modeJSON = strings.HasPrefix(raw, "{")
			modeSet = true
		}
		if modeJSON {
			item, err := parseJSONLine(raw, cfg, lineNo)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
			continue
		}
		items = append(items, raw)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return items, nil
}

func parseJSONLine(raw string, cfg ParseConfig, lineNo int) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf("parse stdin line %d as json: %w", lineNo, err)
	}

	fields := cfg.FallbackFields
	if field := strings.TrimSpace(cfg.Field); field != "" {
		fields = []string{field}
	}
	for _, field := range fields {
		if value := strings.TrimSpace(stringValue(payload[field])); value != "" {
			return value, nil
		}
	}

	if len(fields) == 0 {
		return "", fmt.Errorf("stdin line %d did not match a supported format", lineNo)
	}
	return "", fmt.Errorf("stdin line %d missing field %q", lineNo, strings.Join(fields, " or "))
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
