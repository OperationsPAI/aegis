package trace

import (
	"encoding/json"
	"testing"

	"aegis/consts"
	"aegis/dto"

	"github.com/redis/go-redis/v9"
)

func TestParseStreamEvent_AlgoResultCollectionPayload(t *testing.T) {
	payloadBytes, err := json.Marshal(dto.ExecutionResult{
		Algorithm: "random",
		JobName:   "job-1",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	event, err := parseStreamEvent("1776894182633-0", map[string]any{
		consts.RdbEventTaskID:   "task-1",
		consts.RdbEventTaskType: consts.GetTaskTypeName(consts.TaskTypeCollectResult),
		consts.RdbEventName:     string(consts.EventAlgoResultCollection),
		consts.RdbEventPayload:  string(payloadBytes),
	})
	if err != nil {
		t.Fatalf("parseStreamEvent: %v", err)
	}

	result, ok := event.Payload.(*dto.ExecutionResult)
	if !ok {
		t.Fatalf("payload type = %T, want *dto.ExecutionResult", event.Payload)
	}
	if result.Algorithm != "random" {
		t.Fatalf("payload algorithm = %q, want %q", result.Algorithm, "random")
	}
}

func TestStreamProcessor_ProcessMessageForSSE_AlgoResultCollectionCompletes(t *testing.T) {
	payloadBytes, err := json.Marshal(dto.ExecutionResult{
		Algorithm: "random",
		JobName:   "job-1",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	processor := NewStreamProcessor([]dto.ContainerVersionItem{
		{ContainerName: "random"},
	})

	_, event, err := processor.ProcessMessageForSSE(redis.XMessage{
		ID: "1776894182633-0",
		Values: map[string]any{
			consts.RdbEventTaskID:   "task-1",
			consts.RdbEventTaskType: consts.GetTaskTypeName(consts.TaskTypeCollectResult),
			consts.RdbEventName:     string(consts.EventAlgoResultCollection),
			consts.RdbEventPayload:  string(payloadBytes),
		},
	})
	if err != nil {
		t.Fatalf("ProcessMessageForSSE: %v", err)
	}
	if event == nil {
		t.Fatal("event is nil")
	}
	if !processor.IsCompleted() {
		t.Fatal("processor should mark stream completed after final algorithm.result.collection")
	}
}
