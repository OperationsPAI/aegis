package cmd

import (
	"testing"
	"time"
)

// TestPedestalReclaimAutoNsUsesFetchedPatterns asserts that when --auto-ns is
// set the reclaim code paths consult nsPatternSource (the backend) instead of
// the in-binary systemNsPatterns map. The test seeds the stub with a system
// not present in the hardcoded map ("onlineboutique") and verifies that
// compileSystemPatterns picks it up when given the fetched source. This is
// the seam #428 exists to plug.
func TestPedestalReclaimAutoNsUsesFetchedPatterns(t *testing.T) {
	if _, ok := systemNsPatterns["onlineboutique"]; ok {
		t.Fatalf("test invariant: onlineboutique should not be in the hardcoded map")
	}

	prev := nsPatternSource
	t.Cleanup(func() { nsPatternSource = prev })

	called := 0
	nsPatternSource = func() (map[string]string, error) {
		called++
		return map[string]string{
			"hs":             `^hs\d+$`,
			"onlineboutique": `^onlineboutique\d+$`,
		}, nil
	}

	fetched, err := nsPatternSource()
	if err != nil {
		t.Fatalf("nsPatternSource stub returned err: %v", err)
	}
	if called != 1 {
		t.Fatalf("expected stub to be invoked once, got %d", called)
	}

	patterns, err := compileSystemPatterns(fetched, "")
	if err != nil {
		t.Fatalf("compileSystemPatterns: %v", err)
	}
	if _, ok := patterns["onlineboutique"]; !ok {
		t.Fatalf("auto-ns fetched source should yield onlineboutique pattern; got %v", patterns)
	}

	sys, matched := matchPattern("onlineboutique0", patterns)
	if !matched || sys != "onlineboutique" {
		t.Fatalf("onlineboutique0 should match fetched pattern; got sys=%q matched=%v", sys, matched)
	}

	filtered, err := compileSystemPatterns(fetched, "onlineboutique")
	if err != nil {
		t.Fatalf("compileSystemPatterns(filter): %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("--system onlineboutique with auto-ns should yield exactly one pattern; got %d", len(filtered))
	}
}


func TestParseHelmTime(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
		// wantUnix pins the exact instant the layout decoded to. Cross-zone
		// comparisons are done in UTC so a parser bug that picks the wrong
		// zone surfaces here, not in a flaky local-time assertion.
		wantUnix int64
	}{
		{
			name:     "byte-cluster helm 3.13 offset-duplicated",
			in:       "2026-05-03 10:42:07.422858648 +0800 +0800",
			wantUnix: time.Date(2026, 5, 3, 2, 42, 7, 422858648, time.UTC).Unix(),
		},
		{
			name:     "rfc3339",
			in:       "2026-05-03T10:42:07Z",
			wantUnix: time.Date(2026, 5, 3, 10, 42, 7, 0, time.UTC).Unix(),
		},
		{
			name:     "rfc3339nano with offset",
			in:       "2026-05-03T10:42:07.422858648+08:00",
			wantUnix: time.Date(2026, 5, 3, 2, 42, 7, 422858648, time.UTC).Unix(),
		},
		{
			name:     "legacy mst-abbrev helm status",
			in:       "2026-05-03 10:42:07.422858648 +0800 CST",
			wantUnix: time.Date(2026, 5, 3, 2, 42, 7, 422858648, time.UTC).Unix(),
		},
		{
			name:    "garbage",
			in:      "not a time",
			wantErr: true,
		},
		{
			name:    "empty",
			in:      "",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHelmTime(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseHelmTime(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHelmTime(%q) error = %v, want nil", tc.in, err)
			}
			if got.UTC().Unix() != tc.wantUnix {
				t.Fatalf("parseHelmTime(%q) = %v (unix %d), want unix %d",
					tc.in, got.UTC(), got.UTC().Unix(), tc.wantUnix)
			}
		})
	}
}
