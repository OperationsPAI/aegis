package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceBatchDeleteEmptyRequestSucceeds(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil)

	err := service.BatchDelete(t.Context(), &BatchDeleteExecutionReq{})

	require.NoError(t, err)
}
