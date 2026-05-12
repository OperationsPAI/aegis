package trace

import (
	"encoding/json"
	"testing"

	"aegis/platform/consts"
	"aegis/platform/dto"
	"aegis/core/domain/execution"

	"github.com/redis/go-redis/v9"
)

func TestParseStreamEvent_AlgoResultCollectionPayload(t *testing.T) {
	payloadBytes, err := json.Marshal([]execution.GranularityResultItem{{
		Level:      "service",
		Result:     "loadgenerator",
		Rank:       1,
		Confidence: 0.9,
	}})
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

	result, ok := event.Payload.(*[]execution.GranularityResultItem)
	if !ok {
		t.Fatalf("payload type = %T, want *[]execution.GranularityResultItem", event.Payload)
	}
	if len(*result) != 1 {
		t.Fatalf("payload length = %d, want %d", len(*result), 1)
	}
	if (*result)[0].Result != "loadgenerator" {
		t.Fatalf("payload result = %q, want %q", (*result)[0].Result, "loadgenerator")
	}
}

func TestStreamProcessor_ProcessMessageForSSE_AlgoResultCollectionCompletes(t *testing.T) {
	payloadBytes, err := json.Marshal([]execution.GranularityResultItem{{
		Level:      "service",
		Result:     "loadgenerator",
		Rank:       1,
		Confidence: 0.9,
	}})
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

func TestStreamProcessor_ProcessMessageForSSE_AlgoNoResultDataCompletes(t *testing.T) {
	processor := NewStreamProcessor([]dto.ContainerVersionItem{
		{ContainerName: "random"},
	})

	_, event, err := processor.ProcessMessageForSSE(redis.XMessage{
		ID: "1776894182633-0",
		Values: map[string]any{
			consts.RdbEventTaskID:   "task-1",
			consts.RdbEventTaskType: consts.GetTaskTypeName(consts.TaskTypeCollectResult),
			consts.RdbEventName:     string(consts.EventAlgoNoResultData),
			consts.RdbEventPayload:  "[]",
		},
	})
	if err != nil {
		t.Fatalf("ProcessMessageForSSE: %v", err)
	}
	if event == nil {
		t.Fatal("event is nil")
	}
	if !processor.IsCompleted() {
		t.Fatal("processor should mark stream completed after algorithm.no_result_data")
	}
}
