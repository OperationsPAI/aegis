package systemconfig

import (
	"sort"
	"sync"
	"testing"
)

type mapProvider struct {
	mu      sync.RWMutex
	systems map[SystemType]Registration
}

func newMapProvider(regs ...Registration) *mapProvider {
	p := &mapProvider{systems: make(map[SystemType]Registration, len(regs))}
	for _, reg := range regs {
		p.systems[reg.Name] = reg
	}
	return p
}

func (p *mapProvider) Get(s SystemType) (Registration, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	reg, ok := p.systems[s]
	return reg, ok
}

func (p *mapProvider) All() []Registration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Registration, 0, len(p.systems))
	for _, reg := range p.systems {
		out = append(out, reg)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i].Name) < string(out[j].Name) })
	return out
}

func (p *mapProvider) set(reg Registration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.systems[reg.Name] = reg
}

func builtinTestRegs() []Registration {
	return []Registration{
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

func withBuiltins(t *testing.T) *mapProvider {
	t.Helper()
	p := newMapProvider(builtinTestRegs()...)
	prev := SetProvider(p)
	t.Cleanup(func() { SetProvider(prev) })
	return p
}

func TestSetCurrentSystem(t *testing.T) {
	withBuiltins(t)
	_ = SetCurrentSystem("ts")

	tests := []struct {
		name    string
		system  SystemType
		wantErr bool
	}{
		{"ts", "ts", false},
		{"otel-demo", "otel-demo", false},
		{"media", "media", false},
		{"hs", "hs", false},
		{"sn", "sn", false},
		{"ob", "ob", false},
		{"sockshop", "sockshop", false},
		{"teastore", "teastore", false},
		{"invalid", "invalid-system", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SetCurrentSystem(tt.system)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SetCurrentSystem(%q) error = %v, wantErr %v", tt.system, err, tt.wantErr)
			}
			if !tt.wantErr && GetCurrentSystem() != tt.system {
				t.Fatalf("GetCurrentSystem() = %v, want %v", GetCurrentSystem(), tt.system)
			}
		})
	}
}

func TestGetCurrentSystem(t *testing.T) {
	withBuiltins(t)
	_ = SetCurrentSystem("ts")
	if got := GetCurrentSystem(); got != "ts" {
		t.Errorf("GetCurrentSystem() = %v, want ts", got)
	}
	_ = SetCurrentSystem("otel-demo")
	if got := GetCurrentSystem(); got != "otel-demo" {
		t.Errorf("GetCurrentSystem() = %v, want otel-demo", got)
	}
}

func TestGetAllSystemTypes(t *testing.T) {
	withBuiltins(t)
	types := GetAllSystemTypes()
	if len(types) != 8 {
		t.Fatalf("GetAllSystemTypes() returned %d types, want 8", len(types))
	}
	want := map[SystemType]bool{
		"ts": true, "otel-demo": true, "media": true, "hs": true,
		"sn": true, "ob": true, "sockshop": true, "teastore": true,
	}
	for _, st := range types {
		if !want[st] {
			t.Errorf("unexpected system in GetAllSystemTypes(): %v", st)
		}
	}
}

func TestParseSystemType(t *testing.T) {
	withBuiltins(t)
	tests := []struct {
		in      string
		want    SystemType
		wantErr bool
	}{
		{"ts", "ts", false},
		{"otel-demo", "otel-demo", false},
		{"sockshop", "sockshop", false},
		{"invalid", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseSystemType(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSystemType(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseSystemType(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestGetAppLabelKey_DefaultsToApp(t *testing.T) {
	withBuiltins(t)
	if got := GetAppLabelKey("does-not-exist"); got != "app" {
		t.Errorf("GetAppLabelKey(unregistered) = %q, want app", got)
	}
}

func TestGetAppLabelKey_OtelDemoUsesKubernetesName(t *testing.T) {
	withBuiltins(t)
	if got := GetAppLabelKey("otel-demo"); got != "app.kubernetes.io/name" {
		t.Errorf("GetAppLabelKey(otel-demo) = %q, want app.kubernetes.io/name", got)
	}
}

func TestGetAppLabelKey_FallbackOnEmptyField(t *testing.T) {
	p := newMapProvider(Registration{Name: "ts", NsPattern: `^ts\d+$`, DisplayName: "TrainTicket", AppLabelKey: "app"})
	p.set(Registration{Name: "empty-label", NsPattern: `^empty\d+$`, DisplayName: "Empty"})
	prev := SetProvider(p)
	t.Cleanup(func() { SetProvider(prev) })

	if got := GetAppLabelKey("empty-label"); got != "app" {
		t.Errorf("GetAppLabelKey(empty field) = %q, want app", got)
	}
	if err := SetCurrentSystem("empty-label"); err != nil {
		t.Fatalf("SetCurrentSystem() error = %v", err)
	}
	if got := GetCurrentAppLabelKey(); got != "app" {
		t.Errorf("GetCurrentAppLabelKey() = %q, want app", got)
	}
}

// TestGetAppLabelKey_LiveUpdate proves there is no static map: updating the
// provider mid-test must be observed by the next GetAppLabelKey call.
func TestGetAppLabelKey_LiveUpdate(t *testing.T) {
	p := withBuiltins(t)

	if got := GetAppLabelKey("ts"); got != "app" {
		t.Fatalf("baseline GetAppLabelKey(ts) = %q, want app", got)
	}

	p.set(Registration{
		Name:        "ts",
		NsPattern:   `^ts\d+$`,
		DisplayName: "TrainTicket",
		AppLabelKey: "x-test-label",
	})

	if got := GetAppLabelKey("ts"); got != "x-test-label" {
		t.Fatalf("after update GetAppLabelKey(ts) = %q, want x-test-label", got)
	}
}

func TestGetNamespaceByIndex(t *testing.T) {
	withBuiltins(t)
	got, err := GetNamespaceByIndex("ts", 7)
	if err != nil {
		t.Fatalf("GetNamespaceByIndex() error = %v", err)
	}
	if got != "ts7" {
		t.Errorf("GetNamespaceByIndex(ts,7) = %q, want ts7", got)
	}
	if _, err := GetNamespaceByIndex("not-registered", 0); err == nil {
		t.Error("GetNamespaceByIndex(not-registered) should error")
	}
}

func TestSystemTypeString(t *testing.T) {
	if SystemType("ts").String() != "ts" {
		t.Fail()
	}
}
