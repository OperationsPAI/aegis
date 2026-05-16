package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"aegis/cli/apiclient"
	"aegis/cli/internal/cli/blobref"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

// readLifecycleFile parses a lifecycle JSON file into the SDK type.
// Only shape errors are returned here; server-side Validate() is the
// authoritative gate.
func readLifecycleFile(path string) (*apiclient.BlobBucketLifecycle, error) {
	body, err := os.ReadFile(path) //nolint:gosec // path is user-supplied CLI input
	if err != nil {
		return nil, fmt.Errorf("read lifecycle file: %w", err)
	}
	var out apiclient.BlobBucketLifecycle
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
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !blobref.IsValidBucketName(name) {
			return usageErrorf("invalid bucket name %q: must match ^[a-z0-9][a-z0-9._-]{1,62}$", name)
		}
		if bucketCreateDriver == "" {
			return usageErrorf("--driver is required (localfs|s3)")
		}
		var lifecycle *apiclient.BlobBucketLifecycle
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

		req := apiclient.NewBlobCreateBucketReq(bucketCreateDriver, name)
		if bucketCreateRoot != "" {
			req.SetRoot(bucketCreateRoot)
		}
		if bucketCreateEndpoint != "" {
			req.SetEndpoint(bucketCreateEndpoint)
		}
		if bucketCreateRegion != "" {
			req.SetRegion(bucketCreateRegion)
		}
		if bucketCreateBucketName != "" {
			req.SetBucket(bucketCreateBucketName)
		}
		if bucketCreatePublic {
			req.SetPublicRead(true)
		}
		if bucketCreateMaxObjectBytes > 0 {
			req.SetMaxObjectBytes(int32(bucketCreateMaxObjectBytes))
		}
		if bucketCreateRetentionDays > 0 {
			req.SetRetentionDays(int32(bucketCreateRetentionDays))
		}
		if lifecycle != nil {
			req.SetLifecycle(*lifecycle)
		}

		if flagDryRun {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{"dry_run": true, "would_create": req})
			} else {
				fmt.Fprintf(os.Stderr, "Dry run — would POST /api/v2/blob/buckets %+v\n", req)
			}
			return nil
		}

		cli, ctx := newAPIClient()
		resp, _, err := cli.BlobAPI.BlobCreateBucket(ctx).BlobCreateBucketReq(*req).Execute()
		if err != nil {
			return err
		}
		if resp == nil || resp.Data == nil {
			return fmt.Errorf("create bucket: empty response")
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}
		output.PrintInfo(fmt.Sprintf("Bucket %q created (driver=%s)", resp.Data.GetName(), resp.Data.GetDriver()))
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
		cli, ctx := newAPIClient()
		resp, _, err := cli.BlobAPI.BlobGetBucketLifecycle(ctx, name).Execute()
		if err != nil {
			return err
		}
		var lc apiclient.BlobBucketLifecycle
		if resp != nil && resp.Data != nil {
			lc = *resp.Data
		}
		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(lc)
			return nil
		}
		rules := lc.GetRules()
		if len(rules) == 0 {
			fmt.Println("(no lifecycle policy)")
			return nil
		}
		rows := make([][]string, 0, len(rules))
		for i := range rules {
			r := &rules[i]
			rows = append(rows, []string{r.GetName(), r.GetMatchPrefix(), strconv.Itoa(int(r.GetExpireAfterDays())), r.GetAction()})
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
		ruleCount := len(lc.GetRules())
		if flagDryRun {
			if output.OutputFormat(flagOutput) == output.FormatJSON {
				output.PrintJSON(map[string]any{
					"dry_run":    true,
					"would_put":  "/api/v2/blob/buckets/" + name + "/lifecycle",
					"rule_count": ruleCount,
					"policy":     lc,
				})
			} else {
				fmt.Fprintf(os.Stderr, "Dry run — would PUT /api/v2/blob/buckets/%s/lifecycle (%d rule(s))\n", name, ruleCount)
			}
			return nil
		}
		cli, ctx := newAPIClient()
		if _, _, err := cli.BlobAPI.BlobPutBucketLifecycle(ctx, name).BlobBucketLifecycle(*lc).Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("lifecycle updated for %q (%d rule(s))", name, ruleCount))
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
		empty := apiclient.BlobBucketLifecycle{Rules: []apiclient.BlobBucketLifecycleRule{}}
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
		cli, ctx := newAPIClient()
		if _, _, err := cli.BlobAPI.BlobPutBucketLifecycle(ctx, name).BlobBucketLifecycle(empty).Execute(); err != nil {
			return err
		}
		output.PrintInfo(fmt.Sprintf("lifecycle cleared for %q", name))
		return nil
	},
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
