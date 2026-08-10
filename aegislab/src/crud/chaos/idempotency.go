package chaos

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
)

// 63 = DNS-1123 label limit Chaos-Mesh CR names are bound by.
const ChaosMeshCRNameMaxLen = 63

// DeriveChaosMeshCRName makes the CR metadata.name a pure function of
// (action, idempotency_key) so a crash mid-Apply can be recovered by a
// plain Status call (ADR-0004) — there is no orphan-name to reconcile.
func DeriveChaosMeshCRName(action, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		return "", fmt.Errorf("chaos: idempotency_key is required")
	}
	a := sanitizeAction(action)
	if a == "" {
		return "", fmt.Errorf("chaos: action sanitises to empty: %q", action)
	}
	sum := sha256.Sum256([]byte(idempotencyKey))
	name := fmt.Sprintf("aegis-%s-%s", a, hex.EncodeToString(sum[:])[:12])
	if len(name) > ChaosMeshCRNameMaxLen {
		return "", fmt.Errorf("chaos: derived CR name exceeds %d chars: %q", ChaosMeshCRNameMaxLen, name)
	}
	return name, nil
}

var actionSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func sanitizeAction(s string) string {
	lower := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			lower = append(lower, c+32)
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			lower = append(lower, c)
		default:
			lower = append(lower, '-')
		}
	}
	out := actionSanitizer.ReplaceAllString(string(lower), "-")
	// Trim leading/trailing dashes — DNS-1123 forbids them.
	for len(out) > 0 && out[0] == '-' {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}
