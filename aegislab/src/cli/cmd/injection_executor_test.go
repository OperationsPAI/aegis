package cmd

import "testing"

func TestInjectionExecutorKey(t *testing.T) {
	cases := []struct {
		name    string
		system  string
		wantNs  string
		wantKey string
		wantErr bool
	}{
		{"plain", "ts", "aegis", "injection.system.ts.executor_authoritative", false},
		{"hyphen", "social-network", "aegis", "injection.system.social-network.executor_authoritative", false},
		{"trims-whitespace", "  hr  ", "aegis", "injection.system.hr.executor_authoritative", false},
		{"empty", "", "", "", true},
		{"whitespace-only", "   ", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns, key, err := injectionExecutorKey(tc.system)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got ns=%q key=%q", ns, key)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ns != tc.wantNs || key != tc.wantKey {
				t.Fatalf("ns=%q key=%q want ns=%q key=%q", ns, key, tc.wantNs, tc.wantKey)
			}
		})
	}
}
