package client

import (
	"context"
	"testing"

	"github.com/k0kubun/pp/v3"
)

func TestGetContainersWithAppLabel(t *testing.T) {
	containerInfos, err := GetContainersWithAppLabel(context.Background(), "ts0", "app")
	if err != nil {
		t.Error(err)
	}

	pp.Println(containerInfos)
}
