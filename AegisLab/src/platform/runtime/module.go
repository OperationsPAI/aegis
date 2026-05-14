package runtimeinfra

import (
	"time"

	"aegis/platform/utils"

	"go.uber.org/fx"
)

var Module = fx.Module("runtime",
	fx.Invoke(InitializeRuntime),
)

func InitializeRuntime() {
	t := InitialTime()
	if t.IsZero() {
		t = time.Now()
		SetInitialTime(t)
	}
	if AppID() == "" {
		SetAppID(utils.GenerateULID(&t))
	}
}
