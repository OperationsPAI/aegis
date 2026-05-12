package consumer

import (
	"aegis/platform/consts"
	"testing"
	"time"
)

func TestExtractPreDuration(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    time.Duration
	}{
		{
			name:    "missing pre_duration",
			payload: map[string]any{},
			want:    0,
		},
		{
			name: "float64 pre_duration",
			payload: map[string]any{
				consts.InjectPreDuration: float64(2),
			},
			want: 2 * time.Minute,
		},
		{
			name: "int pre_duration",
			payload: map[string]any{
				consts.InjectPreDuration: int(3),
			},
			want: 3 * time.Minute,
		},
		{
			name: "non-positive pre_duration",
			payload: map[string]any{
				consts.InjectPreDuration: float64(0),
			},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractPreDuration(tc.payload)
			if got != tc.want {
				t.Fatalf("extractPreDuration() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAdjustInjectTimeAfterWarmup(t *testing.T) {
	base := time.Date(2026, 4, 24, 11, 13, 35, 0, time.UTC)
	ready := time.Date(2026, 4, 24, 11, 14, 0, 0, time.UTC)

	t.Run("keep inject time when already late enough", func(t *testing.T) {
		inject := time.Date(2026, 4, 24, 11, 16, 0, 0, time.UTC)
		got := adjustInjectTimeAfterWarmup(inject, ready, map[string]any{
			consts.InjectPreDuration: float64(1),
		})
		if !got.Equal(inject) {
			t.Fatalf("expected inject time unchanged, got %s", got)
		}
	})

	t.Run("push inject time to ready plus pre_duration", func(t *testing.T) {
		inject := base
		got := adjustInjectTimeAfterWarmup(inject, ready, map[string]any{
			consts.InjectPreDuration: float64(1),
		})
		want := ready.Add(1 * time.Minute)
		if !got.Equal(want) {
			t.Fatalf("adjusted inject time = %s, want %s", got, want)
		}
	})
}
