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
	reclaimAutoNs           bool
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
		return runPedestalReclaim(apply, reclaimSystem, reclaimMax, reclaimIdleTTLHoursFlag, reclaimIncludeUnlabeled, reclaimAutoNs)
	},
}

// nsPatternSource fetches the {system -> ns_pattern} map used by the reclaim
// predicates. Indirected via a package var so tests can stub the backend call
// without spinning up the apiclient HTTP plumbing.
var nsPatternSource = fetchSystemNsPatterns

func init() {
	pedestalReclaimCmd.Flags().BoolVar(&reclaimApply, "apply", false, "Actually perform helm uninstall + namespace delete (default: dry-run via root --dry-run=true)")
	pedestalReclaimCmd.Flags().StringVar(&reclaimSystem, "system", "", "Filter to a single system (matched against ns_pattern). Empty = all systems.")
	pedestalReclaimCmd.Flags().IntVar(&reclaimMax, "max", 0, "Maximum reclaims per invocation. 0 = no limit (manual-mode default).")
	pedestalReclaimCmd.Flags().IntVar(&reclaimIdleTTLHoursFlag, "idle-ttl-hours", 6, "Idle TTL in hours; releases LastDeployed before now - TTL are eligible.")
	pedestalReclaimCmd.Flags().BoolVar(&reclaimIncludeUnlabeled, "include-unlabeled", false, "Reclaim namespaces missing app.kubernetes.io/managed-by=aegis (legacy ns pre-label-convention)")
	pedestalReclaimCmd.Flags().BoolVar(&reclaimAutoNs, "auto-ns", false, "Fetch ns_patterns from backend (GET /api/v2/systems) instead of using the built-in map. Use when onboarding a new system whose pattern isn't yet shipped in this binary.")

	pedestalCmd.AddCommand(pedestalReclaimCmd)
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
	"media":     `^media\d+$`,
	"teastore":  `^teastore\d+$`,
	"sockshop":  `^sockshop\d+$`,
	"otel-demo": `^otel-demo\d+$`,
}

func runPedestalReclaim(apply bool, systemFilter string, maxDeletes, idleTTLHours int, includeUnlabeled, autoNs bool) error {
	if idleTTLHours < 0 {
		idleTTLHours = 6
	}

	if _, err := chartRunner.LookPath("helm"); err != nil {
		return fmt.Errorf("helm binary not found on PATH: %w", err)
	}
	if _, err := chartRunner.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl binary not found on PATH: %w", err)
	}

	source := systemNsPatterns
	if autoNs {
		fetched, err := nsPatternSource()
		if err != nil {
			return fmt.Errorf("fetch ns patterns from backend (--auto-ns): %w", err)
		}
		if len(fetched) == 0 {
			return fmt.Errorf("backend returned no chaos systems via GET /api/v2/systems")
		}
		source = fetched
	}

	patterns, err := compileSystemPatterns(source, systemFilter)
	if err != nil {
		return err
	}
	if len(patterns) == 0 {
		return fmt.Errorf("no systems matched filter %q (known: %s)", systemFilter, strings.Join(sortedKeys(source), ", "))
	}

	namespaces, err := listClusterNamespaces()
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	now := time.Now()
	idleTTL := time.Duration(idleTTLHours) * time.Hour

	var decisions []cliReclaimDecision
	for _, ns := range namespaces {
		sys, ok := matchPattern(ns, patterns)
		if !ok {
			continue
		}

		snap := reclaimSnapshot{Namespace: ns, System: sys}
		if !includeUnlabeled {
			labeled, lerr := namespaceHasManagedByLabel(ns)
			if lerr != nil {
				snap.LabelErr = lerr
			}
			snap.HasManagedByLabel = labeled
		}
		chaos, cerr := countActiveChaosResources(ns)
		if cerr != nil {
			snap.ChaosErr = cerr
		}
		snap.ActiveChaosCount = chaos
		last, found, herr := helmReleaseLastDeployed(ns)
		snap.HelmReleaseFound = found
		snap.LastDeployed = last
		snap.HelmErr = herr

		decisions = append(decisions, decideReclaim(snap, idleTTL, includeUnlabeled, now))
	}

	sort.SliceStable(decisions, func(i, j int) bool {
		if decisions[i].Decision != decisions[j].Decision {
			return decisions[i].Decision == "reclaim"
		}
		return decisions[i].LastDeployed.Before(decisions[j].LastDeployed)
	})

	if len(decisions) == 0 {
		kubeCtxName := resolveActiveKubeContext()
		patternList := make([]string, 0, len(patterns))
		for name := range patterns {
			patternList = append(patternList, name)
		}
		sort.Strings(patternList)
		output.PrintInfo(fmt.Sprintf(
			"no reclaim candidates: scanned %d namespace(s) in kube-context %q; none matched systems: %s",
			len(namespaces), kubeCtxName, strings.Join(patternList, ", "),
		))
	}

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

// reclaimSnapshot is the per-namespace state the decision function consumes.
// Fetch errors are carried so decideReclaim can surface them as a conservative
// "skip" without the enumeration loop having to branch on every source.
type reclaimSnapshot struct {
	Namespace         string
	System            string
	HasManagedByLabel bool
	LabelErr          error
	ActiveChaosCount  int
	ChaosErr          error
	HelmReleaseFound  bool
	LastDeployed      time.Time
	HelmErr           error
}

// decideReclaim mirrors the backend NamespaceReclaimer predicate order. A
// namespace that matched a system pattern is reclaimable once it is unlabeled-ok,
// chaos-free, and its helm release (if any) is idle past the TTL. A matched
// namespace with no helm release is still reclaimable when idle — the goal is to
// drop the orphaned namespace, not to require a release that may have been
// installed via raw manifests.
func decideReclaim(s reclaimSnapshot, idleTTL time.Duration, includeUnlabeled bool, now time.Time) cliReclaimDecision {
	dec := cliReclaimDecision{
		Namespace:    s.Namespace,
		System:       s.System,
		LastDeployed: s.LastDeployed,
	}
	if !s.LastDeployed.IsZero() {
		dec.AgeHours = now.Sub(s.LastDeployed).Hours()
	}

	if !includeUnlabeled {
		if s.LabelErr != nil {
			dec.Decision = "skip"
			dec.Reason = fmt.Sprintf("label check failed: %v", s.LabelErr)
			return dec
		}
		if !s.HasManagedByLabel {
			dec.Decision = "skip"
			dec.Reason = "missing app.kubernetes.io/managed-by=aegis (use --include-unlabeled)"
			return dec
		}
	}
	if s.ChaosErr != nil {
		dec.Decision = "skip"
		dec.Reason = fmt.Sprintf("chaos-list failed: %v", s.ChaosErr)
		return dec
	}
	if s.ActiveChaosCount > 0 {
		dec.Decision = "skip"
		dec.Reason = fmt.Sprintf("%d active chaos CRs", s.ActiveChaosCount)
		return dec
	}
	if s.HelmErr != nil {
		dec.Decision = "skip"
		dec.Reason = fmt.Sprintf("helm status failed: %v", s.HelmErr)
		return dec
	}

	if !s.HelmReleaseFound || s.LastDeployed.IsZero() {
		dec.Decision = "reclaim"
		dec.Reason = "no helm release; namespace matched system pattern"
		return dec
	}

	age := now.Sub(s.LastDeployed)
	if age < idleTTL {
		dec.Decision = "skip"
		dec.Reason = fmt.Sprintf("idle %s < ttl %s", age.Round(time.Second), idleTTL)
		return dec
	}
	dec.Decision = "reclaim"
	dec.Reason = fmt.Sprintf("idle %s >= ttl %s", age.Round(time.Second), idleTTL)
	return dec
}

func compileSystemPatterns(src map[string]string, filter string) (map[string]*regexp.Regexp, error) {
	out := map[string]*regexp.Regexp{}
	for name, pat := range src {
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

// systemsPageSize is the page size used to walk GET /api/v2/systems. The
// backend caps page size at 100 and rejects anything larger with a 400 (the
// same cap that bit `container version set-image`); we page through every
// result rather than clamp-and-drop so a deployment with >100 registered
// systems still resolves all ns_patterns.
const systemsPageSize = 100

// fetchSystemNsPatterns calls GET /api/v2/systems and projects the response
// down to {name -> ns_pattern}. Systems with an empty ns_pattern are dropped
// (the backend can mark a system inactive without removing the row). It walks
// every page so no system past the first page is silently dropped.
func fetchSystemNsPatterns() (map[string]string, error) {
	cli, ctx := newAPIClient()
	out := map[string]string{}
	for page := int32(1); ; page++ {
		resp, _, err := cli.SystemsAPI.ListChaosSystems(ctx).Page(page).Size(systemsPageSize).Execute()
		if err != nil {
			return nil, fmt.Errorf("list systems (page %d): %w", page, err)
		}
		if resp.Data == nil {
			return nil, fmt.Errorf("backend returned empty data for GET /api/v2/systems")
		}
		items := resp.Data.GetItems()
		for _, s := range items {
			name := strings.TrimSpace(s.GetName())
			pat := strings.TrimSpace(s.GetNsPattern())
			if name == "" || pat == "" {
				continue
			}
			out[name] = pat
		}
		pagination := resp.Data.GetPagination()
		if len(items) == 0 || pagination.GetTotalPages() <= page {
			break
		}
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

// listClusterNamespaces enumerates namespaces the way the backend
// NamespaceReclaimer does — by listing k8s namespaces, not helm releases. The
// previous helm-list-driven enumeration silently returned zero candidates
// whenever benchmark namespaces had no helm release (e.g. otel-demo installed
// via raw manifests) or stored their release where `helm list -A` from the
// caller's kubeconfig could not surface it.
func listClusterNamespaces() ([]string, error) {
	out, err := chartRunner.Run("kubectl", "get", "ns",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	if err != nil {
		return nil, fmt.Errorf("kubectl get ns: %w: %s", err, string(out))
	}
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		if n := strings.TrimSpace(l); n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// helmReleaseLastDeployed returns the release LastDeployed time for the release
// named after the namespace (the convention NamespaceReclaimer assumes). found
// is false when no such release exists; that is not an error — the namespace
// may have been installed via raw manifests.
func helmReleaseLastDeployed(ns string) (time.Time, bool, error) {
	out, err := chartRunner.Run("helm", "status", ns, "-n", ns, "-o", "json")
	if err != nil {
		if strings.Contains(string(out), "release: not found") {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("helm status: %w: %s", err, string(out))
	}
	var status struct {
		Info struct {
			LastDeployed string `json:"last_deployed"`
		} `json:"info"`
	}
	if jerr := json.Unmarshal(out, &status); jerr != nil {
		return time.Time{}, false, fmt.Errorf("decode helm status: %w", jerr)
	}
	if strings.TrimSpace(status.Info.LastDeployed) == "" {
		return time.Time{}, true, nil
	}
	t, perr := parseHelmTime(status.Info.LastDeployed)
	if perr != nil {
		return time.Time{}, true, fmt.Errorf("parse last_deployed %q: %w", status.Info.LastDeployed, perr)
	}
	return t, true, nil
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

// resolveActiveKubeContext returns the kube-context label for the diagnostic
// message. Prefers the aegisctl config KubeContext; falls back to shelling
// kubectl config current-context so the message is always non-empty.
func resolveActiveKubeContext() string {
	if ks, _, err := activeKubeSettings(); err == nil && ks.KubeContext != "" {
		return ks.KubeContext
	}
	out, err := chartRunner.Run("kubectl", "config", "current-context")
	if err == nil {
		if ctx := strings.TrimSpace(string(out)); ctx != "" {
			return ctx
		}
	}
	return "<unknown>"
}
