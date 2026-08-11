package main

import (
	"bufio"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestChaosTypeMapInvariant fails if the upstream ChaosTypeMap in
// chaos-experiment/handler/handler.go drifts from the in-file capability
// table. This is the whole reason capgen exists: catch upstream changes
// before they ship downstream silently.
func TestChaosTypeMapInvariant(t *testing.T) {
	// Path is relative to this test's package dir.
	path := "../../../chaos-experiment/handler/handler.go"
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("upstream not vendored at %s: %v (CI must run with full monorepo checkout)", path, err)
	}
	defer f.Close()

	inMap := false
	entry := regexp.MustCompile(`^\s*[A-Za-z]+\s*:\s*"([A-Za-z]+)",\s*$`)
	got := map[string]bool{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		if strings.Contains(line, "var ChaosTypeMap = map[ChaosType]string{") {
			inMap = true
			continue
		}
		if inMap {
			if strings.TrimSpace(line) == "}" {
				break
			}
			if m := entry.FindStringSubmatch(line); m != nil {
				got[m[1]] = true
			}
		}
	}

	want := map[string]bool{}
	for _, c := range capabilities() {
		want[c.ChaosType] = true
	}

	var missing, extra []string
	for k := range want {
		if !got[k] {
			extra = append(extra, k)
		}
	}
	for k := range got {
		if !want[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing)+len(extra) > 0 {
		t.Fatalf("ChaosTypeMap drift: missing in capgen=%v, in capgen but not upstream=%v",
			missing, extra)
	}
}
