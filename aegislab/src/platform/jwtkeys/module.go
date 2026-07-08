package jwtkeys

import (
	"context"
	"crypto/rsa"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aegis/platform/config"
	"aegis/platform/crypto"

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
	urls []string
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
	if (keys == nil || len(*keys) == 0) && len(v.urls) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := v.Refresh(ctx); err == nil {
			keys = v.keys.Load()
		}
	}
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
	if len(v.urls) == 0 {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	merged := make(map[string]*rsa.PublicKey)
	var lastErr error
	for _, u := range v.urls {
		keys, err := FetchJWKS(ctx, u)
		if err != nil {
			logrus.WithError(err).WithField("url", u).Warn("jwks fetch from source failed")
			lastErr = err
			continue
		}
		for kid, pub := range keys {
			if _, exists := merged[kid]; !exists {
				merged[kid] = pub
			}
		}
	}
	if len(merged) == 0 {
		if lastErr != nil {
			return lastErr
		}
		return fmt.Errorf("all jwks sources returned zero usable keys")
	}
	v.keys.Store(&merged)
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
	primary := config.GetString("sso.jwks_url")
	if primary == "" {
		return nil, fmt.Errorf("sso.jwks_url is not configured")
	}
	urls := []string{primary}
	if extra := config.GetString("sso.additional_jwks_urls"); extra != "" {
		for _, u := range strings.Split(extra, ",") {
			if u = strings.TrimSpace(u); u != "" {
				urls = append(urls, u)
			}
		}
	}
	v := &Verifier{urls: urls}
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

func initAcceptedIssuers() {
	raw := strings.TrimSpace(config.GetString("sso.accepted_issuers"))
	if raw == "" {
		return
	}
	var issuers []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			issuers = append(issuers, s)
		}
	}
	if len(issuers) > 0 {
		crypto.SetAcceptedIssuers(issuers)
		logrus.WithField("issuers", issuers).Info("additional JWT issuers configured")
	}
}

// SignerModule provides both a Signer (private key) and a Verifier that trusts
// its own public key. Used by the SSO process that signs and also accepts the
// tokens it just minted.
var SignerModule = fx.Module("jwtkeys.signer",
	fx.Provide(newSigner),
	fx.Provide(newVerifierFromSigner),
	fx.Invoke(func() { initAcceptedIssuers() }),
)

// VerifierModule provides a Verifier backed by a remote JWKS endpoint. Used by
// consumer processes (aegislab backend) that only verify tokens.
var VerifierModule = fx.Module("jwtkeys.verifier",
	fx.Provide(newRemoteVerifier),
	fx.Invoke(func() { initAcceptedIssuers() }),
)
