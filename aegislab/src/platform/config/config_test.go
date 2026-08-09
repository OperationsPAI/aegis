package config

import (
	"bytes"
	"testing"

	"github.com/spf13/viper"
)

// TestGetChaosServiceURL pins the §11 step 4 prereq plumbing: a TOML
// `[chaos] service_url = ...` block must reach GetChaosServiceURL as a
// trimmed string, with the empty/absent case returning "" (caller's
// signal to use the legacy CRD-watcher path).
func TestGetChaosServiceURL(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{"absent", ``, ""},
		{"empty", "[chaos]\nservice_url = \"\"\n", ""},
		{"set", "[chaos]\nservice_url = \"http://localhost:9999\"\n", "http://localhost:9999"},
		{"trims_whitespace", "[chaos]\nservice_url = \"  http://x:1  \"\n", "http://x:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			viper.SetConfigType("toml")
			if err := viper.ReadConfig(bytes.NewBufferString(tc.toml)); err != nil {
				t.Fatalf("ReadConfig: %v", err)
			}
			if got := GetChaosServiceURL(); got != tc.want {
				t.Fatalf("want %q got %q", tc.want, got)
			}
		})
	}
}
