package authz

import "go.uber.org/fx"

var Module = fx.Module("authz",
	fx.Provide(
		fx.Annotate(NewGormProjectMembershipResolver, fx.As(new(ProjectMembershipResolver))),
	),
)
