package execution

import (
	"context"

	"aegis/platform/authz"
)

// Cascader owns destructive writes to the executions table on behalf of
// other domains (currently injection). Holding these writes here keeps the
// scope check (project_id IN scope.VisibleProjects) co-located with the
// table that authorizes them.
type Cascader interface {
	CascadeDeleteByInjectionIDs(ctx context.Context, scope authz.CallerScope, injectionIDs []int) error
}
