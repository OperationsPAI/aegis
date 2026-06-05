package duckdbquery

import (
	"fmt"
	"regexp"
	"strings"

	"aegis/platform/consts"
)

// validateSQL applies a conservative allowlist: only SELECT / WITH
// queries, no semicolons (single trailing semicolon allowed), no
// extension loading, no DDL/DML, no raw file-reader functions. The user
// is expected to query the pre-registered VIEWs.
func validateSQL(raw string) (string, error) {
	stripped := stripSQLComments(raw)
	stripped = strings.TrimSpace(stripped)
	stripped = strings.TrimRight(stripped, ";")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return "", fmt.Errorf("%w: SQL is empty", consts.ErrBadRequest)
	}
	if strings.Contains(stripped, ";") {
		return "", fmt.Errorf("%w: multi-statement SQL is not allowed", consts.ErrBadRequest)
	}
	lowered := strings.ToLower(stripped)
	first := strings.Fields(lowered)[0]
	if first != "select" && first != "with" {
		return "", fmt.Errorf("%w: only SELECT / WITH queries are allowed", consts.ErrBadRequest)
	}
	for _, word := range sqlBlacklist {
		if wordRegex(word).MatchString(lowered) {
			return "", fmt.Errorf("%w: keyword %q is not allowed", consts.ErrBadRequest, word)
		}
	}
	return stripped, nil
}

var sqlBlacklist = []string{
	"attach", "copy", "pragma", "install", "load", "call",
	"insert", "update", "delete", "create", "drop", "alter", "truncate",
	"export", "import", "set", "reset", "begin", "commit", "rollback",
	"read_parquet", "read_csv", "read_json", "read_ndjson", "read_text",
	"read_blob", "glob", "system", "shell_exec",
}

func wordRegex(word string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`)
}

var (
	lineCommentRe  = regexp.MustCompile(`--[^\n]*`)
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func stripSQLComments(in string) string {
	out := lineCommentRe.ReplaceAllString(in, " ")
	out = blockCommentRe.ReplaceAllString(out, " ")
	return out
}

var viewNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func sanitizeViewName(raw string) string {
	cleaned := viewNameSanitizer.ReplaceAllString(raw, "_")
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		return ""
	}
	if cleaned[0] >= '0' && cleaned[0] <= '9' {
		cleaned = "_" + cleaned
	}
	return cleaned
}

func quoteIdent(name string) string {
	return fmt.Sprintf("\"%s\"", strings.ReplaceAll(name, "\"", "\"\""))
}

// quoteLiteral single-quotes a string literal for embedding in DuckDB
// SQL (path lists for allowed_directories, read_parquet URLs).
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
