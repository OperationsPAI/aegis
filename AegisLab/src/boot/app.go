package app

import (
	buildkit "aegis/platform/buildkit"
	config "aegis/platform/configfx"
	db "aegis/platform/db"
	etcd "aegis/platform/etcd"
	harbor "aegis/platform/harbor"
	helm "aegis/platform/helm"
	jwtkeys "aegis/platform/jwtkeys"
	logger "aegis/platform/logger"
	loki "aegis/platform/loki"
	redis "aegis/platform/redis"
	tracing "aegis/platform/tracing"

	"go.uber.org/fx"
)

// BaseOptions provides only config + logger. JWT key material is no
// longer baked in — each binary picks WithSigner (owns the private
// key, like sso / the monolith / runtime-worker) or
// WithRemoteVerifier (verify-only, like aegis-notify / aegis-blob /
// aegis-configcenter / aegis-gateway). Pick exactly one — fx fails to
// start if `*jwtkeys.Verifier` is provided twice.
//
// `module/ssoclient` used to add a remote Verifier transitively, which
// collided with WithSigner in the monolith. It no longer does; every
// binary that loads ssoclient.Module must pair it with one of these.
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
// endpoint (sso.jwks_url). Use in verify-only binaries.
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
