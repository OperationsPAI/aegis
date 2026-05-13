package share

import (
	"context"
	"crypto/rand"
	"fmt"
)

const (
	shortCodeAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	shortCodeLength   = 8
	shortCodeAttempts = 5
)

// CodeLookup is the dependency the generator needs from the repository.
// It returns nil, ErrShareNotFound when the code is free.
type CodeLookup interface {
	FindByCode(ctx context.Context, code string) (*ShareLink, error)
}

func randomShortCode() (string, error) {
	buf := make([]byte, shortCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, shortCodeLength)
	for i, b := range buf {
		out[i] = shortCodeAlphabet[int(b)%len(shortCodeAlphabet)]
	}
	return string(out), nil
}

// AllocateShortCode draws a fresh code and verifies it's not already in
// the lookup. Retries up to shortCodeAttempts times before giving up.
func AllocateShortCode(ctx context.Context, lookup CodeLookup) (string, error) {
	for i := 0; i < shortCodeAttempts; i++ {
		code, err := randomShortCode()
		if err != nil {
			return "", fmt.Errorf("short_code: rand: %w", err)
		}
		existing, err := lookup.FindByCode(ctx, code)
		if err == ErrShareNotFound {
			return code, nil
		}
		if err != nil {
			return "", fmt.Errorf("short_code: lookup: %w", err)
		}
		if existing == nil {
			return code, nil
		}
	}
	return "", ErrShortCodeFailure
}
