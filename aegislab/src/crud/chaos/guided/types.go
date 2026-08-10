package guided

import (
	platformchaos "aegis/platform/chaos"
)

// The wire DTO lives in aegis/platform/chaos so external SDK consumers and the
// in-process guided walker share one type. Aliases keep guided.* identifiers
// usable from inside the package while the handler avoids a JSON round-trip
// across the package boundary.

type GuidedConfig = platformchaos.GuidedConfig

type GuidedResponse = platformchaos.GuidedResponse

type Preview = platformchaos.Preview

type FieldSpec = platformchaos.FieldSpec

type FieldOption = platformchaos.FieldOption

type ConfigFile = platformchaos.ConfigFile

type CLIContext = platformchaos.CLIContext

type GuidedSession = platformchaos.GuidedSession

func intPtr(v int) *int {
	return &v
}

// NewConfig returns a fresh GuidedConfig pre-populated with the given namespace.
// Intended as a starter constructor for external callers (e.g. aegisctl).
func NewConfig(namespace string) *GuidedConfig {
	return &GuidedConfig{Namespace: namespace}
}
