package cmd

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"aegis/cli/client"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

// share API DTOs (mirror what blob's share handler returns).

type shareUploadResp struct {
	ShortCode string `json:"short_code"`
	ShareURL  string `json:"share_url"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Size      int64  `json:"size"`
}

type shareLinkView struct {
	ShortCode        string `json:"short_code"`
	OriginalFilename string `json:"original_filename"`
	ContentType      string `json:"content_type"`
	SizeBytes        int64  `json:"size_bytes"`
	ViewCount        int    `json:"view_count"`
	MaxViews         *int   `json:"max_views,omitempty"`
	CreatedAt        string `json:"created_at"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	Status           int    `json:"status"`
	ShareURL         string `json:"share_url"`
}

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

		fields := map[string]string{}
		if shareUploadTTL != "" {
			secs, err := parseTTL(shareUploadTTL)
			if err != nil {
				return err
			}
			fields["ttl_seconds"] = strconv.FormatInt(secs, 10)
		}
		if shareUploadMaxViews > 0 {
			fields["max_views"] = strconv.Itoa(shareUploadMaxViews)
		}

		c := newClient()
		var resp shareUploadResp
		if err := c.PostMultipart("/api/v2/share/upload", "file", path, fields, &resp); err != nil {
			return fmt.Errorf("share upload: %w", err)
		}

		fullURL := joinURL(flagServer, resp.ShareURL)

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"short_code": resp.ShortCode,
				"share_url":  fullURL,
				"size_bytes": resp.Size,
				"expires_at": resp.ExpiresAt,
			})
			return nil
		}
		fmt.Printf("Short code:  %s\n", resp.ShortCode)
		fmt.Printf("Share URL:   %s\n", fullURL)
		fmt.Printf("Size bytes:  %d\n", resp.Size)
		if resp.ExpiresAt != "" {
			fmt.Printf("Expires at:  %s\n", resp.ExpiresAt)
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
		q := url.Values{}
		q.Set("page", strconv.Itoa(shareListPage))
		q.Set("size", strconv.Itoa(shareListSize))
		if shareListIncludeExpired {
			q.Set("include_expired", "true")
		}

		c := newClient()
		var resp client.APIResponse[client.PaginatedData[shareLinkView]]
		if err := c.Get("/api/v2/share?"+q.Encode(), &resp); err != nil {
			return err
		}
		switch output.OutputFormat(flagOutput) {
		case output.FormatJSON:
			output.PrintJSON(resp.Data)
			return nil
		case output.FormatNDJSON:
			if err := output.PrintMetaJSON(resp.Data.Pagination); err != nil {
				return err
			}
			return output.PrintNDJSON(resp.Data.Items)
		}
		rows := make([][]string, 0, len(resp.Data.Items))
		for _, it := range resp.Data.Items {
			rows = append(rows, []string{
				it.ShortCode,
				it.OriginalFilename,
				strconv.FormatInt(it.SizeBytes, 10),
				strconv.Itoa(it.ViewCount),
				it.ExpiresAt,
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
		c := newClient()
		var resp client.APIResponse[any]
		if err := c.Delete("/api/v2/share/"+url.PathEscape(code), &resp); err != nil {
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

		// /s/:code is auth=none on the gateway, but the client still sends the
		// Authorization header if a token is loaded — that's harmless.
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
	// time.ParseDuration is overkill but handles m/h naturally; do it lazily.
	// We only need a rough bridge — duplicate it inline rather than pulling time here.
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
// with a server-side path/relative URL. If `rel` is already absolute,
// it's returned unchanged.
func joinURL(base, rel string) string {
	if rel == "" {
		return base
	}
	if strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
		return rel
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}

// _ keeps filepath imported if future helpers need it.
var _ = filepath.Base

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
