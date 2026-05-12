package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"aegis/cli/client"
	"aegis/cli/config"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
}

// --- auth login ---

var authLoginServer string
var authLoginKeyID string
var authLoginKeySecret string
var authLoginUsername string
var authLoginPasswordFile string
var authLoginPasswordStdin bool
var authLoginContext string

var (
	apiKeyLoginFunc   = client.LoginWithAPIKey
	passwordLoginFunc = client.LoginWithPassword
)

type authLoginJSONResult struct {
	Context   string `json:"context"`
	Server    string `json:"server"`
	AuthType  string `json:"auth_type"`
	KeyID     string `json:"key_id,omitempty"`
	Username  string `json:"username,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Exchange API credentials for a bearer token",
	Args:  requireNoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		server := authLoginServer
		if server == "" {
			server = flagServer
		}
		if server == "" {
			return usageErrorf("--server is required for login")
		}

		ctxName := resolveAuthLoginContextName()
		var savedCtx config.Context
		if cfg != nil {
			savedCtx = cfg.Contexts[ctxName]
		}

		mode, username, keyID, keySecret, password, err := resolveAuthLoginInputs(cmd, savedCtx)
		if err != nil {
			return err
		}

		var result *client.LoginResult
		switch mode {
		case "password":
			output.PrintInfo(fmt.Sprintf("Logging in to %s as %s...", server, username))
			result, err = passwordLoginFunc(server, username, password)
			if err != nil {
				return err
			}
		case "api_key":
			output.PrintInfo(fmt.Sprintf("Exchanging API key token with %s using %s...", server, keyID))
			result, err = apiKeyLoginFunc(server, keyID, keySecret)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported login mode %q", mode)
		}

		if err := saveLoginContext(ctxName, server, mode, username, password, result); err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(authLoginJSONResult{
				Context:   ctxName,
				Server:    server,
				AuthType:  result.AuthType,
				KeyID:     result.KeyID,
				Username:  result.Username,
				ExpiresAt: result.ExpiresAt.Format(time.RFC3339),
			})
		} else {
			switch mode {
			case "password":
				output.PrintInfo(fmt.Sprintf("Token issued for user %s (context: %s)", result.Username, ctxName))
			case "api_key":
				output.PrintInfo(fmt.Sprintf("Token issued for key id %s (context: %s)", result.KeyID, ctxName))
			}
			output.PrintInfo(fmt.Sprintf("Token expires at %s", result.ExpiresAt.Format(time.RFC3339)))
		}
		return nil
	},
}

func resolveAuthLoginInputs(cmd *cobra.Command, savedCtx config.Context) (mode, username, keyID, keySecret, password string, err error) {
	username = strings.TrimSpace(authLoginUsername)
	if username == "" {
		username = strings.TrimSpace(os.Getenv("AEGIS_USERNAME"))
	}

	keyID = strings.TrimSpace(authLoginKeyID)
	if keyID == "" {
		keyID = strings.TrimSpace(os.Getenv("AEGIS_KEY_ID"))
	}

	// Fall back to saved context only if no credentials were supplied via
	// flags or env. We pick whichever auth-type the saved context already
	// uses; we never combine username and key-id from different sources.
	if username == "" && keyID == "" {
		switch {
		case strings.TrimSpace(savedCtx.Username) != "" && strings.TrimSpace(savedCtx.Password) != "":
			return "password", savedCtx.Username, "", "", savedCtx.Password, nil
		case strings.TrimSpace(savedCtx.KeyID) != "":
			// Saved context has a key-id but no secret — operator must
			// re-supply the secret via flag or env.
			keyID = savedCtx.KeyID
		}
	}

	if username != "" && keyID != "" {
		return "", "", "", "", "", usageErrorf("choose either username/password login or api-key login, not both")
	}

	if username != "" {
		password, err = resolvePasswordInput(cmd, savedCtx)
		if err != nil {
			return "", "", "", "", "", err
		}
		return "password", username, "", "", password, nil
	}

	if keyID == "" {
		return "", "", "", "", "", usageErrorf("either --username or --key-id is required (or store credentials in the saved context)")
	}

	keySecret = authLoginKeySecret
	if keySecret == "" {
		keySecret = os.Getenv("AEGIS_KEY_SECRET")
	}
	if keySecret == "" {
		return "", "", "", "", "", usageErrorf("--key-secret is required")
	}

	return "api_key", "", keyID, keySecret, "", nil
}

func resolvePasswordInput(cmd *cobra.Command, savedCtx config.Context) (string, error) {
	filePath := strings.TrimSpace(authLoginPasswordFile)
	if filePath == "" {
		filePath = strings.TrimSpace(os.Getenv("AEGIS_PASSWORD_FILE"))
	}
	envPassword := os.Getenv("AEGIS_PASSWORD")

	sources := 0
	if authLoginPasswordStdin {
		sources++
	}
	if filePath != "" {
		sources++
	}
	if envPassword != "" {
		sources++
	}
	if sources > 1 {
		return "", usageErrorf("choose only one password source: --password-stdin, --password-file, AEGIS_PASSWORD, or AEGIS_PASSWORD_FILE")
	}

	switch {
	case authLoginPasswordStdin:
		return readPassword(cmd.InOrStdin())
	case filePath != "":
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		return sanitizePassword(string(data))
	case envPassword != "":
		return sanitizePassword(envPassword)
	}

	// Last resort: a password previously persisted into the saved context
	// (matches the AEGIS_KEY_SECRET-via-env pattern but for passwords).
	if stored := savedCtx.Password; stored != "" {
		return stored, nil
	}

	return "", usageErrorf("password is required via --password-stdin, --password-file, AEGIS_PASSWORD, AEGIS_PASSWORD_FILE, or a saved context")
}

func readPassword(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	return sanitizePassword(string(data))
}

func sanitizePassword(raw string) (string, error) {
	password := strings.TrimRight(raw, "\r\n")
	if password == "" {
		return "", usageErrorf("password cannot be empty")
	}
	return password, nil
}

func resolveAuthLoginContextName() string {
	ctxName := strings.TrimSpace(authLoginContext)
	if ctxName == "" {
		ctxName = "default"
	}
	return ctxName
}

func saveLoginContext(ctxName, server, mode, username, password string, result *client.LoginResult) error {
	if cfg.Contexts == nil {
		cfg.Contexts = make(map[string]config.Context)
	}
	ctx := cfg.Contexts[ctxName]
	ctx.Server = server
	ctx.Token = result.Token
	ctx.AuthType = result.AuthType
	ctx.TokenExpiry = result.ExpiresAt

	// Persist credentials of the auth-type we just used. Do NOT clear the
	// stored creds for the other auth-type — operators sometimes keep both
	// in the same context and switch between them.
	switch mode {
	case "password":
		if username != "" {
			ctx.Username = username
		}
		if password != "" {
			ctx.Password = password
		}
	case "api_key":
		if result.KeyID != "" {
			ctx.KeyID = result.KeyID
		}
	}

	cfg.Contexts[ctxName] = ctx
	cfg.CurrentContext = ctxName

	if err := config.SaveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// --- auth status ---

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, ctxName, err := config.GetCurrentContext(cfg)
		if err != nil {
			return err
		}

		if ctx.Token == "" {
			return authErrorf("no token set in context %q (run 'aegisctl auth login' to refresh your token)", ctxName)
		}

		expired := client.IsTokenExpired(ctx.TokenExpiry)

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			status := "valid"
			if expired {
				status = "expired"
			}
			output.PrintJSON(map[string]any{
				"context":    ctxName,
				"server":     ctx.Server,
				"status":     status,
				"auth_type":  ctx.AuthType,
				"key_id":     ctx.KeyID,
				"expires_at": ctx.TokenExpiry.Format(time.RFC3339),
			})
			return nil
		}

		output.PrintTable(
			[]string{"Context", "Server", "Status", "Expires"},
			[][]string{{
				ctxName,
				ctx.Server,
				func() string {
					if expired {
						return "expired"
					}
					return "valid"
				}(),
				ctx.TokenExpiry.Format(time.RFC3339),
			}},
		)

		// Also try to fetch profile to verify token is actually valid.
		profile, err := client.GetProfile(ctx.Server, ctx.Token)
		if err != nil {
			hint := ""
			if expired {
				hint = " (run 'aegisctl auth login' to refresh your token)"
			}
			output.PrintInfo(fmt.Sprintf("Warning: could not verify token with server: %v%s", err, hint))
		} else {
			output.PrintInfo(fmt.Sprintf("Authenticated as: %s (id: %d)", profile.Username, profile.ID))
		}
		if ctx.KeyID != "" {
			output.PrintInfo(fmt.Sprintf("Issued via key id: %s", ctx.KeyID))
		}

		return nil
	},
}

// --- auth inspect ---

var authInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect locally stored authentication context",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, ctxName, err := config.GetCurrentContext(cfg)
		if err != nil {
			return err
		}

		tokenPreview := ""
		if ctx.Token != "" {
			tokenPreview = ctx.Token
			if len(tokenPreview) > 20 {
				tokenPreview = tokenPreview[:10] + "..." + tokenPreview[len(tokenPreview)-10:]
			}
		}
		expiresAt := ""
		if !ctx.TokenExpiry.IsZero() {
			expiresAt = ctx.TokenExpiry.Format(time.RFC3339)
		}
		expired := false
		if !ctx.TokenExpiry.IsZero() {
			expired = client.IsTokenExpired(ctx.TokenExpiry)
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(map[string]any{
				"context":       ctxName,
				"server":        ctx.Server,
				"auth_type":     ctx.AuthType,
				"key_id":        ctx.KeyID,
				"token_present": ctx.Token != "",
				"token_preview": tokenPreview,
				"token_expired": expired,
				"expires_at":    expiresAt,
			})
			return nil
		}

		output.PrintTable(
			[]string{"Context", "Server", "AuthType", "KeyID", "Token", "Expired", "Expires"},
			[][]string{{
				ctxName,
				ctx.Server,
				emptyOrValue(ctx.AuthType, "-"),
				emptyOrValue(ctx.KeyID, "-"),
				emptyOrValue(tokenPreview, "-"),
				fmt.Sprintf("%t", expired),
				emptyOrValue(expiresAt, "-"),
			}},
		)
		return nil
	},
}

// --- auth sign-debug ---

var authSignDebugKeyID string
var authSignDebugKeySecret string
var authSignDebugTimestamp int64
var authSignDebugNonce string
var authSignDebugExecute bool
var authSignDebugSaveContext bool

var authSignDebugCmd = &cobra.Command{
	Use:   "sign-debug",
	Short: "Print canonical string and signature headers for Key ID / Key Secret token exchange",
	RunE: func(cmd *cobra.Command, args []string) error {
		keyID := authSignDebugKeyID
		if keyID == "" {
			keyID = os.Getenv("AEGIS_KEY_ID")
		}
		if keyID == "" {
			return usageErrorf("--key-id is required")
		}

		keySecret := authSignDebugKeySecret
		if keySecret == "" {
			keySecret = os.Getenv("AEGIS_KEY_SECRET")
		}
		if keySecret == "" {
			return usageErrorf("--key-secret is required")
		}

		signTime := time.Now().UTC()
		if authSignDebugTimestamp > 0 {
			signTime = time.Unix(authSignDebugTimestamp, 0).UTC()
		}

		debugInfo, err := client.PrepareAPIKeyTokenDebug(keyID, keySecret, signTime, authSignDebugNonce)
		if err != nil {
			return err
		}

		server := strings.TrimRight(resolveServerForAuthDebug(), "/")
		if authSignDebugExecute && (server == "" || strings.Contains(server, "HOST:8082")) {
			return fmt.Errorf("--execute requires a real --server or configured AEGIS_SERVER/current context")
		}
		curlCommand := buildAPIKeyCurl(server, debugInfo)
		var executeResp map[string]any
		if authSignDebugExecute {
			executeResp, err = executeAPIKeyTokenExchange(server, debugInfo)
			if err != nil {
				return err
			}
			if authSignDebugSaveContext {
				if err := saveAPIKeyContext(server, executeResp); err != nil {
					return err
				}
			}
		} else if authSignDebugSaveContext {
			return fmt.Errorf("--save-context requires --execute")
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			result := map[string]any{
				"server":           server,
				"method":           debugInfo.Method,
				"path":             debugInfo.Path,
				"key_id":           debugInfo.KeyID,
				"timestamp":        debugInfo.Timestamp,
				"nonce":            debugInfo.Nonce,
				"body_sha256":      debugInfo.BodySHA256,
				"canonical_string": debugInfo.CanonicalString,
				"signature":        debugInfo.Signature,
				"headers":          debugInfo.Headers(),
				"curl":             curlCommand,
				"executed":         authSignDebugExecute,
				"saved_context":    authSignDebugSaveContext,
			}
			if authSignDebugExecute {
				result["response"] = executeResp
			}
			output.PrintJSON(result)
			return nil
		}

		fmt.Printf("Server: %s\n", server)
		fmt.Printf("Method: %s\n", debugInfo.Method)
		fmt.Printf("Path: %s\n", debugInfo.Path)
		fmt.Printf("Key-Id: %s\n", debugInfo.KeyID)
		fmt.Printf("Timestamp: %s\n", debugInfo.Timestamp)
		fmt.Printf("Nonce: %s\n", debugInfo.Nonce)
		fmt.Printf("Body-SHA256: %s\n", debugInfo.BodySHA256)
		fmt.Printf("Signature: %s\n\n", debugInfo.Signature)
		fmt.Println("Canonical String:")
		fmt.Println(debugInfo.CanonicalString)
		fmt.Println()
		fmt.Println("curl:")
		fmt.Println(curlCommand)
		if authSignDebugExecute {
			fmt.Println()
			fmt.Println("response:")
			output.PrintJSON(executeResp)
		}
		return nil
	},
}

// --- auth token ---

var authTokenSet string

var authTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage authentication token",
	RunE: func(cmd *cobra.Command, args []string) error {
		if authTokenSet == "" {
			// Display current token info.
			ctx, ctxName, err := config.GetCurrentContext(cfg)
			if err != nil {
				return err
			}
			if ctx.Token == "" {
				return authErrorf("no token set in context %q (run 'aegisctl auth login' to refresh your token)", ctxName)
			}
			// Show truncated token.
			token := ctx.Token
			display := token
			if len(token) > 20 {
				display = token[:10] + "..." + token[len(token)-10:]
			}
			fmt.Println(display)
			return nil
		}

		// Set token directly.
		ctxName := cfg.CurrentContext
		if ctxName == "" {
			ctxName = "default"
		}

		ctx := cfg.Contexts[ctxName]
		ctx.Token = authTokenSet
		ctx.AuthType = "token"
		ctx.KeyID = ""
		ctx.TokenExpiry = time.Time{}
		cfg.Contexts[ctxName] = ctx
		cfg.CurrentContext = ctxName

		if err := config.SaveConfig(cfg); err != nil {
			return missingEnvErrorf("save config: %v", err)
		}

		output.PrintInfo(fmt.Sprintf("Token set for context %q", ctxName))
		return nil
	},
}

func init() {
	authLoginCmd.Flags().StringVar(&authLoginServer, "server", "", "Server URL")
	authLoginCmd.Flags().StringVar(&authLoginKeyID, "key-id", "", "Key ID (env: AEGIS_KEY_ID)")
	authLoginCmd.Flags().StringVar(&authLoginKeySecret, "key-secret", "", "Key secret (env: AEGIS_KEY_SECRET)")
	authLoginCmd.Flags().StringVar(&authLoginUsername, "username", "", "Username (env: AEGIS_USERNAME)")
	authLoginCmd.Flags().BoolVar(&authLoginPasswordStdin, "password-stdin", false, "Read password from stdin")
	authLoginCmd.Flags().StringVar(&authLoginPasswordFile, "password-file", "", "Read password from file (env: AEGIS_PASSWORD_FILE)")
	authLoginCmd.Flags().StringVar(&authLoginContext, "context", "", "Context name to save credentials under (default: \"default\")")
	authSignDebugCmd.Flags().StringVar(&authSignDebugKeyID, "key-id", "", "Key ID (env: AEGIS_KEY_ID)")
	authSignDebugCmd.Flags().StringVar(&authSignDebugKeySecret, "key-secret", "", "Key secret (env: AEGIS_KEY_SECRET)")
	authSignDebugCmd.Flags().Int64Var(&authSignDebugTimestamp, "timestamp", 0, "Override unix timestamp in seconds")
	authSignDebugCmd.Flags().StringVar(&authSignDebugNonce, "nonce", "", "Override nonce for reproducible signature output")
	authSignDebugCmd.Flags().BoolVar(&authSignDebugExecute, "execute", false, "Execute the signed token exchange request and print the response")
	authSignDebugCmd.Flags().BoolVar(&authSignDebugSaveContext, "save-context", false, "Save the exchanged bearer token into the current context after --execute succeeds")

	authTokenCmd.Flags().StringVar(&authTokenSet, "set", "", "Set token directly")

	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authInspectCmd)
	authCmd.AddCommand(authSignDebugCmd)
	authCmd.AddCommand(authTokenCmd)
}

func resolveServerForAuthDebug() string {
	if flagServer != "" {
		return flagServer
	}
	if value := os.Getenv("AEGIS_SERVER"); value != "" {
		return value
	}
	if cfg != nil {
		if ctx, _, err := config.GetCurrentContext(cfg); err == nil && ctx.Server != "" {
			return ctx.Server
		}
	}
	return "http://HOST:8082"
}

func buildAPIKeyCurl(server string, debugInfo *client.APIKeyTokenDebug) string {
	return fmt.Sprintf(
		"curl -X POST %s%s -H 'Accept: application/json' -H 'X-Key-Id: %s' -H 'X-Timestamp: %s' -H 'X-Nonce: %s' -H 'X-Signature: %s'",
		server,
		debugInfo.Path,
		debugInfo.KeyID,
		debugInfo.Timestamp,
		debugInfo.Nonce,
		debugInfo.Signature,
	)
}

func executeAPIKeyTokenExchange(server string, debugInfo *client.APIKeyTokenDebug) (map[string]any, error) {
	httpClient := client.NewClient(server, "", 30*time.Second)
	var response map[string]any
	if err := httpClient.PostWithHeaders(debugInfo.Path, debugInfo.Headers(), &response); err != nil {
		return nil, fmt.Errorf("execute token exchange: %w", err)
	}
	return response, nil
}

func saveAPIKeyContext(server string, executeResp map[string]any) error {
	ctxName := resolveContextNameForSave()
	ctx := cfg.Contexts[ctxName]
	ctx.Server = server

	data, ok := executeResp["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("execute response does not contain a valid data payload")
	}

	token, _ := data["token"].(string)
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("execute response does not contain a token")
	}
	ctx.Token = token

	if authType, _ := data["auth_type"].(string); strings.TrimSpace(authType) != "" {
		ctx.AuthType = authType
	}
	if keyID, _ := data["key_id"].(string); strings.TrimSpace(keyID) != "" {
		ctx.KeyID = keyID
	}
	if expiresAt, _ := data["expires_at"].(string); strings.TrimSpace(expiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return fmt.Errorf("parse expires_at: %w", err)
		}
		ctx.TokenExpiry = parsed
	}

	cfg.Contexts[ctxName] = ctx
	cfg.CurrentContext = ctxName
	if err := config.SaveConfig(cfg); err != nil {
		return missingEnvErrorf("save config: %v", err)
	}

	output.PrintInfo(fmt.Sprintf("Saved token to context %q", ctxName))
	return nil
}

func resolveContextNameForSave() string {
	if cfg != nil && strings.TrimSpace(cfg.CurrentContext) != "" {
		return cfg.CurrentContext
	}
	return "default"
}

func emptyOrValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
