package app

import (
	buildkit "aegis/infra/buildkit"
	config "aegis/infra/config"
	db "aegis/infra/db"
	etcd "aegis/infra/etcd"
	harbor "aegis/infra/harbor"
	helm "aegis/infra/helm"
	jwtkeys "aegis/infra/jwtkeys"
	logger "aegis/infra/logger"
	loki "aegis/infra/loki"
	redis "aegis/infra/redis"
	tracing "aegis/infra/tracing"

	"go.uber.org/fx"
)

// BaseOptions provides only config + logger. JWT key material is no
// longer baked in — each binary picks WithSigner (it owns a private
// key, like aegis-sso / the monolith) or WithRemoteVerifier (verifies
// JWKS from another process, like aegis-notify / aegis-blob /
// aegis-configcenter / aegis-gateway). Producers that go through
// `module/ssoclient` already get a remote Verifier transitively and
// must NOT also pull WithRemoteVerifier (fx would see a duplicate
// `*jwtkeys.Verifier` provider).
func BaseOptions(confPath string) fx.Option {
	return fx.Options(
		fx.Supply(config.Params{Path: confPath}),
		config.Module,
		logger.Module,
	)
}

// WithSigner registers the JWT signer + a Verifier that trusts the
// signer's own public key. Use in any binary that mints tokens.
func WithSigner() fx.Option {
	return jwtkeys.SignerModule
}

// WithRemoteVerifier registers a Verifier backed by a remote JWKS
// endpoint (sso.jwks_url). Use in verify-only binaries that DO NOT
// import `module/ssoclient` (which already brings the same module
// in transitively).
func WithRemoteVerifier() fx.Option {
	return jwtkeys.VerifierModule
}

func ObserveOptions() fx.Option {
	return fx.Options(
		loki.Module,
		tracing.Module,
	)
}

func DataOptions() fx.Option {
	return fx.Options(
		db.Module,
		redis.Module,
	)
}

func CoordinationOptions() fx.Option {
	return fx.Options(
		etcd.Module,
	)
}

func BuildInfraOptions() fx.Option {
	return fx.Options(
		harbor.Module,
		helm.Module,
		buildkit.Module,
	)
}

func CommonOptions(confPath string) fx.Option {
	return fx.Options(
		BaseOptions(confPath),
		ObserveOptions(),
		DataOptions(),
		CoordinationOptions(),
		BuildInfraOptions(),
	)
}
