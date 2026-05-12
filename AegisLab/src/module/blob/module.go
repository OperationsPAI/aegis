package blob

import (
	"fmt"
	"os"

	"aegis/config"

	"go.uber.org/fx"
)

// Module wires the six-role blob skeleton:
//
//	Ingestion (Handler) → Authorization (Authorizer) → Routing (Registry)
//	  → Driver (localfs, s3-stub) → Lifecycle (DeletionWorker stub)
//	  → Observability (counters live in existing infra; TODO wire metrics)
//
// Two consumers depend on this:
//   - the monolith — Service is injected by producers in-process via
//     module/blobclient's LocalClient.
//   - the standalone microservice (app/blob) — same wiring, HTTP
//     handler is the only ingestion path.
var Module = fx.Module("blob",
	fx.Provide(NewClock),
	fx.Provide(NewAuthorizer),
	fx.Provide(NewRepository),
	fx.Provide(provideRegistryDeps),
	fx.Provide(provideRegistry),
	fx.Provide(NewService),
	fx.Provide(NewHandler),
	fx.Provide(NewDeletionWorker),

	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(RoutesSDK, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
)

// provideRegistryDeps reads the HMAC signing key. Order of precedence:
// `[blob] signing_key_env` → env var named there, then literal
// `[blob] signing_key`. Fails fast if no key is configured AND any
// localfs bucket is declared (checked inside provideRegistry).
func provideRegistryDeps() RegistryDeps {
	key := config.GetString("blob.signing_key")
	if envName := config.GetString("blob.signing_key_env"); envName != "" {
		if v := os.Getenv(envName); v != "" {
			key = v
		}
	}
	return RegistryDeps{SigningKey: []byte(key)}
}

func provideRegistry(deps RegistryDeps) (*Registry, error) {
	reg, err := NewRegistryFromConfig(deps)
	if err != nil {
		return nil, fmt.Errorf("blob: build registry: %w", err)
	}
	return reg, nil
}
