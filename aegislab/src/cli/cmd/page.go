package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"aegis/cli/apiclient"
	apiclientext "aegis/cli/apiclient_ext"
	"aegis/cli/internal/cli/pagedir"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var pageCmd = &cobra.Command{
	Use:   "page",
	Short: "Manage static-site pages (markdown + assets) served at /p/<slug>",
	Long: `Page sites are small static-content bundles (markdown + optional CSS / JS /
images) uploaded through the aegis pages module and served at /p/<slug>.

EXAMPLES:
  aegisctl page push ./my-site/ --slug release-notes --visibility public_listed
  aegisctl page push notes.md --title "Quick notes"
  aegisctl page ls
  aegisctl page ls --public --limit 10
  aegisctl page get release-notes
  aegisctl page rm release-notes --yes
  aegisctl page open release-notes`,
}

// ---------------------------------------------------------------------------
// page push
// ---------------------------------------------------------------------------

var (
	pagePushSlug       string
	pagePushVisibility string
	pagePushTitle      string
)

var pagePushCmd = &cobra.Command{
	Use:   "push <dir-or-file>",
	Short: "Create a page site from a directory or single .md file",
	Args:  exactArgs(1, "push <dir-or-file>"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		src := args[0]
		plan, err := pagedir.CollectFromPath(src)
		if err != nil {
			return err
		}

		// Frontmatter defaults — only consulted when the caller didn't pass
		// the corresponding flag. We always look at index.md for a directory
		// push, and at the file itself for a single-file push.
		fmPath := frontmatterCandidate(src, plan)
		var fmDef pagedir.FrontmatterDefaults
		if fmPath != "" {
			if def, err := pagedir.ParseFrontmatterFile(fmPath); err == nil {
				fmDef = def
			}
		}

		slug := pagePushSlug
		if slug == "" {
			slug = fmDef.Slug
		}
		if slug == "" {
			// Best-effort default from the input filename / dirname.
			if plan.SingleFile {
				slug = pagedir.DefaultSlugFromPath(plan.Entries[0].RelPath)
			} else {
				slug = pagedir.DefaultSlugFromPath(filepath.Base(filepath.Clean(src)))
			}
		}

		title := pagePushTitle
		if title == "" {
			title = fmDef.Title
		}
		if title == "" && fmPath != "" {
			if f, err := os.Open(fmPath); err == nil {
				title = pagedir.FirstH1(f)
				_ = f.Close()
			}
		}

		visibility := pagePushVisibility
		if visibility != "" {
			switch visibility {
			case "public_listed", "public_unlisted", "private":
			default:
				return usageErrorf("invalid --visibility %q; expected public_listed|public_unlisted|private", visibility)
			}
		}

		if flagDryRun {
			return pagePushDryRun(plan, slug, title, visibility)
		}

		resp, err := pagePushUpload(plan, slug, title, visibility)
		if err != nil {
			return err
		}
		return pagePushPrint(resp)
	},
}

// frontmatterCandidate returns the path to the markdown file the CLI should
// inspect for slug/title defaults. Single-file push uses that file; directory
// push prefers `index.md` at the root, falling back to "" (no defaults).
func frontmatterCandidate(src string, plan *pagedir.Plan) string {
	if plan.SingleFile {
		return plan.Entries[0].AbsPath
	}
	for _, e := range plan.Entries {
		if e.RelPath == "index.md" || e.RelPath == "index.markdown" {
			return e.AbsPath
		}
	}
	return ""
}

func pagePushDryRun(plan *pagedir.Plan, slug, title, visibility string) error {
	paths := make([]string, 0, len(plan.Entries))
	for _, e := range plan.Entries {
		paths = append(paths, e.RelPath)
	}
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(map[string]any{
			"dry_run":     true,
			"slug":        slug,
			"title":       title,
			"visibility":  visibility,
			"files":       paths,
			"total_bytes": plan.TotalBytes,
		})
		return nil
	}
	fmt.Fprintf(os.Stderr, "Dry run — would POST /api/v2/pages (slug=%q title=%q visibility=%q)\n",
		slug, title, visibility)
	fmt.Println("Files to upload:")
	for _, e := range plan.Entries {
		fmt.Printf("  %s  (%d bytes)\n", e.RelPath, e.Size)
	}
	fmt.Printf("Total: %d bytes, %d file(s)\n", plan.TotalBytes, len(plan.Entries))
	return nil
}

// pagePushUpload delegates to the apiclient_ext typed wrapper, which builds
// the multipart body with per-part site-relative filenames (the generated
// SDK can't — it accepts only a single *os.File and uses filepath.Base).
// We keep the CLI's rich HTTP-status → exit-code mapping local to this file.
func pagePushUpload(plan *pagedir.Plan, slug, title, visibility string) (*apiclient.PagesPageSiteResponse, error) {
	if flagServer == "" {
		return nil, missingEnvErrorf("--server or AEGIS_SERVER is required")
	}
	files, cleanup, err := openPlanUploads(plan)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	cli, ctx := newAPIClient()
	data, httpResp, err := apiclientext.PagesCreateMulti(ctx, cli.GetConfig(), slug, visibility, title, files)
	if err != nil {
		return nil, mapPagesHTTPError(httpResp, err)
	}
	if data == nil {
		return nil, fmt.Errorf("page push: empty response data")
	}
	return data, nil
}

// openPlanUploads turns a pagedir.Plan into apiclient_ext.FileUpload parts.
// The returned cleanup func closes every opened file regardless of whether
// the upload succeeded or failed; deferring it at the call site keeps file
// descriptors bounded even on early returns.
func openPlanUploads(plan *pagedir.Plan) ([]apiclientext.FileUpload, func(), error) {
	files := make([]apiclientext.FileUpload, 0, len(plan.Entries))
	opened := make([]*os.File, 0, len(plan.Entries))
	cleanup := func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}
	for _, e := range plan.Entries {
		f, err := os.Open(e.AbsPath) //nolint:gosec // path comes from our own walker, already validated.
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("open %q: %w", e.AbsPath, err)
		}
		opened = append(opened, f)
		files = append(files, apiclientext.FileUpload{
			Name:     "files",
			Filename: e.RelPath,
			Content:  f,
		})
	}
	return files, cleanup, nil
}

// mapPagesHTTPError converts a wrapper-returned error + *http.Response into
// the CLI's exit-code-bearing error sentinels. When httpResp is nil we have
// a transport-level failure and pass the error through unchanged.
func mapPagesHTTPError(httpResp *http.Response, err error) error {
	if httpResp == nil {
		return err
	}
	body, _ := io.ReadAll(httpResp.Body)
	msg := serverErrorMessage(body, httpResp.StatusCode)
	if rid := httpResp.Header.Get("X-Request-Id"); rid != "" {
		msg = msg + " (request_id=" + rid + ")"
	}
	switch httpResp.StatusCode {
	case http.StatusBadRequest:
		if strings.Contains(strings.ToLower(msg), "slug") &&
			strings.Contains(strings.ToLower(msg), "tak") {
			return conflictErrorf("page push: %s", msg)
		}
		return usageErrorf("page push: %s", msg)
	case http.StatusUnauthorized, http.StatusForbidden:
		return authErrorf("page push: %s", msg)
	case http.StatusConflict:
		return conflictErrorf("page push: %s", msg)
	case http.StatusRequestEntityTooLarge:
		return usageErrorf("page push: %s", msg)
	default:
		if httpResp.StatusCode >= 500 {
			return fmt.Errorf("page push: server returned HTTP %d: %s", httpResp.StatusCode, msg)
		}
		return fmt.Errorf("page push: HTTP %d: %s", httpResp.StatusCode, msg)
	}
}

func serverErrorMessage(body []byte, status int) string {
	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &env) == nil && env.Message != "" {
		return env.Message
	}
	if len(body) == 0 {
		return http.StatusText(status)
	}
	return strings.TrimSpace(string(body))
}

func pagePushPrint(resp *apiclient.PagesPageSiteResponse) error {
	shareURL := joinURL(flagServer, fmt.Sprintf("/p/%s", resp.GetSlug()))
	if output.OutputFormat(flagOutput) == output.FormatJSON {
		output.PrintJSON(map[string]any{
			"id":         resp.GetId(),
			"slug":       resp.GetSlug(),
			"title":      resp.GetTitle(),
			"visibility": resp.GetVisibility(),
			"share_url":  shareURL,
			"file_count": resp.GetFileCount(),
			"size_bytes": resp.GetSizeBytes(),
		})
		return nil
	}
	fmt.Printf("ID:          %d\n", resp.GetId())
	fmt.Printf("Slug:        %s\n", resp.GetSlug())
	fmt.Printf("Title:       %s\n", resp.GetTitle())
	fmt.Printf("Visibility:  %s\n", resp.GetVisibility())
	fmt.Printf("Files:       %d\n", resp.GetFileCount())
	fmt.Printf("Size bytes:  %d\n", resp.GetSizeBytes())
	fmt.Printf("Share URL:   %s\n", shareURL)
	return nil
}

// ---------------------------------------------------------------------------
// page ls
// ---------------------------------------------------------------------------

var (
	pageListMine   bool
	pageListPublic bool
	pageListLimit  int
	pageListOffset int
)

const (
	pageListDefaultLimit = 20
	pageListAutoCap      = 200
)

var pageListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List page sites",
	RunE: func(cmd *cobra.Command, args []string) error {
		if pageListMine && pageListPublic {
			return usageErrorf("--mine and --public are mutually exclusive")
		}
		cli, ctx := newAPIClient()

		// Pagination: if --limit was explicitly passed, do one request with
		// the user's window. Otherwise auto-follow up to pageListAutoCap.
		limit := pageListLimit
		offset := pageListOffset
		autoPage := !cmd.Flags().Lookup("limit").Changed && offset == 0

		var (
			items []apiclient.PagesPageSiteResponse
		)
		for {
			window := limit
			if window <= 0 {
				window = pageListDefaultLimit
			}
			page, err := pageListFetch(cli, ctx, pageListPublic, window, offset)
			if err != nil {
				return err
			}
			items = append(items, page...)
			if !autoPage {
				break
			}
			if len(page) < window {
				break
			}
			if len(items) >= pageListAutoCap {
				items = items[:pageListAutoCap]
				break
			}
			offset += len(page)
		}

		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			output.PrintJSON(items)
			return nil
		case output.FormatNDJSON:
			return output.PrintNDJSON(items)
		}
		rows := make([][]string, 0, len(items))
		for _, it := range items {
			rows = append(rows, []string{
				it.GetSlug(),
				it.GetTitle(),
				it.GetVisibility(),
				strconv.FormatInt(int64(it.GetFileCount()), 10),
				strconv.FormatInt(int64(it.GetSizeBytes()), 10),
				it.GetUpdatedAt(),
			})
		}
		output.PrintTable(
			[]string{"SLUG", "TITLE", "VISIBILITY", "FILES", "SIZE", "UPDATED"},
			rows,
		)
		return nil
	},
}

func pageListFetch(cli *apiclient.APIClient, ctx context.Context, public bool, limit, offset int) ([]apiclient.PagesPageSiteResponse, error) {
	if public {
		resp, _, err := cli.PagesAPI.PagesListPublic(ctx).
			Limit(int32(limit)).
			Offset(int32(offset)).
			Execute()
		if err != nil {
			return nil, err
		}
		if resp.Data == nil {
			return nil, nil
		}
		return resp.Data.GetItems(), nil
	}
	resp, _, err := cli.PagesAPI.PagesListMine(ctx).
		Limit(int32(limit)).
		Offset(int32(offset)).
		Execute()
	if err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, nil
	}
	return resp.Data.GetItems(), nil
}

// ---------------------------------------------------------------------------
// page get
// ---------------------------------------------------------------------------

var pageGetCmd = &cobra.Command{
	Use:   "get <slug-or-id>",
	Short: "Show details for one page site",
	Args:  exactArgs(1, "get <slug-or-id>"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		id, slug, err := resolvePageRef(args[0])
		if err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		resp, _, err := cli.PagesAPI.PagesDetail(ctx, int32(id)).Execute()
		if err != nil {
			return err
		}
		d := resp.Data
		if d == nil {
			return notFoundErrorf("page %q not found", args[0])
		}
		if d.GetSlug() == "" && slug != "" {
			d.SetSlug(slug)
		}
		shareURL := joinURL(flagServer, fmt.Sprintf("/p/%s", d.GetSlug()))
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"id":         d.GetId(),
				"slug":       d.GetSlug(),
				"title":      d.GetTitle(),
				"visibility": d.GetVisibility(),
				"file_count": d.GetFileCount(),
				"size_bytes": d.GetSizeBytes(),
				"created_at": d.GetCreatedAt(),
				"updated_at": d.GetUpdatedAt(),
				"share_url":  shareURL,
				"files":      d.GetFiles(),
			})
			return nil
		}
		fmt.Printf("ID:          %d\n", d.GetId())
		fmt.Printf("Slug:        %s\n", d.GetSlug())
		fmt.Printf("Title:       %s\n", d.GetTitle())
		fmt.Printf("Visibility:  %s\n", d.GetVisibility())
		fmt.Printf("Files:       %d\n", d.GetFileCount())
		fmt.Printf("Size bytes:  %d\n", d.GetSizeBytes())
		fmt.Printf("Created:     %s\n", d.GetCreatedAt())
		fmt.Printf("Updated:     %s\n", d.GetUpdatedAt())
		fmt.Printf("Share URL:   %s\n", shareURL)
		if files := d.GetFiles(); len(files) > 0 {
			fmt.Println("Files:")
			for _, f := range files {
				fmt.Printf("  %s  (%d bytes)\n", f.GetPath(), f.GetSizeBytes())
			}
		}
		return nil
	},
}

// resolvePageRef accepts either a numeric id or a slug. Numeric resolution is
// purely client-side; slug resolution lists the caller's pages (mine first,
// then public) and picks the first match. The SDK has no slug-lookup
// endpoint, so this list-and-filter is the only honest approach.
func resolvePageRef(ref string) (int, string, error) {
	if n, err := strconv.Atoi(ref); err == nil && n > 0 {
		return n, "", nil
	}
	cli, ctx := newAPIClient()
	for _, public := range []bool{false, true} {
		offset := 0
		for {
			items, err := pageListFetch(cli, ctx, public, 100, offset)
			if err != nil {
				if public {
					// /pages/public may be unauthenticated-OK; surface mine's
					// error preference by ignoring failures here.
					break
				}
				return 0, "", err
			}
			for _, it := range items {
				if it.GetSlug() == ref {
					return int(it.GetId()), it.GetSlug(), nil
				}
			}
			if len(items) < 100 {
				break
			}
			offset += len(items)
			if offset >= pageListAutoCap {
				break
			}
		}
	}
	return 0, "", notFoundErrorf("page %q not found", ref)
}

// ---------------------------------------------------------------------------
// page rm
// ---------------------------------------------------------------------------

var pageRemoveYes bool

var pageRemoveCmd = &cobra.Command{
	Use:     "rm <slug-or-id>",
	Aliases: []string{"delete", "remove"},
	Short:   "Delete a page site",
	Args:    exactArgs(1, "rm <slug-or-id>"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		id, slug, err := resolvePageRef(args[0])
		if err != nil {
			return err
		}
		label := slug
		if label == "" {
			label = strconv.Itoa(id)
		}
		if flagDryRun {
			fmt.Fprintf(os.Stderr, "Dry run — would DELETE /api/v2/pages/%d (%s)\n", id, label)
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "id": id, "slug": slug})
			} else {
				fmt.Printf("Would delete page %s (id %d)\n", label, id)
			}
			return nil
		}
		if err := confirmDeletion("page", label, id, pageRemoveYes); err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		if _, err := cli.PagesAPI.PagesDelete(ctx, int32(id)).Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("page %s (id %d) deleted", label, id))
		return nil
	},
}

// ---------------------------------------------------------------------------
// page open
// ---------------------------------------------------------------------------

var pageOpenCmd = &cobra.Command{
	Use:   "open <slug>",
	Short: "Open a page site's share URL in the default browser",
	Args:  exactArgs(1, "open <slug>"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagNonInteractive {
			return usageErrorf("aegisctl page open opens a browser; refused under --non-interactive")
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		// Verify the page exists; this catches typos and refuses to spawn a
		// browser at a URL the user can't actually reach.
		_, slug, err := resolvePageRef(args[0])
		if err != nil {
			return err
		}
		if slug == "" {
			slug = args[0]
		}
		shareURL := joinURL(flagServer, "/p/"+url.PathEscape(slug))
		if err := openBrowser(shareURL); err != nil {
			return err
		}
		output.PrintInfo("opening " + shareURL)
		return nil
	},
}

// openBrowser dispatches to the platform's "open the default browser" tool.
// Tested only at the call shape (exec.Command name + args); the actual
// browser launch is a UI-level concern.
func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd.exe", "/c", "start", "", rawURL)
	default:
		return fmt.Errorf("page open: no default browser launcher for GOOS=%s", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("page open: launch browser: %w", err)
	}
	// Don't Wait — the browser process typically outlives the CLI invocation.
	_ = cmd.Process.Release()
	return nil
}

// ---------------------------------------------------------------------------
// wiring
// ---------------------------------------------------------------------------

func init() {
	pagePushCmd.Flags().StringVar(&pagePushSlug, "slug", "", "Slug (defaults to frontmatter 'slug:' or filename stem)")
	pagePushCmd.Flags().StringVar(&pagePushVisibility, "visibility", "", "Visibility: public_listed|public_unlisted|private")
	pagePushCmd.Flags().StringVar(&pagePushTitle, "title", "", "Display title (defaults to frontmatter 'title:' or first H1)")

	pageListCmd.Flags().BoolVar(&pageListMine, "mine", true, "List your own pages (default)")
	pageListCmd.Flags().BoolVar(&pageListPublic, "public", false, "List publicly visible pages")
	pageListCmd.Flags().IntVar(&pageListLimit, "limit", pageListDefaultLimit, "Items per page")
	pageListCmd.Flags().IntVar(&pageListOffset, "offset", 0, "Offset into the result set")

	pageRemoveCmd.Flags().BoolVar(&pageRemoveYes, "yes", false, "Skip confirmation prompt")
	pageRemoveCmd.Flags().BoolVar(&pageRemoveYes, "force", false, "Alias for --yes")

	pageCmd.AddCommand(pagePushCmd)
	pageCmd.AddCommand(pageListCmd)
	pageCmd.AddCommand(pageGetCmd)
	pageCmd.AddCommand(pageRemoveCmd)
	pageCmd.AddCommand(pageOpenCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(pagePushCmd)
		markDryRunSupported(pageRemoveCmd)
	})
}
