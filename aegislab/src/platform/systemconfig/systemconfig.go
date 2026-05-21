// Package systemconfig exposes the live chaos-system configuration as a small
// set of accessors. There is no static registration table — every read goes
// through Provider, which is wired to the etcd/Viper-backed
// chaosSystemConfigManager in production.
package systemconfig

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"aegis/platform/chaos"
	"aegis/platform/config"
)

// SystemType is the short code that identifies a target microservice system
// (e.g. "ts", "otel-demo"). Live registrations live in etcd; this stays a
// string alias to keep the chaos-service HTTP boundary trivial.
type SystemType string

// String returns the string representation of the SystemType.
func (s SystemType) String() string { return string(s) }

// Registration is the read-only view of a chaos-system that callers need from
// systemconfig. It is decoded from the live config manager every time it is
// requested; there is no in-package cache.
type Registration struct {
	Name        SystemType
	NsPattern   string
	DisplayName string
	AppLabelKey string
}

// Provider abstracts the source of system registrations so tests can swap in
// an in-memory map without booting Viper / etcd.
type Provider interface {
	Get(SystemType) (Registration, bool)
	All() []Registration
}

var (
	providerMu sync.RWMutex
	provider   Provider = configManagerProvider{}

	currentSystem   SystemType = "ts"
	currentSystemMu sync.RWMutex
)

// SetProvider swaps the active Provider. Returns the previously installed
// provider so callers (typically test fixtures) can restore it.
func SetProvider(p Provider) Provider {
	providerMu.Lock()
	defer providerMu.Unlock()
	prev := provider
	provider = p
	return prev
}

func getProvider() Provider {
	providerMu.RLock()
	defer providerMu.RUnlock()
	return provider
}

// configManagerProvider is the production Provider. It is stateless; the
// chaosSystemConfigManager façade reads Viper on each call.
type configManagerProvider struct{}

func (configManagerProvider) Get(system SystemType) (Registration, bool) {
	cfg, ok := config.GetChaosSystemConfigManager().Get(chaos.SystemType(system))
	if !ok {
		return Registration{}, false
	}
	return Registration{
		Name:        system,
		NsPattern:   cfg.NsPattern,
		DisplayName: cfg.DisplayName,
		AppLabelKey: cfg.AppLabelKey,
	}, true
}

func (configManagerProvider) All() []Registration {
	all := config.GetChaosSystemConfigManager().GetAll()
	out := make([]Registration, 0, len(all))
	for name, cfg := range all {
		out = append(out, Registration{
			Name:        SystemType(name),
			NsPattern:   cfg.NsPattern,
			DisplayName: cfg.DisplayName,
			AppLabelKey: cfg.AppLabelKey,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return string(out[i].Name) < string(out[j].Name)
	})
	return out
}

// GetRegistration returns the live registration for a system, or nil if the
// system is not configured.
func GetRegistration(system SystemType) *Registration {
	reg, ok := getProvider().Get(system)
	if !ok {
		return nil
	}
	return &reg
}

// IsRegistered reports whether the system is currently configured.
func IsRegistered(system SystemType) bool {
	_, ok := getProvider().Get(system)
	return ok
}

// GetAppLabelKey returns the pod selector label key for the given system.
// Defaults to "app" if the system is unregistered or the field is empty.
func GetAppLabelKey(system SystemType) string {
	reg, ok := getProvider().Get(system)
	if !ok || reg.AppLabelKey == "" {
		return "app"
	}
	return reg.AppLabelKey
}

// GetCurrentAppLabelKey returns the pod selector label key for the current
// system.
func GetCurrentAppLabelKey() string {
	return GetAppLabelKey(GetCurrentSystem())
}

// SetCurrentSystem sets the process-wide system type. It validates the value
// against the live Provider.
func SetCurrentSystem(system SystemType) error {
	if !IsRegistered(system) {
		return fmt.Errorf("invalid system type: %s, valid types are: %s", system, strings.Join(registeredSystemNames(), ", "))
	}
	currentSystemMu.Lock()
	defer currentSystemMu.Unlock()
	currentSystem = system
	return nil
}

// GetCurrentSystem returns the current system type.
func GetCurrentSystem() SystemType {
	currentSystemMu.RLock()
	defer currentSystemMu.RUnlock()
	return currentSystem
}

// GetAllSystemTypes returns every system currently configured, sorted by name.
func GetAllSystemTypes() []SystemType {
	regs := getProvider().All()
	out := make([]SystemType, len(regs))
	for i, reg := range regs {
		out[i] = reg.Name
	}
	return out
}

// GetNamespaceByIndex expands the system's ns_pattern with the given index.
func GetNamespaceByIndex(system SystemType, index int) (string, error) {
	reg, ok := getProvider().Get(system)
	if !ok {
		return "", fmt.Errorf("system type not found: %s", system)
	}
	name := strings.TrimPrefix(reg.NsPattern, "^")
	name = strings.TrimSuffix(name, "$")
	name = strings.Replace(name, `\d+`, fmt.Sprintf("%d", index), 1)
	return name, nil
}

// ParseSystemType parses a string into a SystemType, validating against the
// live Provider.
func ParseSystemType(s string) (SystemType, error) {
	system := SystemType(s)
	if !IsRegistered(system) {
		return "", fmt.Errorf("invalid system type: %s, valid types are: %s", s, strings.Join(registeredSystemNames(), ", "))
	}
	return system, nil
}

func registeredSystemNames() []string {
	systems := GetAllSystemTypes()
	names := make([]string, len(systems))
	for i, system := range systems {
		names[i] = system.String()
	}
	return names
}
