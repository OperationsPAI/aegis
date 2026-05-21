// Package systemconfigtest provides test-only fixtures that swap the
// systemconfig Provider for an in-memory map so unit tests don't have to
// boot Viper / etcd.
package systemconfigtest

import (
	"sort"
	"sync"
	"testing"

	"aegis/platform/systemconfig"
)

// InMemoryProvider implements systemconfig.Provider backed by a map.
type InMemoryProvider struct {
	mu      sync.RWMutex
	systems map[systemconfig.SystemType]systemconfig.Registration
}

// NewInMemoryProvider seeds an InMemoryProvider with the given registrations.
func NewInMemoryProvider(regs ...systemconfig.Registration) *InMemoryProvider {
	p := &InMemoryProvider{systems: make(map[systemconfig.SystemType]systemconfig.Registration, len(regs))}
	for _, reg := range regs {
		p.systems[reg.Name] = reg
	}
	return p
}

// Set inserts or replaces a registration. Safe for use after the provider
// has been installed.
func (p *InMemoryProvider) Set(reg systemconfig.Registration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.systems[reg.Name] = reg
}

// Get implements systemconfig.Provider.
func (p *InMemoryProvider) Get(system systemconfig.SystemType) (systemconfig.Registration, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	reg, ok := p.systems[system]
	return reg, ok
}

// All implements systemconfig.Provider.
func (p *InMemoryProvider) All() []systemconfig.Registration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]systemconfig.Registration, 0, len(p.systems))
	for _, reg := range p.systems {
		out = append(out, reg)
	}
	sort.Slice(out, func(i, j int) bool {
		return string(out[i].Name) < string(out[j].Name)
	})
	return out
}

// WithSystems installs an InMemoryProvider seeded with regs for the duration
// of the test. The previous provider is restored via t.Cleanup.
func WithSystems(t *testing.T, regs ...systemconfig.Registration) *InMemoryProvider {
	t.Helper()
	p := NewInMemoryProvider(regs...)
	prev := systemconfig.SetProvider(p)
	t.Cleanup(func() {
		systemconfig.SetProvider(prev)
	})
	return p
}

// BuiltinFixtures returns the 8 historical builtin systems as Registration
// values. Useful for tests that previously relied on the hardcoded map.
func BuiltinFixtures() []systemconfig.Registration {
	return []systemconfig.Registration{
		{Name: "ts", NsPattern: `^ts\d+$`, DisplayName: "TrainTicket", AppLabelKey: "app"},
		{Name: "otel-demo", NsPattern: `^otel-demo\d+$`, DisplayName: "OtelDemo", AppLabelKey: "app.kubernetes.io/name"},
		{Name: "media", NsPattern: `^media\d+$`, DisplayName: "MediaMicroservices", AppLabelKey: "app"},
		{Name: "hs", NsPattern: `^hs\d+$`, DisplayName: "HotelReservation", AppLabelKey: "app"},
		{Name: "sn", NsPattern: `^sn\d+$`, DisplayName: "SocialNetwork", AppLabelKey: "app"},
		{Name: "ob", NsPattern: `^ob\d+$`, DisplayName: "OnlineBoutique", AppLabelKey: "app"},
		{Name: "sockshop", NsPattern: `^sockshop\d+$`, DisplayName: "SockShop", AppLabelKey: "app"},
		{Name: "teastore", NsPattern: `^teastore\d+$`, DisplayName: "TeaStore", AppLabelKey: "app"},
	}
}
