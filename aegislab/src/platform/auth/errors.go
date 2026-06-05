package auth

import "errors"

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForgedSignature = errors.New("forged trusted-header signature")
)
