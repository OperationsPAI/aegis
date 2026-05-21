//go:build integration

package chaosclient

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
