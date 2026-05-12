package sso

import (
	"strings"

	"aegis/app"
	httpapi "aegis/boot/wiring/http"
	"aegis/crud/iam/auth"
	"aegis/crud/iam/rbac"
	ssomod "aegis/crud/iam/sso"
	"aegis/crud/iam/user"
	"aegis/platform/router"

	"go.uber.org/fx"
)

// Options builds the sso runtime. Identity, auth, and RBAC only.
// No chaos/k8s/business modules — this process exists solely to issue
// tokens and answer permission checks (the latter lands in Task #6).
func Options(confPath, port string) fx.Option {
	return fx.Options(
		app.BaseOptions(confPath),
		app.WithSigner(),
		app.ObserveOptions(),
		app.DataOptions(),
		user.Module,
		auth.Module,
		rbac.Module,
		ssomod.Module,
		// SSO process verifies tokens itself; producer uses ssoclient. Both
		// satisfy middleware.TokenVerifier; in this binary auth owns it.
		fx.Provide(auth.NewTokenVerifier),
		fx.Provide(ssoLocalPermissionChecker),
		fx.Supply(&router.Handlers{}),
		fx.Supply(httpapi.ServerConfig{Addr: normalizeAddr(port)}),
		httpapi.Module,
		fx.Decorate(decorateEngineWithHealthz),
		fx.Invoke(registerSSOInitialization),
	)
}

func normalizeAddr(port string) string {
	if port == "" {
		return ":8083"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}
