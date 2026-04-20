package consumer

import (
	"testing"

	"aegis/consts"
)

func TestBuiltinTaskExecutorsIncludeInjectToCollectChain(t *testing.T) {
	reg := BuiltinTaskExecutors()

	for _, taskType := range []consts.TaskType{
		consts.TaskTypeFaultInjection,
		consts.TaskTypeBuildDatapack,
		consts.TaskTypeRunAlgorithm,
		consts.TaskTypeCollectResult,
	} {
		if _, ok := reg.Executors[taskType]; !ok {
			t.Fatalf("builtin task executors missing %s", consts.GetTaskTypeName(taskType))
		}
	}
}
