package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"

	"aegis/platform/crypto"
	"aegis/platform/model"
)

type CredentialType int

const (
	CredBearer        CredentialType = iota
	CredTrustedHeader
	CredInternalToken
)

type Credential struct {
	Type          CredentialType
	BearerToken   string
	Headers       TrustedHeaderSet
	HMACKey       []byte
	InternalToken string
}

// Verifier is the narrow interface the Authenticator needs from SSO.
// It matches the existing middleware.TokenVerifier interface.
type Verifier interface {
	VerifyToken(ctx context.Context, token string) (*crypto.Claims, error)
	VerifyServiceToken(ctx context.Context, token string) (*crypto.ServiceClaims, error)
}

// ServiceAccountStore checks whether a service account is revoked.
type ServiceAccountStore interface {
	FindByName(ctx context.Context, name string) (*model.ServiceAccount, error)
}

type Authenticator struct {
	verifier Verifier
	saStore  ServiceAccountStore
	resolve  crypto.PublicKeyResolver
	revStore RevocationStore
}

func NewAuthenticator(v Verifier, saStore ServiceAccountStore, resolve crypto.PublicKeyResolver, revStore RevocationStore) *Authenticator {
	return &Authenticator{verifier: v, saStore: saStore, resolve: resolve, revStore: revStore}
}

func (a *Authenticator) Verify(ctx context.Context, cred Credential) (Principal, error) {
	switch cred.Type {
	case CredBearer:
		return a.verifyBearer(ctx, cred.BearerToken)
	case CredTrustedHeader:
		return a.verifyTrustedHeader(cred)
	case CredInternalToken:
		return a.verifyInternalToken(cred.InternalToken)
	default:
		return Principal{}, ErrUnauthenticated
	}
}

func (a *Authenticator) verifyBearer(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrUnauthenticated
	}

	p, err := a.resolveBearer(ctx, token)
	if err != nil {
		return Principal{}, err
	}

	if a.revStore != nil && p.JTI != "" {
		if revoked, revErr := a.revStore.IsRevoked(ctx, p.JTI); revErr != nil {
			log.Printf("auth: revocation check failed for jti %q: %v (fail-open)", p.JTI, revErr)
		} else if revoked {
			return Principal{}, fmt.Errorf("%w: token revoked", ErrUnauthenticated)
		}
	}

	return p, nil
}

func (a *Authenticator) resolveBearer(ctx context.Context, token string) (Principal, error) {
	// 1. Try unified token (new "aegis" issuer)
	if a.resolve != nil {
		if uc, err := crypto.ParseUnifiedToken(token, a.resolve); err == nil {
			return PrincipalFromUnifiedClaims(uc), nil
		}
	}

	// 2. Try legacy user token
	claims, err := a.verifier.VerifyToken(ctx, token)
	if err == nil {
		return PrincipalFromClaims(claims), nil
	}

	// 3. Try legacy service/task token
	serviceClaims, err := a.verifier.VerifyServiceToken(ctx, token)
	if err == nil {
		return PrincipalFromServiceClaims(serviceClaims), nil
	}

	// 4. Try legacy service-account token (if store + resolver configured)
	if a.saStore != nil && a.resolve != nil {
		saClaims, saErr := crypto.ParseServiceAccountToken(token, a.resolve)
		if saErr == nil {
			name := saClaims.Subject
			sa, findErr := a.saStore.FindByName(ctx, name)
			if findErr != nil {
				return Principal{}, fmt.Errorf("%w: unknown service account", ErrUnauthenticated)
			}
			if sa.RevokedAt != nil {
				return Principal{}, fmt.Errorf("%w: service account revoked", ErrUnauthenticated)
			}
			return PrincipalFromServiceAccountClaims(saClaims, name), nil
		}
		if !errors.Is(saErr, crypto.ErrInvalidToken) {
			return Principal{}, fmt.Errorf("%w: %v", ErrUnauthenticated, saErr)
		}
	}

	return Principal{}, ErrUnauthenticated
}

func (a *Authenticator) verifyTrustedHeader(cred Credential) (Principal, error) {
	h := cred.Headers
	if h.UserID == "" && h.Signature == "" {
		return Principal{}, ErrUnauthenticated
	}
	if len(cred.HMACKey) == 0 {
		return Principal{}, ErrUnauthenticated
	}

	canonical := strings.Join([]string{
		h.UserID, h.UserEmail, h.Roles, h.TokenAud, h.TokenJti,
		h.Username, h.IsActive, h.IsAdmin, h.AuthType,
		h.APIKeyID, h.APIKeyScopes, h.TaskID,
	}, "|")
	mac := hmac.New(sha256.New, cred.HMACKey)
	_, _ = mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(h.Signature), []byte(expected)) {
		return Principal{}, ErrForgedSignature
	}

	return PrincipalFromTrustedHeaders(h), nil
}

func (a *Authenticator) verifyInternalToken(token string) (Principal, error) {
	if token == "" || a.resolve == nil {
		return Principal{}, ErrUnauthenticated
	}
	ic, err := ParseInternalToken(token, a.resolve)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	return PrincipalFromInternalClaims(ic), nil
}
