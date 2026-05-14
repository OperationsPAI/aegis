// Package router — well-known endpoints (RFC 8615).
//
// The aegis-environments manifest tells the aegislab UI which deployment
// flavors of this API exist (prod, stage, dev, ...). When the manifest is
// not configured we return 404 (not an empty payload), so the UI's
// `useEnvironmentManifest()` hook can fall back to single-env mode.
//
// Source of truth, in priority order:
//  1. `AEGIS_ENVIRONMENTS_JSON` env var (a literal JSON document).
//  2. `aegis.environments` config block (toml).
//
// CORS for this endpoint is intentionally wide (`Access-Control-Allow-Origin: *`)
// because the UI may be served from a different origin during multi-deploy
// verification. The endpoint is anonymous and ships no secrets.
package router

import (
	"encoding/json"
	"net/http"
	"os"

	"aegis/platform/config"

	"github.com/gin-gonic/gin"
)

const (
	wellKnownEnvPath  = "/.well-known/aegis-environments.json"
	envJSONOverride   = "AEGIS_ENVIRONMENTS_JSON"
	envConfigKey      = "aegis.environments"
	envConfigDefault  = "aegis.environments_default"
)

type EnvironmentDescriptor struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	BaseURL string `json:"baseUrl"`
	Badge   string `json:"badge,omitempty"`
}

type EnvironmentManifest struct {
	Default      string                  `json:"default"`
	Environments []EnvironmentDescriptor `json:"environments"`
}

// registerWellKnownRoutes wires the env-manifest endpoint onto the engine.
// Called once from `router.New`.
func registerWellKnownRoutes(engine *gin.Engine) {
	engine.GET(wellKnownEnvPath, handleEnvironmentManifest)
}

func handleEnvironmentManifest(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=60")
	c.Header("Access-Control-Allow-Origin", "*")

	manifest, ok := loadEnvironmentManifest()
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, manifest)
}

// loadEnvironmentManifest pulls the manifest out of env or config. Returns
// (zero, false) when no manifest is configured — callers should 404.
func loadEnvironmentManifest() (EnvironmentManifest, bool) {
	if raw := os.Getenv(envJSONOverride); raw != "" {
		var m EnvironmentManifest
		if err := json.Unmarshal([]byte(raw), &m); err == nil && validateManifest(m) {
			return m, true
		}
	}

	defaultID := config.GetString(envConfigDefault)
	rawList := config.GetList(envConfigKey)
	if len(rawList) == 0 || defaultID == "" {
		return EnvironmentManifest{}, false
	}

	envs := make([]EnvironmentDescriptor, 0, len(rawList))
	for _, item := range rawList {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		desc := EnvironmentDescriptor{
			ID:      stringField(entry, "id"),
			Label:   stringField(entry, "label"),
			BaseURL: stringField(entry, "base_url"),
			Badge:   stringField(entry, "badge"),
		}
		if desc.BaseURL == "" {
			desc.BaseURL = stringField(entry, "baseUrl")
		}
		envs = append(envs, desc)
	}

	manifest := EnvironmentManifest{Default: defaultID, Environments: envs}
	if !validateManifest(manifest) {
		return EnvironmentManifest{}, false
	}
	return manifest, true
}

func validateManifest(m EnvironmentManifest) bool {
	if m.Default == "" || len(m.Environments) == 0 {
		return false
	}
	hasDefault := false
	for _, env := range m.Environments {
		if env.ID == "" || env.Label == "" || env.BaseURL == "" {
			return false
		}
		if env.Badge != "" && !validBadge(env.Badge) {
			return false
		}
		if env.ID == m.Default {
			hasDefault = true
		}
	}
	return hasDefault
}

func validBadge(b string) bool {
	switch b {
	case "default", "info", "warning", "danger":
		return true
	}
	return false
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
