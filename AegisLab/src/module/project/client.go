package project

import (
	"context"

	"aegis/dto"
)

// Reader is the forward-compatible project read surface for downstream
// modules that need project-owned data without reaching into the
// repository implementation directly.
type Reader interface {
	ListProjectStatistics(context.Context, []int) (map[int]*dto.ProjectStatistics, error)
}

func AsReader(source projectStatisticsSource) Reader {
	return source
}
