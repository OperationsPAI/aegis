package runtimeinfra

import (
	"sync/atomic"
	"time"
)

var (
	appID       atomic.Value
	initialTime atomic.Value
)

func SetAppID(id string) { appID.Store(id) }

func AppID() string {
	v, _ := appID.Load().(string)
	return v
}

func SetInitialTime(t time.Time) { initialTime.Store(t) }

func InitialTime() time.Time {
	v, _ := initialTime.Load().(time.Time)
	return v
}
