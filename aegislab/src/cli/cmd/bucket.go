package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/client"
	"aegis/cli/internal/cli/blobref"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

// cliBucketLifecycleRule mirrors the server's BucketLifecycleRule shape;
// kept local to the CLI because the generated SDK does not yet expose it.
//
// TODO(SDK-regen): drop this duplicate once `just swagger-init && just
// generate-portal` adds BlobBucketLifecycle to the generated SDK.
type cliBucketLifecycleRule struct {
	Name            string `json:"name"`
	MatchPrefix     string `json:"match_prefix"`
	ExpireAfterDays int    `json:"expire_after_days"`
	Action          string `json:"action"`
}

type cliBucketLifecycle struct {
	Rules []cliBucketLifecycleRule `json:"rules"`
}

// readLifecycleFile parses + structurally validates a lifecycle JSON
// file. We only check shape here; server-side Validate() is the
// authoritative gate.
func readLifecycleFile(path string) (*cliBucketLifecycle, error) {
	body, err := os.ReadFile(path) //nolint:gosec // path is user-supplied CLI input
	if err != nil {
		return nil, fmt.Errorf("read lifecycle file: %w", err)
	}
	var out cliBucketLifecycle
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, usageErrorf("invalid lifecycle JSON in %q: %v", path, err)
	}
	return &out, nil
}

var bucketCmd = &cobra.Command{
	Use:   "bucket",
	Short: "Manage blob buckets (list / create / get / rm)",
	Long: `Manage blob storage buckets exposed by the aegis-blob microservice.

Buckets are the top-level grouping for objects accessed via 'aegisctl blob ...'.
List the buckets you can see, inspect one, or provision a new bucket against
a configured driver (localfs / s3).

EXAMPLES:
  # List buckets
  aegisctl bucket ls

  # Inspect a bucket
  aegisctl bucket get aegis-pages

  # Provision a new bucket on the localfs driver
  aegisctl bucket create aegis-scratch --driver localfs --root /var/lib/aegis-blob/aegis-scratch

  # See related noun-group:
  aegisctl blob ls aegis-pages:
`,
}

// --- bucket ls ---

var bucketLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List buckets the current token can see",
	Long: `List buckets registered with the aegis-blob service.

The columns are NAME / DRIVER / MAX_OBJECT_BYTES / PUBLIC. Use --output json
or --output ndjson for machine-readable output.

EXAMPLES:
  aegisctl bucket ls
  aegisctl bucket ls --output ndjson | jq .
`,
	Args: requireNoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		resp, _, err := cli.BlobAPI.BlobListBuckets(ctx).Execute()
		if err != nil {
			return err
		}
		var items []apiclient.BlobBucketSummary
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
		for _, b := range items {
			pub := "false"
			if b.GetPublicRead() {
				pub = "true"
			}
			rows = append(rows, []string{
				b.GetName(),
				b.GetDriver(),
				strconv.FormatInt(int64(b.GetMaxObjectBytes()), 10),
				pub,
			})
		}
		output.PrintTable([]string{"NAME", "DRIVER", "MAX_OBJECT_BYTES", "PUBLIC"}, rows)
		return nil
	},
}

// --- bucket create ---

var (
	bucketCreateDriver         string
	bucketCreateRoot           string
	bucketCreateEndpoint       string
	bucketCreateRegion         string
	bucketCreatePublic         bool
	bucketCreateMaxObjectBytes int64
	bucketCreateRetentionDays  int
	bucketCreateLifecycleFile  string
	bucketCreateBucketName     string
)

var bucketCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Provision a new bucket against a configured driver",
	Long: `Create a new bucket. The bucket name must be 2-63 chars, lowercase, and may
contain digits, dots, dashes, and underscores (matches the server-side regex).

For the localfs driver, --root is required and points at the on-disk directory
the driver will own. For the s3 driver, --endpoint and --region are typical,
plus --bucket to specify the underlying S3 bucket name.

--lifecycle <path.json> attaches a retention policy to the bucket. The JSON
must be {"rules":[{"name":..., "match_prefix":..., "expire_after_days":...,
"action":"delete"}, ...]}. Rules are persisted but execution is deferred —
the server validates and stores them; no GC sweep consumes them yet.

EXAMPLES:
  # Localfs bucket
  aegisctl bucket create aegis-scratch --driver localfs --root /var/lib/aegis-blob/aegis-scratch

  # Public S3-backed bucket
  aegisctl bucket create cdn-assets --driver s3 --endpoint https://oss.example --region cn-hangzhou --bucket cdn-assets --public

  # With a lifecycle policy
  aegisctl bucket create scratch --driver localfs --root /var/lib/aegis-blob/scratch --lifecycle policy.json
`,
	Args: cobra.ExactArgs(1),
	// TODO(SDK-regen): once the SDK exposes BlobCreateBucketReq.Lifecycle,
	// drop the raw-HTTP path below in favour of the typed client.
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !blobref.IsValidBucketName(name) {
			return usageErrorf("invalid bucket name %q: must match ^[a-z0-9][a-z0-9._-]{1,62}$", name)
		}
		if bucketCreateDriver == "" {
			return usageErrorf("--driver is required (localfs|s3)")
		}
		var lifecycle *cliBucketLifecycle
		if bucketCreateLifecycleFile != "" {
			lc, err := readLifecycleFile(bucketCreateLifecycleFile)
			if err != nil {
				return err
			}
			lifecycle = lc
		}

		if err := requireAPIContext(true); err != nil {
			return err
		}

		payload := map[string]any{
			"name":   name,
			"driver": bucketCreateDriver,
		}
		if bucketCreateRoot != "" {
			payload["root"] = bucketCreateRoot
		}
		if bucketCreateEndpoint != "" {
			payload["endpoint"] = bucketCreateEndpoint
		}
		if bucketCreateRegion != "" {
			payload["region"] = bucketCreateRegion
		}
		if bucketCreateBucketName != "" {
			payload["bucket"] = bucketCreateBucketName
		}
		if bucketCreatePublic {
			payload["public_read"] = true
		}
		if bucketCreateMaxObjectBytes > 0 {
			payload["max_object_bytes"] = bucketCreateMaxObjectBytes
		}
		if bucketCreateRetentionDays > 0 {
			payload["retention_days"] = bucketCreateRetentionDays
		}
		if lifecycle != nil {
			payload["lifecycle"] = lifecycle
		}

		if flagDryRun {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "would_create": payload})
			} else {
				fmt.Fprintf(os.Stderr, "Dry run — would POST /api/v2/blob/buckets %+v\n", payload)
			}
			return nil
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal create-bucket request: %w", err)
		}
		_, status, respBody, err := bucketDoJSON(http.MethodPost, "/api/v2/blob/buckets", body)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return bucketTranslateHTTPError("bucket create", status, respBody)
		}
		var env struct {
			Data *apiclient.BlobBucketSummary `json:"data"`
		}
		if err := json.Unmarshal(respBody, &env); err != nil {
			return fmt.Errorf("decode create-bucket response: %w", err)
		}
		if env.Data == nil {
			return fmt.Errorf("create bucket: empty response")
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(env.Data)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("Bucket %q created (driver=%s)", env.Data.GetName(), env.Data.GetDriver()))
		return nil
	},
}

// --- bucket get ---

var bucketGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show the configuration and metadata of a bucket",
	Long: `Show config for a single bucket: driver, retention, max-object-bytes,
and public/ACL summary (when available).

EXAMPLES:
  aegisctl bucket get aegis-pages
  aegisctl bucket get aegis-pages --output json | jq .max_object_bytes
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAPIContext(true); err != nil {
			return err
		}
		name := args[0]
		cli, ctx := newAPIClient()
		resp, _, err := cli.BlobAPI.BlobListBuckets(ctx).Execute()
		if err != nil {
			return err
		}
		var found *apiclient.BlobBucketSummary
		if resp.Data != nil {
			items := resp.Data.GetItems()
			for i := range items {
				if items[i].GetName() == name {
					found = &items[i]
					break
				}
			}
		}
		if found == nil {
			return notFoundErrorf("bucket %q not found", name)
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(found)
			return nil
		}
		fmt.Printf("Name:             %s\n", found.GetName())
		fmt.Printf("Driver:           %s\n", found.GetDriver())
		fmt.Printf("MaxObjectBytes:   %d\n", found.GetMaxObjectBytes())
		fmt.Printf("RetentionDays:    %d\n", found.GetRetentionDays())
		fmt.Printf("PublicRead:       %t\n", found.GetPublicRead())
		return nil
	},
}

// --- bucket rm ---

var (
	bucketRmYes   bool
	bucketRmForce bool
)

var bucketRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"delete"},
	Short:   "Delete a bucket (refuses to delete non-empty buckets without --force)",
	Long: `Delete a bucket. Refuses to delete a non-empty bucket unless --force is
given (which forwards force=true on the server-side DELETE).

EXAMPLES:
  aegisctl bucket rm aegis-scratch --yes
  aegisctl bucket rm aegis-scratch --force --yes
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := requireAPIContext(true); err != nil {
			return err
		}
		if flagDryRun {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "would_delete": name, "force": bucketRmForce})
			} else {
				fmt.Fprintf(os.Stderr, "Dry run — would DELETE /api/v2/blob/buckets/%s (force=%t)\n", name, bucketRmForce)
			}
			return nil
		}
		yes := bucketRmYes || bucketRmForce
		if err := confirmDeletion("bucket", name, 0, yes); err != nil {
			return err
		}
		cli, ctx := newAPIClient()
		req := cli.BlobAPI.BlobDeleteBucket(ctx, name)
		if bucketRmForce {
			req = req.Force(true)
		}
		if _, _, err := req.Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("Bucket %q deleted", name))
		return nil
	},
}

// --- bucket lifecycle ---

var (
	bucketLifecycleSetFile string
	bucketLifecycleClrYes  bool
)

var bucketLifecycleCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Manage a bucket's lifecycle policy (get / set / clear)",
	Long: `Manage the persisted lifecycle policy for a bucket.

The policy is a JSON document with a flat list of rules:

  {
    "rules": [
      { "name": "expire-tmp", "match_prefix": "tmp/", "expire_after_days": 7, "action": "delete" }
    ]
  }

Server-side validation caps the policy at 50 rules; expire_after_days must
fall in [1, 3650]; match_prefix is ≤ 256 chars; action must be "delete";
rule names must be unique.

NOTE: persistence only. Today the server stores and returns the policy; the
deletion sweep does not yet evaluate it.

EXAMPLES:
  aegisctl bucket lifecycle get aegis-scratch
  aegisctl bucket lifecycle set aegis-scratch -f policy.json
  aegisctl bucket lifecycle clear aegis-scratch --yes
`,
}

var bucketLifecycleGetCmd = &cobra.Command{
	Use:   "get <bucket>",
	Short: "Print the bucket's current lifecycle policy",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !blobref.IsValidBucketName(name) {
			return usageErrorf("invalid bucket name %q", name)
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		_, status, body, err := bucketDoJSON(http.MethodGet, "/api/v2/blob/buckets/"+name+"/lifecycle", nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return bucketTranslateHTTPError("bucket lifecycle get", status, body)
		}
		var env struct {
			Data *cliBucketLifecycle `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return fmt.Errorf("decode lifecycle response: %w", err)
		}
		if env.Data == nil {
			env.Data = &cliBucketLifecycle{Rules: []cliBucketLifecycleRule{}}
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(env.Data)
			return nil
		}
		if len(env.Data.Rules) == 0 {
			fmt.Println("(no lifecycle policy)")
			return nil
		}
		rows := make([][]string, 0, len(env.Data.Rules))
		for _, r := range env.Data.Rules {
			rows = append(rows, []string{r.Name, r.MatchPrefix, strconv.Itoa(r.ExpireAfterDays), r.Action})
		}
		output.PrintTable([]string{"NAME", "MATCH_PREFIX", "EXPIRE_AFTER_DAYS", "ACTION"}, rows)
		return nil
	},
}

var bucketLifecycleSetCmd = &cobra.Command{
	Use:   "set <bucket> -f <path.json>",
	Short: "Replace the bucket's lifecycle policy from a JSON file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !blobref.IsValidBucketName(name) {
			return usageErrorf("invalid bucket name %q", name)
		}
		if bucketLifecycleSetFile == "" {
			return usageErrorf("-f / --file is required")
		}
		lc, err := readLifecycleFile(bucketLifecycleSetFile)
		if err != nil {
			return err
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		if flagDryRun {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{
					"dry_run":    true,
					"would_put":  "/api/v2/blob/buckets/" + name + "/lifecycle",
					"rule_count": len(lc.Rules),
					"policy":     lc,
				})
			} else {
				fmt.Fprintf(os.Stderr, "Dry run — would PUT /api/v2/blob/buckets/%s/lifecycle (%d rule(s))\n", name, len(lc.Rules))
			}
			return nil
		}
		body, err := json.Marshal(lc)
		if err != nil {
			return fmt.Errorf("marshal lifecycle: %w", err)
		}
		_, status, respBody, err := bucketDoJSON(http.MethodPut, "/api/v2/blob/buckets/"+name+"/lifecycle", body)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return bucketTranslateHTTPError("bucket lifecycle set", status, respBody)
		}
		output.PrintInfo(fmt.Sprintf("lifecycle updated for %q (%d rule(s))", name, len(lc.Rules)))
		return nil
	},
}

var bucketLifecycleClearCmd = &cobra.Command{
	Use:   "clear <bucket>",
	Short: "Remove the bucket's lifecycle policy (PUT empty rules list)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !blobref.IsValidBucketName(name) {
			return usageErrorf("invalid bucket name %q", name)
		}
		if err := requireAPIContext(true); err != nil {
			return err
		}
		if !bucketLifecycleClrYes && !flagDryRun {
			return usageErrorf("--yes is required to clear the policy on %q", name)
		}
		empty := cliBucketLifecycle{Rules: []cliBucketLifecycleRule{}}
		if flagDryRun {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{
					"dry_run":   true,
					"would_put": "/api/v2/blob/buckets/" + name + "/lifecycle",
					"policy":    empty,
				})
			} else {
				fmt.Fprintf(os.Stderr, "Dry run — would PUT /api/v2/blob/buckets/%s/lifecycle (empty rules)\n", name)
			}
			return nil
		}
		body, _ := json.Marshal(empty)
		_, status, respBody, err := bucketDoJSON(http.MethodPut, "/api/v2/blob/buckets/"+name+"/lifecycle", body)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return bucketTranslateHTTPError("bucket lifecycle clear", status, respBody)
		}
		output.PrintInfo(fmt.Sprintf("lifecycle cleared for %q", name))
		return nil
	},
}

// bucketDoJSON is the raw-HTTP fallback used until the SDK regen exposes
// the Lifecycle field on BlobCreateBucketReq and the new lifecycle
// endpoints. Mirrors the pagesDoMultipart pattern in page.go.
//
// TODO(SDK-regen): once `just generate-portal` adds BlobGetBucketLifecycle
// / BlobPutBucketLifecycle, switch callers to the typed client and remove
// this helper.
func bucketDoJSON(method, path string, body []byte) (*http.Response, int, []byte, error) {
	if flagServer == "" {
		return nil, 0, nil, missingEnvErrorf("--server or AEGIS_SERVER is required")
	}
	rawURL := strings.TrimRight(flagServer, "/") + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, rawURL, reader)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if flagToken != "" {
		req.Header.Set("Authorization", "Bearer "+flagToken)
	}
	httpClient := &http.Client{Transport: client.TransportFor(resolveTLSOptions())}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, resp.StatusCode, respBody, nil
}

func bucketTranslateHTTPError(op string, status int, body []byte) error {
	msg := serverErrorMessage(body, status)
	switch status {
	case http.StatusBadRequest:
		return usageErrorf("%s: %s", op, msg)
	case http.StatusUnauthorized, http.StatusForbidden:
		return authErrorf("%s: %s", op, msg)
	case http.StatusNotFound:
		return notFoundErrorf("%s: %s", op, msg)
	case http.StatusConflict:
		return conflictErrorf("%s: %s", op, msg)
	default:
		if status >= 500 {
			return fmt.Errorf("%s: server returned HTTP %d: %s", op, status, msg)
		}
		return fmt.Errorf("%s: HTTP %d: %s", op, status, msg)
	}
}

func init() {
	bucketCreateCmd.Flags().StringVar(&bucketCreateDriver, "driver", "", "Storage driver: localfs|s3 (required)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateRoot, "root", "", "Root directory (localfs driver)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateEndpoint, "endpoint", "", "S3 endpoint URL (s3 driver)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateRegion, "region", "", "S3 region (s3 driver)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateBucketName, "bucket", "", "Underlying S3 bucket name (s3 driver)")
	bucketCreateCmd.Flags().BoolVar(&bucketCreatePublic, "public", false, "Mark bucket public-read")
	bucketCreateCmd.Flags().Int64Var(&bucketCreateMaxObjectBytes, "max-object-bytes", 0, "Per-object size cap in bytes (0 = server default)")
	bucketCreateCmd.Flags().IntVar(&bucketCreateRetentionDays, "retention-days", 0, "Retention window in days (0 = no retention)")
	bucketCreateCmd.Flags().StringVar(&bucketCreateLifecycleFile, "lifecycle", "", "Lifecycle policy JSON file (persisted; execution deferred)")

	bucketRmCmd.Flags().BoolVar(&bucketRmYes, "yes", false, "Skip confirmation prompt")
	bucketRmCmd.Flags().BoolVar(&bucketRmForce, "force", false, "Delete non-empty buckets (and implies --yes)")

	bucketLifecycleSetCmd.Flags().StringVarP(&bucketLifecycleSetFile, "file", "f", "", "Lifecycle policy JSON file (required)")
	bucketLifecycleClearCmd.Flags().BoolVar(&bucketLifecycleClrYes, "yes", false, "Confirm policy removal")

	bucketLifecycleCmd.AddCommand(bucketLifecycleGetCmd)
	bucketLifecycleCmd.AddCommand(bucketLifecycleSetCmd)
	bucketLifecycleCmd.AddCommand(bucketLifecycleClearCmd)

	bucketCmd.AddCommand(bucketLsCmd)
	bucketCmd.AddCommand(bucketCreateCmd)
	bucketCmd.AddCommand(bucketGetCmd)
	bucketCmd.AddCommand(bucketRmCmd)
	bucketCmd.AddCommand(bucketLifecycleCmd)

	cobra.OnInitialize(func() {
		markDryRunSupported(bucketCreateCmd)
		markDryRunSupported(bucketRmCmd)
		markDryRunSupported(bucketLifecycleSetCmd)
		markDryRunSupported(bucketLifecycleClearCmd)
	})
}
