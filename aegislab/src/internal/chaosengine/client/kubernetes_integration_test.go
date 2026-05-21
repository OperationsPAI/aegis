//go:build integration

package client

import (
	"context"
	"fmt"
	"testing"
)

func TestGetLabel(t *testing.T) {
	labels, err := GetLabels(context.Background(), "ts0", "app")
	if err != nil {
		t.Error(err)
	}
	fmt.Println(labels)
}

func TestCRDClient1(t *testing.T) {
	start, end, err := QueryCRDByName("ts", "ts-ts-train-service-cpu-exhaustion-7mwd86")
	if err != nil {
		t.Error(err)
	}
	fmt.Println(start, end)
}
