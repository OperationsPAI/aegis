package evaluation

import (
	"testing"

	execution "aegis/core/domain/execution"
	"aegis/platform/authz"
	"aegis/platform/consts"
	"aegis/platform/model"

	"github.com/stretchr/testify/require"
)

// TestEvaluationScopeCheck_NullProjectAdminOnly verifies that legacy
// evaluations without project_id surface as not-found for non-admins, and
// that cross-project evaluations are also hidden behind 404.
func TestEvaluationScopeCheck_NullProjectAdminOnly(t *testing.T) {
	pid := 5
	nullEval := &model.Evaluation{ID: 1}
	pinnedEval := &model.Evaluation{ID: 2, ProjectID: &pid}

	require.NoError(t, evaluationScopeCheck(authz.SystemScope(), nullEval, 1))

	nonAdmin := authz.CallerScope{UserID: 9, IsAdmin: false, VisibleProjects: []int64{5}}
	require.ErrorIs(t, evaluationScopeCheck(nonAdmin, nullEval, 1), consts.ErrNotFound)
	require.NoError(t, evaluationScopeCheck(nonAdmin, pinnedEval, 2))

	foreign := authz.CallerScope{UserID: 9, IsAdmin: false, VisibleProjects: []int64{99}}
	require.ErrorIs(t, evaluationScopeCheck(foreign, pinnedEval, 2), consts.ErrNotFound)
}

func TestListEvaluationExecutionsRequiresQuerySource(t *testing.T) {
	service := &Service{}

	_, err := service.listEvaluationExecutionsByDatapack(t.Context(), &execution.EvaluationExecutionsByDatapackReq{})
	if err == nil {
		t.Fatalf("expected datapack query to fail without orchestrator or execution service")
	}

	_, err = service.listEvaluationExecutionsByDataset(t.Context(), &execution.EvaluationExecutionsByDatasetReq{})
	if err == nil {
		t.Fatalf("expected dataset query to fail without orchestrator or execution service")
	}
}
