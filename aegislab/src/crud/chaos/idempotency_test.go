package chaos

import (
	"strings"
	"testing"
)

func TestDeriveChaosMeshCRName_Deterministic(t *testing.T) {
	a, err := DeriveChaosMeshCRName("PodKill", "01HXYZABCDEFGHJKMNPQRSTVWX")
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := DeriveChaosMeshCRName("PodKill", "01HXYZABCDEFGHJKMNPQRSTVWX")
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a != b {
		t.Fatalf("same key must produce same name: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "aegis-podkill-") {
		t.Fatalf("expected aegis-podkill- prefix; got %q", a)
	}
	if len(a) > ChaosMeshCRNameMaxLen {
		t.Fatalf("derived name exceeds DNS-1123 label length: %q (%d)", a, len(a))
	}
}

func TestDeriveChaosMeshCRName_DifferentKeyDifferentName(t *testing.T) {
	a, _ := DeriveChaosMeshCRName("pod_kill", "key-one")
	b, _ := DeriveChaosMeshCRName("pod_kill", "key-two")
	if a == b {
		t.Fatalf("different keys must produce different names; both %q", a)
	}
}

func TestDeriveChaosMeshCRName_EmptyKeyRejected(t *testing.T) {
	if _, err := DeriveChaosMeshCRName("pod_kill", ""); err == nil {
		t.Fatalf("empty idempotency_key must be rejected")
	}
}

func TestDeriveChaosMeshCRName_DNS1123Compliant(t *testing.T) {
	// Action with underscores, slashes, uppercase — all forbidden by DNS-1123.
	got, err := DeriveChaosMeshCRName("Pod_Kill/Foo", "abc")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i := 0; i < len(got); i++ {
		c := got[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
		if !ok {
			t.Fatalf("CR name %q contains DNS-1123-invalid byte %q at %d", got, c, i)
		}
	}
	if got[0] == '-' || got[len(got)-1] == '-' {
		t.Fatalf("CR name %q must not start/end with '-'", got)
	}
}
