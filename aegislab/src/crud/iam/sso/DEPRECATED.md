# Files to delete after Casdoor migration is validated

The admin/RBAC surface (/v1/*) has been absorbed into aegis-api via
AdminModule. The files below belong to the OIDC provider and SSO boot,
which Casdoor replaces. Delete them once Casdoor is fully validated
in production.

## OIDC Provider (replaced by Casdoor)
- oidc.go
- oidc_handler.go
- oidc_grants.go
- oidc_pkce.go
- oidc_state.go
- oidc_jwks.go

## Federation (handled by Casdoor natively)
- federation_handler.go
- federation_state.go
- federation_repository.go
- federation_models.go
- federation_admin_handler.go

## OIDC Client CRUD (no longer needed)
- handler.go (OIDC client CRUD)
- routes.go (OIDC routes)
- service.go (OIDCClient CRUD)
- repository.go (OIDCClient repo)

## GitHub OAuth Proxy (absorbed by Casdoor federation)
- github_proxy_handler.go

## SSO Boot
- boot/sso/ (entire directory)

## SSO Helm Chart
- helm/charts/sso/ (entire directory)

## SSO Client (callers switch to in-process AdminService)
- clients/sso/ (entire directory)

## Model
- model/entity_sso.go (OIDCClient model)

## Full SSO Module (replaced by AdminModule)
- module.go (the full SSO fx module — AdminModule is the replacement)
