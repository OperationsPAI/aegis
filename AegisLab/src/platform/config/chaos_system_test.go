package config

import (
	"testing"
	"time"
)

func TestChaosSystemConfig_ReadinessTimeout_Default(t *testing.T) {
	cfg := ChaosSystemConfig{}
	got := cfg.ReadinessTimeout()
	want := time.Duration(DefaultReadinessTimeoutSeconds) * time.Second
	if got != want {
		t.Fatalf("ReadinessTimeout() = %s, want %s (default)", got, want)
	}
}

func TestChaosSystemConfig_ReadinessTimeout_Negative(t *testing.T) {
	cfg := ChaosSystemConfig{ReadinessTimeoutSeconds: -1}
	got := cfg.ReadinessTimeout()
	want := time.Duration(DefaultReadinessTimeoutSeconds) * time.Second
	if got != want {
		t.Fatalf("ReadinessTimeout() = %s, want %s (default for negative)", got, want)
	}
}

func TestChaosSystemConfig_ReadinessTimeout_Override(t *testing.T) {
	cfg := ChaosSystemConfig{ReadinessTimeoutSeconds: 1500} // 25 min DSB-class
	got := cfg.ReadinessTimeout()
	want := 1500 * time.Second
	if got != want {
		t.Fatalf("ReadinessTimeout() = %s, want %s (override)", got, want)
	}
}
