package handler

import (
	"strings"
	"testing"

	"github.com/OperationsPAI/chaos-experiment/internal/resourcelookup"
	"github.com/OperationsPAI/chaos-experiment/internal/systemconfig"
)

func TestSelectContainerByIndex_EmptyListGivesActionableError(t *testing.T) {
	// Reproduces the sockshop "max: -1" failure: GetGroundtruth runs at a
	// moment when no pods carry the configured app-label key, so the
	// container snapshot is empty. The previous implementation reported
	// "container index out of range: 5 (max: -1)" which gave the user no hint
	// that the list was actually empty.
	_, err := selectContainerByIndex(
		nil, // empty list
		systemconfig.SystemSockShop,
		"sockshop1",
		5,
	)
	if err == nil {
		t.Fatal("expected error for empty container list, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"no containers found",
		`system "sockshop"`,
		`namespace "sockshop1"`,
		"index 5",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q is missing required substring %q", msg, want)
		}
	}
	// And it must NOT report the bogus "max: -1" any longer.
	if strings.Contains(msg, "max: -1") {
		t.Errorf("error %q still contains the unhelpful 'max: -1' phrasing", msg)
	}
}

func TestSelectContainerByIndex_OutOfRangeReportsCount(t *testing.T) {
	containers := []resourcelookup.ContainerInfo{
		{PodName: "p1", AppLabel: "front-end", ContainerName: "front-end"},
		{PodName: "p2", AppLabel: "catalogue", ContainerName: "catalogue"},
	}
	_, err := selectContainerByIndex(
		containers,
		systemconfig.SystemSockShop,
		"sockshop1",
		7,
	)
	if err == nil {
		t.Fatal("expected error for out-of-range index, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"out of range",
		"index 7",
		"2 containers available",
		"valid range 0..1",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q is missing required substring %q", msg, want)
		}
	}
}

func TestSelectContainerByIndex_NegativeIndexHandled(t *testing.T) {
	containers := []resourcelookup.ContainerInfo{
		{PodName: "p1", AppLabel: "front-end", ContainerName: "front-end"},
	}
	_, err := selectContainerByIndex(
		containers,
		systemconfig.SystemSockShop,
		"sockshop1",
		-1,
	)
	if err == nil {
		t.Fatal("expected error for negative index, got nil")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("expected out-of-range error, got %q", err.Error())
	}
}

func TestSelectContainerByIndex_ValidIndexReturnsEntry(t *testing.T) {
	containers := []resourcelookup.ContainerInfo{
		{PodName: "p1", AppLabel: "front-end", ContainerName: "front-end"},
		{PodName: "p2", AppLabel: "catalogue", ContainerName: "catalogue"},
	}
	got, err := selectContainerByIndex(
		containers,
		systemconfig.SystemSockShop,
		"sockshop1",
		1,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != containers[1] {
		t.Errorf("got %+v, want %+v", got, containers[1])
	}
}
