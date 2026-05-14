package injection

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceSearchNilRequest(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil, nil, nil, nil)

	_, err := service.Search(t.Context(), nil, nil)

	require.Error(t, err)
	require.ErrorContains(t, err, "search injection request is nil")
}

func TestServiceListNoIssuesEmptyLabelsSucceeds(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil, nil, nil, nil)

	resp, err := service.ListNoIssues(t.Context(), &ListInjectionNoIssuesReq{}, nil)

	require.NoError(t, err)
	require.Nil(t, resp)
}
