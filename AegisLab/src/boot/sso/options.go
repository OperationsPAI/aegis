package sso

import (
	"aegis/boot"
	httpapi "aegis/boot/wiring/http"
	"aegis/crud/iam/auth"
	"aegis/crud/iam/rbac"
	ssomod "aegis/crud/iam/sso"
	"aegis/crud/iam/user"
	"aegis/platform/router"

	"github.com/gin-gonic/gin"
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
		fx.Supply(httpapi.ServerConfig{Addr: httpapi.NormalizeAddr(port, ":8083")}),
		httpapi.Module,
		fx.Decorate(func(e *gin.Engine) *gin.Engine { return httpapi.DecorateEngineWithHealthz(e) }),
		fx.Invoke(registerSSOInitialization),
	)
}
