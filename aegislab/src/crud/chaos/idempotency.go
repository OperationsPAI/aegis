package chaos

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
)

// ChaosMeshCRNameMaxLen is the DNS-1123 label limit Chaos-Mesh CRs are bound by.
const ChaosMeshCRNameMaxLen = 63

// DeriveChaosMeshCRName produces a deterministic CR metadata.name from a
// caller idempotency_key, per ADR-0004. The action prefix is preserved so
// `kubectl get` lists remain readable (`aegis-{action}-{12hex}`).
//
// Identical (action, key) always produces identical name; Apply against an
// existing CR with the same name is the recoverability primitive — the
// executor treats AlreadyExists as success.
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
		// action prefix is bounded by sanitizeAction; this guards against
		// future action vocabulary additions creeping past the label limit.
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
