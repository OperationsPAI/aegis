package cmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"aegis/cli/output"
)

var (
	reclaimApply            bool
	reclaimSystem           string
	reclaimMax              int
	reclaimIdleTTLHoursFlag int
	reclaimIncludeUnlabeled bool
)

var pedestalReclaimCmd = &cobra.Command{
	Use:   "reclaim",
	Short: "Preview or perform helm-uninstall + namespace-delete for idle benchmark namespaces",
	Long: `Identify benchmark namespaces (hs/ts/sn/mm/tea/sockshop/otel-demo/...)
whose helm release has been idle past the configured TTL and either print a
decision table (default) or perform the helm-uninstall + namespace-delete
sequence.

By default this is a DRY RUN — only --apply actually deletes. Predicates
mirror the backend NamespaceReclaimer:

  1. namespace name matches a registered system's ns_pattern
  2. namespace carries the app.kubernetes.io/managed-by=aegis label
     (relax with --include-unlabeled to catch legacy unlabeled namespaces
     like sockshop0..9, ts0..1)
  3. no active chaos-mesh CR (network/pod/http/stress/dns/time/io/jvm chaos)
  4. helm release.Info.LastDeployed is older than --idle-ttl-hours

The Redis ns-lock check is NOT performed from the CLI (no Redis access).
The in-cluster reconciler still respects it. If a campaign is currently
running, prefer running this CLI from outside the active window.`,
	Example: `  aegisctl pedestal reclaim                              # dry-run all systems
  aegisctl pedestal reclaim --system hs                  # dry-run, hs only
  aegisctl pedestal reclaim --apply --max 5              # actually reclaim, up to 5
  aegisctl pedestal reclaim --apply --include-unlabeled  # catch legacy sockshop0..9`,
	RunE: func(cmd *cobra.Command, args []string) error {
		apply := reclaimApply && !flagDryRun
		return runPedestalReclaim(apply, reclaimSystem, reclaimMax, reclaimIdleTTLHoursFlag, reclaimIncludeUnlabeled)
	},
}

func init() {
	pedestalReclaimCmd.Flags().BoolVar(&reclaimApply, "apply", false, "Actually perform helm uninstall + namespace delete (default: dry-run via root --dry-run=true)")
	pedestalReclaimCmd.Flags().StringVar(&reclaimSystem, "system", "", "Filter to a single system (matched against ns_pattern). Empty = all systems.")
	pedestalReclaimCmd.Flags().IntVar(&reclaimMax, "max", 0, "Maximum reclaims per invocation. 0 = no limit (manual-mode default).")
	pedestalReclaimCmd.Flags().IntVar(&reclaimIdleTTLHoursFlag, "idle-ttl-hours", 6, "Idle TTL in hours; releases LastDeployed before now - TTL are eligible.")
	pedestalReclaimCmd.Flags().BoolVar(&reclaimIncludeUnlabeled, "include-unlabeled", false, "Reclaim namespaces missing app.kubernetes.io/managed-by=aegis (legacy ns pre-label-convention)")

	pedestalCmd.AddCommand(pedestalReclaimCmd)
}

// helmReleaseRecord is the CLI projection of `helm list -A -o json` rows.
// Only the fields the reclaim predicates touch.
type helmReleaseRecord struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Updated    string `json:"updated"`
	Status     string `json:"status"`
}

// cliReclaimDecision matches the backend ReclaimDecision shape for output.
type cliReclaimDecision struct {
	Namespace    string
	System       string
	LastDeployed time.Time
	AgeHours     float64
	Decision     string
	Reason       string
}

// Mirrors chaos-system registrations in dynamic config; update when a new system is registered.
var systemNsPatterns = map[string]string{
	"hs":        `^hs\d+$`,
	"ts":        `^ts\d+$`,
	"sn":        `^sn\d+$`,
	"mm":        `^mm\d+$`,
	"tea":       `^tea\d+$`,
	"sockshop":  `^sockshop\d+$`,
	"otel-demo": `^otel-demo\d+$`,
}

func runPedestalReclaim(apply bool, systemFilter string, maxDeletes, idleTTLHours int, includeUnlabeled bool) error {
	if idleTTLHours <= 0 {
		idleTTLHours = 6
	}

	if _, err := chartRunner.LookPath("helm"); err != nil {
		return fmt.Errorf("helm binary not found on PATH: %w", err)
	}
	if _, err := chartRunner.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl binary not found on PATH: %w", err)
	}

	patterns, err := compileSystemPatterns(systemFilter)
	if err != nil {
		return err
	}
	if len(patterns) == 0 {
		return fmt.Errorf("no systems matched filter %q (known: %s)", systemFilter, strings.Join(sortedKeys(systemNsPatterns), ", "))
	}

	releases, err := listHelmReleases()
	if err != nil {
		return fmt.Errorf("list helm releases: %w", err)
	}

	now := time.Now()
	idleTTL := time.Duration(idleTTLHours) * time.Hour
	cutoff := now.Add(-idleTTL)

	var decisions []cliReclaimDecision
	for _, rel := range releases {
		sys, ok := matchPattern(rel.Namespace, patterns)
		if !ok {
			continue
		}
		updated, err := parseHelmTime(rel.Updated)
		if err != nil {
			decisions = append(decisions, cliReclaimDecision{
				Namespace: rel.Namespace,
				System:    sys,
				Decision:  "skip",
				Reason:    fmt.Sprintf("unparseable helm updated time %q: %v", rel.Updated, err),
			})
			continue
		}
		age := now.Sub(updated)
		dec := cliReclaimDecision{
			Namespace:    rel.Namespace,
			System:       sys,
			LastDeployed: updated,
			AgeHours:     age.Hours(),
		}
		if !includeUnlabeled {
			labeled, lerr := namespaceHasManagedByLabel(rel.Namespace)
			if lerr != nil {
				dec.Decision = "skip"
				dec.Reason = fmt.Sprintf("label check failed: %v", lerr)
				decisions = append(decisions, dec)
				continue
			}
			if !labeled {
				dec.Decision = "skip"
				dec.Reason = "missing app.kubernetes.io/managed-by=aegis (use --include-unlabeled)"
				decisions = append(decisions, dec)
				continue
			}
		}
		chaos, cerr := countActiveChaosResources(rel.Namespace)
		if cerr != nil {
			dec.Decision = "skip"
			dec.Reason = fmt.Sprintf("chaos-list failed: %v", cerr)
			decisions = append(decisions, dec)
			continue
		}
		if chaos > 0 {
			dec.Decision = "skip"
			dec.Reason = fmt.Sprintf("%d active chaos CRs", chaos)
			decisions = append(decisions, dec)
			continue
		}
		if updated.After(cutoff) {
			dec.Decision = "skip"
			dec.Reason = fmt.Sprintf("idle %s < ttl %s", age.Round(time.Second), idleTTL)
			decisions = append(decisions, dec)
			continue
		}
		dec.Decision = "reclaim"
		dec.Reason = fmt.Sprintf("idle %s >= ttl %s", age.Round(time.Second), idleTTL)
		decisions = append(decisions, dec)
	}

	sort.SliceStable(decisions, func(i, j int) bool {
		if decisions[i].Decision != decisions[j].Decision {
			return decisions[i].Decision == "reclaim"
		}
		return decisions[i].LastDeployed.Before(decisions[j].LastDeployed)
	})

	printReclaimTable(decisions, apply)

	if !apply {
		return nil
	}

	processed := 0
	for _, d := range decisions {
		if d.Decision != "reclaim" {
			continue
		}
		if maxDeletes > 0 && processed >= maxDeletes {
			output.PrintInfo(fmt.Sprintf("--max %d reached, stopping", maxDeletes))
			break
		}
		if err := performReclaim(d.Namespace); err != nil {
			output.PrintError(fmt.Sprintf("reclaim %s failed: %v", d.Namespace, err))
			continue
		}
		processed++
		output.PrintInfo(fmt.Sprintf("reclaimed %s", d.Namespace))
	}
	output.PrintInfo(fmt.Sprintf("processed %d/%d reclaim candidate(s)", processed, countDecisions(decisions, "reclaim")))
	return nil
}

func compileSystemPatterns(filter string) (map[string]*regexp.Regexp, error) {
	out := map[string]*regexp.Regexp{}
	for name, pat := range systemNsPatterns {
		if filter != "" && filter != name {
			continue
		}
		rx, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("compile pattern for %s: %w", name, err)
		}
		out[name] = rx
	}
	return out, nil
}

func matchPattern(ns string, patterns map[string]*regexp.Regexp) (string, bool) {
	for name, rx := range patterns {
		if rx.MatchString(ns) {
			return name, true
		}
	}
	return "", false
}

func listHelmReleases() ([]helmReleaseRecord, error) {
	out, err := chartRunner.Run("helm", "list", "-A", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("helm list: %w: %s", err, string(out))
	}
	var rows []helmReleaseRecord
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("decode helm list output: %w", err)
	}
	return rows, nil
}

// parseHelmTime tolerates the shapes helm emits across versions. byte-cluster
// `helm list -A -o json` on helm 3.13 emits the offset-duplicated form
// "2026-05-03 10:42:07.422858648 +0800 +0800" where older versions / `helm
// status` would put an MST-style zone abbreviation; both layouts are listed.
func parseHelmTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 -0700",
		"2006-01-02 15:04:05 -0700 -0700",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("no known layout matches %q", s)
}

func namespaceHasManagedByLabel(ns string) (bool, error) {
	out, err := chartRunner.Run("kubectl", "get", "ns", ns,
		"-o", `jsonpath={.metadata.labels.app\.kubernetes\.io/managed-by}`)
	if err != nil {
		return false, fmt.Errorf("kubectl get ns: %w: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)) == "aegis", nil
}

// chaosKinds is the fixed set the predicate refuses on. Matches what the
// backend uses (discovery-driven there, statically enumerated here to avoid
// a second kubectl api-resources call).
var chaosKinds = []string{
	"networkchaos",
	"podchaos",
	"httpchaos",
	"stresschaos",
	"dnschaos",
	"timechaos",
	"iochaos",
	"jvmchaos",
}

func countActiveChaosResources(ns string) (int, error) {
	out, err := chartRunner.Run("kubectl",
		"-n", ns,
		"get", strings.Join(chaosKinds, ","),
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`,
		"--ignore-not-found",
	)
	if err != nil {
		// "the server doesn't have a resource type X" means chaos-mesh
		// is not installed (or partially installed). Treat as zero so a
		// dry-cluster doesn't block reclaim.
		if strings.Contains(string(out), "the server doesn't have a resource type") {
			return 0, nil
		}
		return 0, fmt.Errorf("kubectl get chaos resources: %w: %s", err, string(out))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	count := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			count++
		}
	}
	return count, nil
}

func performReclaim(ns string) error {
	if out, err := chartRunner.Run("helm", "uninstall", ns, "-n", ns, "--wait"); err != nil {
		// helm uninstall on a missing release errors with a specific
		// substring; tolerate it so a partial state (chart already
		// gone, ns still present) still proceeds to ns delete.
		if !strings.Contains(string(out), "release: not found") {
			return fmt.Errorf("helm uninstall: %w: %s", err, string(out))
		}
	}
	if out, err := chartRunner.Run("kubectl", "delete", "ns", ns, "--wait=true"); err != nil {
		if strings.Contains(string(out), "not found") {
			return nil
		}
		return fmt.Errorf("kubectl delete ns: %w: %s", err, string(out))
	}
	return nil
}

func printReclaimTable(decisions []cliReclaimDecision, willApply bool) {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		type row struct {
			Namespace    string  `json:"namespace"`
			System       string  `json:"system"`
			LastDeployed string  `json:"last_deployed"`
			AgeHours     float64 `json:"age_hours"`
			Decision     string  `json:"decision"`
			Reason       string  `json:"reason"`
		}
		out := make([]row, 0, len(decisions))
		for _, d := range decisions {
			ld := ""
			if !d.LastDeployed.IsZero() {
				ld = d.LastDeployed.Format(time.RFC3339)
			}
			out = append(out, row{
				Namespace: d.Namespace, System: d.System,
				LastDeployed: ld, AgeHours: d.AgeHours,
				Decision: d.Decision, Reason: d.Reason,
			})
		}
		output.PrintJSON(out)
		return
	}

	rows := make([][]string, 0, len(decisions))
	for _, d := range decisions {
		ld := ""
		age := ""
		if !d.LastDeployed.IsZero() {
			ld = d.LastDeployed.Format(time.RFC3339)
			age = fmt.Sprintf("%.1f", d.AgeHours)
		}
		rows = append(rows, []string{d.Namespace, d.System, ld, age, d.Decision, d.Reason})
	}
	output.PrintTable(
		[]string{"Namespace", "System", "LastDeployed", "AgeHours", "Decision", "Reason"},
		rows,
	)
	if !willApply {
		output.PrintInfo("dry-run only. Re-run with --apply to actually uninstall + delete.")
	}
}

func countDecisions(d []cliReclaimDecision, action string) int {
	n := 0
	for _, x := range d {
		if x.Decision == action {
			n++
		}
	}
	return n
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
