package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"aegis/cli/config"
	"aegis/cli/output"
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

// TestFetchSystemNsPatternsPaginates is the Bug 1 guard: --auto-ns must request
// a page size the backend accepts (<=100, the cap that 400'd at 200) AND walk
// every page so systems past page 1 are not silently dropped. The stub serves
// two pages; the test asserts every requested size is <=100 and that systems
// from both pages land in the result.
func TestFetchSystemNsPatternsPaginates(t *testing.T) {
	page1 := []map[string]string{
		{"name": "hs", "ns_pattern": `^hs\d+$`},
		{"name": "ts", "ns_pattern": `^ts\d+$`},
	}
	page2 := []map[string]string{
		{"name": "sn", "ns_pattern": `^sn\d+$`},
		{"name": "media", "ns_pattern": `^media\d+$`},
	}
	totalPages := 2

	var sawSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/systems" {
			http.NotFound(w, r)
			return
		}
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		sawSizes = append(sawSizes, size)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		items := page1
		if page == 2 {
			items = page2
		}
		var sb strings.Builder
		sb.WriteString(`{"code":200,"message":"ok","data":{"items":[`)
		for i, it := range items {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"name":%q,"ns_pattern":%q}`, it["name"], it["ns_pattern"])
		}
		fmt.Fprintf(&sb, `],"pagination":{"page":%d,"size":%d,"total":4,"total_pages":%d}}}`, page, size, totalPages)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sb.String()))
	}))
	defer srv.Close()

	oldServer, oldToken, oldCfg := flagServer, flagToken, cfg
	t.Cleanup(func() { flagServer, flagToken, cfg = oldServer, oldToken, oldCfg })
	flagServer = srv.URL
	flagToken = "test-token"
	cfg = &config.Config{}

	got, err := fetchSystemNsPatterns()
	if err != nil {
		t.Fatalf("fetchSystemNsPatterns: %v", err)
	}

	for _, s := range sawSizes {
		if s > 100 {
			t.Fatalf("requested page size %d exceeds backend cap of 100 (sizes=%v)", s, sawSizes)
		}
	}
	if len(sawSizes) < 2 {
		t.Fatalf("expected at least 2 page requests, got %d (%v)", len(sawSizes), sawSizes)
	}
	for _, name := range []string{"hs", "ts", "sn", "media"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("system %q from a later page was dropped; got %v", name, got)
		}
	}
}

// TestDecideReclaim is the Bug 2 guard at the pure-logic seam: it pins which
// matched namespaces become reclaim candidates. The empty-discovery regression
// was that namespaces with no helm release (manifest-installed otel-demo, or a
// release helm-list could not surface) never reached a decision at all; here a
// matched namespace with no release still surfaces as reclaim.
func TestDecideReclaim(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	ttl := 6 * time.Hour
	old := now.Add(-11 * 24 * time.Hour)
	fresh := now.Add(-1 * time.Hour)

	cases := []struct {
		name             string
		snap             reclaimSnapshot
		includeUnlabeled bool
		wantDecision     string
	}{
		{
			name:         "labeled idle past ttl reclaims",
			snap:         reclaimSnapshot{Namespace: "ts0", System: "ts", HasManagedByLabel: true, HelmReleaseFound: true, LastDeployed: old},
			wantDecision: "reclaim",
		},
		{
			name:         "idle within ttl skips",
			snap:         reclaimSnapshot{Namespace: "ts1", System: "ts", HasManagedByLabel: true, HelmReleaseFound: true, LastDeployed: fresh},
			wantDecision: "skip",
		},
		{
			name:             "unlabeled without include-unlabeled skips",
			snap:             reclaimSnapshot{Namespace: "ts0", System: "ts", HasManagedByLabel: false, HelmReleaseFound: true, LastDeployed: old},
			includeUnlabeled: false,
			wantDecision:     "skip",
		},
		{
			name:             "unlabeled with include-unlabeled reclaims",
			snap:             reclaimSnapshot{Namespace: "ts0", System: "ts", HasManagedByLabel: false, HelmReleaseFound: true, LastDeployed: old},
			includeUnlabeled: true,
			wantDecision:     "reclaim",
		},
		{
			name:             "no helm release still reclaims when matched",
			snap:             reclaimSnapshot{Namespace: "otel-demo0", System: "otel-demo", HasManagedByLabel: false, HelmReleaseFound: false},
			includeUnlabeled: true,
			wantDecision:     "reclaim",
		},
		{
			name:         "active chaos skips",
			snap:         reclaimSnapshot{Namespace: "hs0", System: "hs", HasManagedByLabel: true, ActiveChaosCount: 1, HelmReleaseFound: true, LastDeployed: old},
			wantDecision: "skip",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideReclaim(tc.snap, ttl, tc.includeUnlabeled, now)
			if got.Decision != tc.wantDecision {
				t.Fatalf("decideReclaim = %q (%s), want %q", got.Decision, got.Reason, tc.wantDecision)
			}
		})
	}
}

// TestRunPedestalReclaimDiscoversNamespaces is the end-to-end Bug 2 regression
// guard against the empty-table symptom: discovery must enumerate k8s
// namespaces and surface idle matched ones even when helm has no release for
// them. Before the fix this path iterated `helm list` and returned zero rows.
func TestRunPedestalReclaimDiscoversNamespaces(t *testing.T) {
	old := time.Now().Add(-11 * 24 * time.Hour).Format(time.RFC3339Nano)

	f := &fakeChartExec{
		results: map[string]fakeExecResult{},
		fallback: func(name string, args []string) ([]byte, error) {
			joined := name + " " + strings.Join(args, " ")
			switch {
			case name == "kubectl" && len(args) >= 2 && args[0] == "get" && args[1] == "ns":
				return []byte("ts0\nhs0\nkube-system\notel-demo0\n"), nil
			case name == "helm" && len(args) >= 1 && args[0] == "status":
				ns := args[1]
				if ns == "otel-demo0" {
					return []byte(`Error: release: not found`), fmt.Errorf("exit 1")
				}
				return []byte(fmt.Sprintf(`{"info":{"last_deployed":%q,"status":"deployed"}}`, old)), nil
			case name == "kubectl" && strings.Contains(joined, "networkchaos"):
				return []byte(""), nil
			}
			return nil, fmt.Errorf("unexpected exec: %s", joined)
		},
	}
	withFakeRunner(t, f)

	oldOutput := flagOutput
	t.Cleanup(func() { flagOutput = oldOutput })
	flagOutput = string(output.FormatJSON)

	out, runErr := captureStdout(t, func() error {
		return runPedestalReclaim(false, "", 0, 0, true, false)
	})
	if runErr != nil {
		t.Fatalf("runPedestalReclaim: %v", runErr)
	}

	var rows []struct {
		Namespace string `json:"namespace"`
		Decision  string `json:"decision"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("output not JSON array: %v\n%s", err, out)
	}

	reclaimed := map[string]bool{}
	for _, d := range rows {
		if d.Decision == "reclaim" {
			reclaimed[d.Namespace] = true
		}
	}
	for _, ns := range []string{"ts0", "hs0", "otel-demo0"} {
		if !reclaimed[ns] {
			t.Fatalf("expected %q to be a reclaim candidate; rows=%+v", ns, rows)
		}
	}
	if reclaimed["kube-system"] {
		t.Fatalf("kube-system must not match any system pattern")
	}
}
