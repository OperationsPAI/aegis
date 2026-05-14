package cmd

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var shareCmd = &cobra.Command{
	Use:   "share",
	Short: "Upload / list / revoke / download share links (blob/share)",
	Long: `Manage temporary file share links served by the aegis-blob microservice.

Authentication is the JWT saved by ` + "`aegisctl auth login`" + `; download
goes through the unauthenticated /s/<code> endpoint.`,
}

// --- share upload ---

var (
	shareUploadTTL      string
	shareUploadMaxViews int
)

var shareUploadCmd = &cobra.Command{
	Use:   "upload <file>",
	Short: "Upload a file and produce a public /s/<code> link",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		st, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat %q: %w", path, err)
		}
		if st.IsDir() {
			return fmt.Errorf("%q is a directory; share upload takes a single file", path)
		}
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %q: %w", path, err)
		}
		defer func() { _ = f.Close() }()

		cli, ctx := newAPIClient()
		req := cli.ShareAPI.ShareUpload(ctx).File(f)
		if shareUploadTTL != "" {
			secs, err := parseTTL(shareUploadTTL)
			if err != nil {
				return err
			}
			req = req.TtlSeconds(int32(secs))
		}
		if shareUploadMaxViews > 0 {
			req = req.MaxViews(int32(shareUploadMaxViews))
		}

		resp, _, err := req.Execute()
		if err != nil {
			return fmt.Errorf("share upload: %w", err)
		}
		data := resp.Data
		if data == nil {
			return fmt.Errorf("share upload: empty response")
		}

		fullURL := joinURL(flagServer, data.GetShareUrl())
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"short_code": data.GetShortCode(),
				"share_url":  fullURL,
				"size_bytes": data.GetSize(),
				"expires_at": data.GetExpiresAt(),
			})
			return nil
		}
		fmt.Printf("Short code:  %s\n", data.GetShortCode())
		fmt.Printf("Share URL:   %s\n", fullURL)
		fmt.Printf("Size bytes:  %d\n", data.GetSize())
		if exp := data.GetExpiresAt(); exp != "" {
			fmt.Printf("Expires at:  %s\n", exp)
		}
		return nil
	},
}

// --- share list ---

var (
	shareListPage           int
	shareListSize           int
	shareListIncludeExpired bool
)

var shareListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the share links you own",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()
		resp, _, err := cli.ShareAPI.ShareList(ctx).
			Page(int32(shareListPage)).
			Size(int32(shareListSize)).
			IncludeExpired(shareListIncludeExpired).
			Execute()
		if err != nil {
			return err
		}
		items := []apiclient.ShareLinkView{}
		if resp.Data != nil {
			items = resp.Data.GetItems()
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
				it.GetShortCode(),
				it.GetOriginalFilename(),
				strconv.FormatInt(int64(it.GetSizeBytes()), 10),
				strconv.FormatInt(int64(it.GetViewCount()), 10),
				it.GetExpiresAt(),
			})
		}
		output.PrintTable(
			[]string{"Code", "Filename", "Bytes", "Views", "Expires"},
			rows,
		)
		return nil
	},
}

// --- share revoke ---

var shareRevokeCmd = &cobra.Command{
	Use:   "revoke <code>",
	Short: "Revoke (delete) a share link",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code := args[0]
		cli, ctx := newAPIClient()
		if _, _, err := cli.ShareAPI.ShareRevoke(ctx, code).Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("share link %q revoked", code))
		return nil
	},
}

// --- share download ---

var shareDownloadOut string

var shareDownloadCmd = &cobra.Command{
	Use:   "download <code>",
	Short: "Download a share link's bytes via /s/<code>",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code := args[0]
		out := shareDownloadOut
		if out == "" {
			out = code + ".bin"
		}

		// /s/:code is auth=none and streams bytes; the generated client's
		// ShareRedirect treats 302 as an error and never returns the body
		// stream, so keep the manual streaming path here.
		c := newClient()
		f, err := os.Create(out)
		if err != nil {
			return fmt.Errorf("create %q: %w", out, err)
		}
		defer func() { _ = f.Close() }()
		n, err := c.GetRaw("/s/"+url.PathEscape(code), f)
		if err != nil {
			return fmt.Errorf("download: %w", err)
		}
		output.PrintInfo(fmt.Sprintf("downloaded %d bytes to %s", n, out))
		return nil
	},
}

// --- helpers ---

// parseTTL accepts either a bare integer (seconds) or a Go-style
// duration like "30m"/"24h"/"7d". Days are expanded manually because
// time.ParseDuration doesn't know about days.
func parseTTL(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty ttl")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	if strings.HasSuffix(s, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid ttl %q: %w", s, err)
		}
		return days * 24 * 60 * 60, nil
	}
	mult := int64(1)
	num := s
	switch {
	case strings.HasSuffix(s, "h"):
		mult, num = 60*60, strings.TrimSuffix(s, "h")
	case strings.HasSuffix(s, "m"):
		mult, num = 60, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "s"):
		mult, num = 1, strings.TrimSuffix(s, "s")
	default:
		return 0, fmt.Errorf("invalid ttl %q (use seconds, or 30m/24h/7d)", s)
	}
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid ttl %q: %w", s, err)
	}
	return v * mult, nil
}

// joinURL forms an absolute URL by combining `base` (e.g. http://host:8082)
// with a server-side path. Absolute `rel` is returned unchanged.
func joinURL(base, rel string) string {
	if rel == "" {
		return base
	}
	if strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
		return rel
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}

func init() {
	shareUploadCmd.Flags().StringVar(&shareUploadTTL, "ttl", "", "Link lifetime: seconds, e.g. 3600, 30m, 24h, 7d (server default if unset)")
	shareUploadCmd.Flags().IntVar(&shareUploadMaxViews, "max-views", 0, "Cap on download count (server default if 0)")

	shareListCmd.Flags().IntVar(&shareListPage, "page", 1, "Page number")
	shareListCmd.Flags().IntVar(&shareListSize, "size", 50, "Page size")
	shareListCmd.Flags().BoolVar(&shareListIncludeExpired, "include-expired", false, "Include expired/revoked links")

	shareDownloadCmd.Flags().StringVarP(&shareDownloadOut, "output", "o", "", "Output file path (default: <code>.bin)")

	shareCmd.AddCommand(shareUploadCmd)
	shareCmd.AddCommand(shareListCmd)
	shareCmd.AddCommand(shareRevokeCmd)
	shareCmd.AddCommand(shareDownloadCmd)
}
