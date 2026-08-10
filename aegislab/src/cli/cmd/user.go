package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "User administration (admin-only)",
	Long: `Administrative user-account operations.

These commands require the caller's token to carry the user:update:all
permission (super_admin / aegis admin role).`,
}

// --- user reset-password ---

var (
	userResetPwdInline   string
	userResetPwdFile     string
	userResetPwdStdin    bool
	userResetPwdAssumeOK bool
)

var userResetPasswordCmd = &cobra.Command{
	Use:   "reset-password <user-id|username>",
	Short: "Reset another user's password and revoke their existing sessions",
	Long: `Set a new password for the target user without requiring the old one.

The new password is supplied through exactly one of:
  --password-stdin     read the password from stdin (recommended for pipes)
  --password-file <p>  read the password from a file (newline-trimmed)
  --password <pwd>     inline flag (visible in shell history — avoid)

On success the target user's existing access and refresh tokens are revoked
server-side, so the new password is the only way back in.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()

		targetID, targetName, err := resolveUserRef(cli, ctx, args[0])
		if err != nil {
			return err
		}

		newPwd, err := resolveResetPasswordInput(cmd)
		if err != nil {
			return err
		}

		if !userResetPwdAssumeOK {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"About to reset password for user %d (%s) and revoke all of their sessions. Continue? [y/N] ",
				targetID, targetName)
			var answer string
			_, _ = fmt.Fscanln(cmd.InOrStdin(), &answer)
			if !strings.EqualFold(strings.TrimSpace(answer), "y") &&
				!strings.EqualFold(strings.TrimSpace(answer), "yes") {
				return fmt.Errorf("aborted")
			}
		}

		req := cli.UsersAPI.ResetUserPassword(ctx, int32(targetID)).
			UserResetPasswordReq(apiclient.UserResetPasswordReq{NewPassword: newPwd})
		resp, _, err := req.Execute()
		if err != nil {
			return fmt.Errorf("reset password: %w", err)
		}
		data := resp.Data
		if data == nil {
			return fmt.Errorf("reset password: empty response")
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"user_id":             data.GetUserId(),
				"username":            data.GetUsername(),
				"sessions_revoked":    data.GetSessionsRevoked(),
				"password_updated_at": data.GetPasswordUpdatedAt(),
			})
			return nil
		}
		fmt.Printf("User ID:             %d\n", data.GetUserId())
		fmt.Printf("Username:            %s\n", data.GetUsername())
		fmt.Printf("Sessions revoked:    %t\n", data.GetSessionsRevoked())
		fmt.Printf("Password updated at: %s\n", data.GetPasswordUpdatedAt())
		return nil
	},
}

// resolveUserRef accepts a positional argument that is either a numeric user
// id or a username. Numeric paths skip the lookup; username paths page through
// `users list` (the backend's username filter is wired through the swagger
// spec but not enforced by the handler, so we match client-side) until a
// case-insensitive hit is found or the page cap is exhausted.
func resolveUserRef(cli *apiclient.APIClient, ctx context.Context, raw string) (int, string, error) {
	if id, err := strconv.Atoi(raw); err == nil && id > 0 {
		return id, raw, nil
	}
	const maxPages, pageSize = 10, 50
	want := strings.ToLower(strings.TrimSpace(raw))
	for page := int32(1); page <= int32(maxPages); page++ {
		resp, _, err := cli.UsersAPI.ListUsers(ctx).
			Page(page).
			Size(pageSize).
			Username(raw).
			Execute()
		if err != nil {
			return 0, "", fmt.Errorf("list users: %w", err)
		}
		if resp.Data == nil {
			break
		}
		items := resp.Data.GetItems()
		for _, u := range items {
			if strings.EqualFold(u.GetUsername(), want) {
				return int(u.GetId()), u.GetUsername(), nil
			}
		}
		if int32(len(items)) < pageSize {
			break
		}
	}
	return 0, "", fmt.Errorf("user %q not found (searched up to %d users)", raw, maxPages*pageSize)
}

// resolveResetPasswordInput reads the new password from exactly one supported
// source. Defaults to inline `--password` rejected — callers must pick stdin
// or a file unless they explicitly accept the security trade-off.
func resolveResetPasswordInput(cmd *cobra.Command) (string, error) {
	sources := 0
	if userResetPwdStdin {
		sources++
	}
	if strings.TrimSpace(userResetPwdFile) != "" {
		sources++
	}
	if userResetPwdInline != "" {
		sources++
	}
	if sources == 0 {
		return "", usageErrorf("password is required via --password-stdin, --password-file, or --password")
	}
	if sources > 1 {
		return "", usageErrorf("choose only one password source: --password-stdin, --password-file, --password")
	}

	switch {
	case userResetPwdStdin:
		return readPasswordFrom(cmd.InOrStdin())
	case userResetPwdFile != "":
		data, err := os.ReadFile(userResetPwdFile)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		return sanitizePassword(string(data))
	default:
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: passing a password via --password leaves it in shell history")
		return sanitizePassword(userResetPwdInline)
	}
}

func readPasswordFrom(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	return sanitizePassword(string(data))
}

func init() {
	userResetPasswordCmd.Flags().StringVar(&userResetPwdInline, "password", "", "New password (visible in shell history — prefer --password-stdin)")
	userResetPasswordCmd.Flags().StringVar(&userResetPwdFile, "password-file", "", "Read new password from file (newline-trimmed)")
	userResetPasswordCmd.Flags().BoolVar(&userResetPwdStdin, "password-stdin", false, "Read new password from stdin")
	userResetPasswordCmd.Flags().BoolVarP(&userResetPwdAssumeOK, "yes", "y", false, "Skip the interactive confirmation prompt")

	userCmd.AddCommand(userResetPasswordCmd)
}
