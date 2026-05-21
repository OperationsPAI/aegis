package handler

import (
	"testing"

	"aegis/internal/chaosengine/registry"
	"aegis/internal/chaosengine/systemconfig"
)

func TestUnregisterSystemRemovesRuntimeData(t *testing.T) {
	const systemName = "dynamic-system-cleanup"
	system := systemconfig.SystemType(systemName)

	t.Cleanup(func() {
		_ = systemconfig.SetCurrentSystem(systemconfig.SystemTrainTicket)
		if IsSystemRegistered(systemName) {
			_ = UnregisterSystem(systemName)
		}
	})

	if err := RegisterSystem(SystemConfig{
		Name:        systemName,
		NsPattern:   "^dynamic-system-cleanup\\d+$",
		DisplayName: "DynamicSystemCleanup",
	}); err != nil {
		t.Fatalf("RegisterSystem() error = %v", err)
	}

	if err := RegisterServiceEndpointProvider(systemName, &testServiceEndpointProvider{
		services: []string{"api"},
		endpoints: map[string][]ServiceEndpointData{
			"api": {{ServiceName: "api", Route: "/health"}},
		},
	}); err != nil {
		t.Fatalf("RegisterServiceEndpointProvider() error = %v", err)
	}

	if err := UnregisterSystem(systemName); err != nil {
		t.Fatalf("UnregisterSystem() error = %v", err)
	}

	if IsSystemRegistered(systemName) {
		t.Fatal("IsSystemRegistered() = true after UnregisterSystem()")
	}
	if registry.IsRegistered(system) {
		t.Fatal("registry.IsRegistered() = true after UnregisterSystem()")
	}
	if systemconfig.GetRegistration(system) != nil {
		t.Fatal("GetRegistration() should return nil after unregister")
	}
}

// TestUpdateSystemPreservesProviders is a regression guard for issue #129.
// UpdateSystem must refresh the registration fields (NsPattern / DisplayName /
// AppLabelKey) without evicting any metadata providers that were attached
// after the initial Register. Unregister+Register would wipe them; Update
// must not.
func TestUpdateSystemPreservesProviders(t *testing.T) {
	const systemName = "dynamic-system-update"

	t.Cleanup(func() {
		_ = systemconfig.SetCurrentSystem(systemconfig.SystemTrainTicket)
		if IsSystemRegistered(systemName) {
			_ = UnregisterSystem(systemName)
		}
	})

	if err := RegisterSystem(SystemConfig{
		Name:        systemName,
		NsPattern:   "^dynamic-system-update\\d+$",
		DisplayName: "Original",
		AppLabelKey: "app",
	}); err != nil {
		t.Fatalf("RegisterSystem() error = %v", err)
	}
	if err := RegisterServiceEndpointProvider(systemName, &testServiceEndpointProvider{
		services: []string{"api"},
		endpoints: map[string][]ServiceEndpointData{
			"api": {{ServiceName: "api", Route: "/health"}},
		},
	}); err != nil {
		t.Fatalf("RegisterServiceEndpointProvider() error = %v", err)
	}

	if err := UpdateSystem(SystemConfig{
		Name:        systemName,
		NsPattern:   "^renamed\\d+$",
		DisplayName: "Renamed",
		AppLabelKey: "app.kubernetes.io/name",
	}); err != nil {
		t.Fatalf("UpdateSystem() error = %v", err)
	}

	reg := systemconfig.GetRegistration(systemconfig.SystemType(systemName))
	if reg == nil {
		t.Fatal("GetRegistration() returned nil after UpdateSystem()")
	}
	if reg.NsPattern != "^renamed\\d+$" || reg.DisplayName != "Renamed" || reg.AppLabelKey != "app.kubernetes.io/name" {
		t.Fatalf("GetRegistration() = %+v, want renamed fields", reg)
	}

	if err := systemconfig.SetCurrentSystem(systemconfig.SystemType(systemName)); err != nil {
		t.Fatalf("SetCurrentSystem() error = %v", err)
	}
	store := systemconfig.GetMetadataStore()
	names, err := store.GetAllServiceNames(systemName)
	if err != nil {
		t.Fatalf("GetAllServiceNames() error = %v", err)
	}
	if len(names) != 1 || names[0] != "api" {
		t.Fatalf("GetAllServiceNames() = %v, want [api] — provider was dropped by UpdateSystem", names)
	}
	endpoints, err := store.GetServiceEndpoints(systemName, "api")
	if err != nil {
		t.Fatalf("GetServiceEndpoints() error = %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].Route != "/health" {
		t.Fatalf("GetServiceEndpoints() = %#v, want one endpoint with /health", endpoints)
	}
}

type testServiceEndpointProvider struct {
	services  []string
	endpoints map[string][]ServiceEndpointData
}

func (p *testServiceEndpointProvider) GetServiceNames() []string {
	return p.services
}

func (p *testServiceEndpointProvider) GetEndpointsByService(serviceName string) []ServiceEndpointData {
	return p.endpoints[serviceName]
}
