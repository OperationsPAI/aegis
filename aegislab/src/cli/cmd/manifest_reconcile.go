package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var flagReconcileSystem string

var manifestReconcileDirCmd = &cobra.Command{
	Use:   "reconcile-dir <root>",
	Short: "Import every PointManifest under a directory, then deprecate Points it no longer covers",
	Long: `Walk <root> for *.yaml / *.yml PointManifests and import each one (like
'manifest import-dir'), collecting the Point ids each system's manifests
produce. Then call POST /v1beta/systems/{sys}/points/sweep per system so any
active Point absent from the imported set is deprecated.

Import-dir alone only supersedes Points that share a service identity with a
re-imported manifest; Points whose natural key drifted out of the manifest set
(e.g. a renamed service prefix) survive as zombies. reconcile-dir retires them.

--system restricts both import and sweep to manifests for that one system.
--dry-run imports in a rolled-back transaction and reports how many Points
would be deprecated per system without sweeping.`,
	Args: cobra.ExactArgs(1),
	RunE: runManifestReconcileDir,
}

func runManifestReconcileDir(_ *cobra.Command, args []string) error {
	root := args[0]
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return usageErrorf("%s is not a directory", root)
	}

	var files []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml":
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}

	cli, ctx, err := newChaosAPIClient()
	if err != nil {
		return err
	}

	activeBySystem := map[string]map[string]struct{}{}
	// A system with any failed import has a non-authoritative active set;
	// sweeping it would deprecate Points the failed manifest still covers.
	failedSystems := map[string]struct{}{}
	imported, skipped, failed := 0, 0, 0
	var firstErr error
	recordFail := func(file, system string, ferr error) {
		failed++
		if system != "" {
			failedSystems[system] = struct{}{}
		}
		if firstErr == nil {
			firstErr = ferr
		}
		if !flagQuiet {
			fmt.Fprintf(os.Stderr, "  FAIL   %s: %v\n", file, ferr)
		}
	}
	for _, f := range files {
		c := classifyManifestFile(f)
		if c.Err != nil {
			recordFail(f, c.System, c.Err)
			continue
		}
		if c.Skipped {
			skipped++
			continue
		}
		if flagReconcileSystem != "" && c.System != flagReconcileSystem {
			skipped++
			continue
		}
		res, ierr := importManifest(ctx, cli, c.System, c.Req, flagDryRun)
		if ierr != nil {
			recordFail(f, c.System, ierr)
			continue
		}
		imported++
		if activeBySystem[c.System] == nil {
			activeBySystem[c.System] = map[string]struct{}{}
		}
		for _, id := range res.GetPointIds() {
			activeBySystem[c.System][id] = struct{}{}
		}
		if !flagQuiet {
			fmt.Fprintf(os.Stderr, "  import %s (system=%s upserted=%d superseded=%d)\n",
				f, c.System, res.GetUpserted(), res.GetSuperseded())
		}
	}

	results, err := reconcileSweepSystems(ctx, cli, activeBySystem, failedSystems)
	if err != nil {
		return err
	}

	renderReconcileSummary(reconcileSummary{
		Imported: imported, Skipped: skipped, Failed: failed,
		DryRun: flagDryRun, Systems: results,
	})

	if failed > 0 {
		if firstErr != nil {
			return fmt.Errorf("reconcile-dir: %d file(s) failed; first error: %w", failed, firstErr)
		}
		return fmt.Errorf("reconcile-dir: %d file(s) failed", failed)
	}
	return nil
}

type reconcileSystemResult struct {
	System     string
	ActiveIDs  int
	Deprecated int
	Skipped    bool
	SkipReason string
}

func reconcileSweepSystems(ctx context.Context, cli *apiclient.APIClient, activeBySystem map[string]map[string]struct{}, failedSystems map[string]struct{}) ([]reconcileSystemResult, error) {
	seen := map[string]struct{}{}
	systems := make([]string, 0, len(activeBySystem)+len(failedSystems))
	for s := range activeBySystem {
		seen[s] = struct{}{}
		systems = append(systems, s)
	}
	// Failed systems with zero successful imports never landed in
	// activeBySystem, but their skipped sweep must still be reported.
	for s := range failedSystems {
		if _, ok := seen[s]; !ok {
			systems = append(systems, s)
		}
	}
	sort.Strings(systems)

	out := make([]reconcileSystemResult, 0, len(systems))
	for _, system := range systems {
		ids := sortedSet(activeBySystem[system])
		r := reconcileSystemResult{System: system, ActiveIDs: len(ids)}
		if _, bad := failedSystems[system]; bad {
			r.Skipped = true
			r.SkipReason = "import had failures; active set not authoritative"
			if !flagQuiet {
				fmt.Fprintf(os.Stderr, "  skip-sweep %s (%s)\n", system, r.SkipReason)
			}
			out = append(out, r)
			continue
		}
		if len(ids) == 0 {
			out = append(out, r)
			continue
		}
		if flagDryRun {
			n, err := reconcileWouldDeprecate(ctx, cli, system, activeBySystem[system])
			if err != nil {
				return nil, err
			}
			r.Deprecated = n
			out = append(out, r)
			continue
		}
		req := apiclient.NewChaosChaosSweepPointsReq()
		req.SetActivePointIds(ids)
		resp, _, err := cli.ChaosAPI.ChaosSweepPoints(ctx, system).ChaosChaosSweepPointsReq(*req).Execute()
		if err != nil {
			return nil, fmt.Errorf("sweep system %s: %w", system, err)
		}
		if resp != nil && resp.Data != nil {
			r.Deprecated = int(resp.Data.GetDeprecated())
		}
		out = append(out, r)
	}
	return out, nil
}

// reconcileWouldDeprecate counts active Points the sweep would deprecate by
// listing the system's current active Points and subtracting the imported set.
// Only used for --dry-run, where no sweep is issued.
func reconcileWouldDeprecate(ctx context.Context, cli *apiclient.APIClient, system string, active map[string]struct{}) (int, error) {
	count := 0
	var offset int32
	const page = int32(500)
	for {
		resp, _, err := cli.ChaosAPI.ChaosListSystemPoints(ctx, system).
			Status("active").Limit(page).Offset(offset).Execute()
		if err != nil {
			return 0, fmt.Errorf("list active points for %s: %w", system, err)
		}
		if resp == nil || resp.Data == nil {
			break
		}
		pts := resp.Data.Points
		for _, p := range pts {
			if _, kept := active[strDeref(p.Id)]; !kept {
				count++
			}
		}
		if int32(len(pts)) < page {
			break
		}
		offset += page
	}
	return count, nil
}

func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type reconcileSummary struct {
	Imported, Skipped, Failed int
	DryRun                    bool
	Systems                   []reconcileSystemResult
}

func renderReconcileSummary(s reconcileSummary) {
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		sysOut := make([]map[string]any, 0, len(s.Systems))
		for _, r := range s.Systems {
			entry := map[string]any{
				"system":     r.System,
				"active_ids": r.ActiveIDs,
				"deprecated": r.Deprecated,
				"swept":      !r.Skipped,
			}
			if r.Skipped {
				entry["skip_reason"] = r.SkipReason
			}
			sysOut = append(sysOut, entry)
		}
		output.PrintJSON(map[string]any{
			"imported": s.Imported,
			"skipped":  s.Skipped,
			"failed":   s.Failed,
			"dry_run":  s.DryRun,
			"systems":  sysOut,
		})
		return
	}
	headers := []string{"SYSTEM", "ACTIVE_IDS", "DEPRECATED", "SWEPT"}
	rows := make([][]string, 0, len(s.Systems))
	for _, r := range s.Systems {
		swept := "yes"
		dep := fmt.Sprintf("%d", r.Deprecated)
		if r.Skipped {
			swept = "no (" + r.SkipReason + ")"
			dep = "-"
		}
		rows = append(rows, []string{r.System, fmt.Sprintf("%d", r.ActiveIDs), dep, swept})
	}
	output.PrintTable(headers, rows)
	if !flagQuiet {
		mode := "COMMITTED"
		if s.DryRun {
			mode = "DRY-RUN (no sweep issued)"
		}
		fmt.Fprintf(os.Stderr, "reconcile-dir (%s): imported=%d skipped=%d failed=%d\n",
			mode, s.Imported, s.Skipped, s.Failed)
	}
}

func init() {
	manifestReconcileDirCmd.Flags().StringVar(&flagReconcileSystem, "system", "",
		"Restrict import + sweep to manifests for this system only")
	manifestCmd.AddCommand(manifestReconcileDirCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(manifestReconcileDirCmd)
	})
}
