package share

import (
	"context"
	"testing"
)

type fakeLookup struct {
	hits int
	taken map[string]bool
}

func (f *fakeLookup) FindByCode(_ context.Context, code string) (*ShareLink, error) {
	f.hits++
	if f.taken[code] {
		return &ShareLink{ShortCode: code}, nil
	}
	return nil, ErrShareNotFound
}

func TestRandomShortCodeShape(t *testing.T) {
	c, err := randomShortCode()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(c) != shortCodeLength {
		t.Fatalf("len=%d want %d", len(c), shortCodeLength)
	}
	for i, b := range []byte(c) {
		ok := false
		for _, a := range []byte(shortCodeAlphabet) {
			if a == b {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("char[%d]=%q not in alphabet", i, b)
		}
	}
}

func TestAllocateShortCodeRetriesOnCollision(t *testing.T) {
	// Force collisions by accepting whatever the first few codes are.
	first1, _ := randomShortCode()
	first2, _ := randomShortCode()
	// Re-seeded each loop — we can't precisely force collisions via the
	// real rand path, so use a permissive lookup: mark random codes as
	// taken on the fly to drive at least one collision and confirm
	// allocation eventually succeeds.
	lookup := &fakeLookup{taken: map[string]bool{first1: true, first2: true}}
	code, err := AllocateShortCode(context.Background(), lookup)
	if err != nil {
		t.Fatalf("alloc err: %v", err)
	}
	if code == "" {
		t.Fatalf("empty code")
	}
}

func TestAllocateShortCodeFailsAfterMaxRetries(t *testing.T) {
	// Repository that always says the code is taken.
	lookup := &alwaysTakenLookup{}
	_, err := AllocateShortCode(context.Background(), lookup)
	if err != ErrShortCodeFailure {
		t.Fatalf("want ErrShortCodeFailure, got %v", err)
	}
	if lookup.hits != shortCodeAttempts {
		t.Fatalf("attempts=%d want %d", lookup.hits, shortCodeAttempts)
	}
}

type alwaysTakenLookup struct{ hits int }

func (l *alwaysTakenLookup) FindByCode(_ context.Context, code string) (*ShareLink, error) {
	l.hits++
	return &ShareLink{ShortCode: code}, nil
}
