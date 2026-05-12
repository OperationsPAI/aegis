package jwtkeys

import (
	"context"
	"crypto/rsa"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"aegis/platform/config"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

type Signer struct {
	PrivateKey *rsa.PrivateKey
	Kid        string
}

func (s *Signer) PublicKey() *rsa.PublicKey {
	return &s.PrivateKey.PublicKey
}

type Verifier struct {
	keys atomic.Pointer[map[string]*rsa.PublicKey]
	mu   sync.Mutex
	url  string
}

func NewVerifierWithKeys(keys map[string]*rsa.PublicKey) *Verifier {
	v := &Verifier{}
	v.keys.Store(&keys)
	return v
}

func (v *Verifier) Resolve(kid string) (*rsa.PublicKey, error) {
	if v == nil {
		return nil, fmt.Errorf("verifier not configured")
	}
	keys := v.keys.Load()
	if keys == nil {
		return nil, fmt.Errorf("verifier has no keys loaded")
	}
	// Empty kid: if exactly one key is registered, accept it. Tokens minted by
	// an SSO that doesn't yet stamp kid headers should still verify during
	// rolling rollout. Reject ambiguity (multiple kids) so a stale key can't
	// silently authenticate.
	if kid == "" {
		if len(*keys) == 1 {
			for _, k := range *keys {
				return k, nil
			}
		}
		return nil, fmt.Errorf("token missing kid and verifier has %d keys", len(*keys))
	}
	pub, ok := (*keys)[kid]
	if !ok {
		return nil, fmt.Errorf("no public key for kid %q", kid)
	}
	return pub, nil
}

func (v *Verifier) Refresh(ctx context.Context) error {
	if v.url == "" {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	keys, err := FetchJWKS(ctx, v.url)
	if err != nil {
		return err
	}
	v.keys.Store(&keys)
	return nil
}

func newSigner() (*Signer, error) {
	path := config.GetString("sso.private_key_path")
	if path == "" {
		return nil, fmt.Errorf("sso.private_key_path is not configured")
	}
	kid := config.GetString("sso.kid")
	if kid == "" {
		return nil, fmt.Errorf("sso.kid is not configured")
	}
	pk, err := LoadPrivateKey(path)
	if err != nil {
		return nil, err
	}
	return &Signer{PrivateKey: pk, Kid: kid}, nil
}

func newVerifierFromSigner(s *Signer) *Verifier {
	return NewVerifierWithKeys(map[string]*rsa.PublicKey{s.Kid: s.PublicKey()})
}

func newRemoteVerifier(lc fx.Lifecycle) (*Verifier, error) {
	url := config.GetString("sso.jwks_url")
	if url == "" {
		return nil, fmt.Errorf("sso.jwks_url is not configured")
	}
	v := &Verifier{url: url}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := v.Refresh(fetchCtx); err != nil {
				logrus.WithError(err).Warn("jwks initial fetch failed; refresh loop will retry")
			}
			return nil
		},
	})
	startRefreshLoop(lc, v)
	return v, nil
}

func startRefreshLoop(lc fx.Lifecycle, v *Verifier) {
	stop := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			go func() {
				t := time.NewTicker(10 * time.Minute)
				defer t.Stop()
				for {
					select {
					case <-stop:
						return
					case <-t.C:
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						if err := v.Refresh(ctx); err != nil {
							logrus.WithError(err).Warn("jwks refresh failed")
						}
						cancel()
					}
				}
			}()
			return nil
		},
		OnStop: func(_ context.Context) error {
			close(stop)
			return nil
		},
	})
}

// SignerModule provides both a Signer (private key) and a Verifier that trusts
// its own public key. Used by the SSO process that signs and also accepts the
// tokens it just minted.
var SignerModule = fx.Module("jwtkeys.signer",
	fx.Provide(newSigner),
	fx.Provide(newVerifierFromSigner),
)

// VerifierModule provides a Verifier backed by a remote JWKS endpoint. Used by
// consumer processes (AegisLab backend) that only verify tokens.
var VerifierModule = fx.Module("jwtkeys.verifier",
	fx.Provide(newRemoteVerifier),
)
