package domain

import (
	"testing"
	"time"
)

func TestBiasScore_String(t *testing.T) {
	tests := []struct {
		score BiasScore
		want  string
	}{
		{BiasBullish, "Bullish"},
		{BiasBearish, "Bearish"},
		{BiasNeutral, "Neutral"},
		{BiasScore(99), "Neutral"}, // unknown defaults to Neutral
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.score.String(); got != tt.want {
				t.Errorf("BiasScore(%d).String() = %q, want %q", tt.score, got, tt.want)
			}
		})
	}
}

func TestBiasResult_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{
			name:      "not expired",
			expiresAt: time.Now().Add(1 * time.Hour),
			want:      false,
		},
		{
			name:      "expired",
			expiresAt: time.Now().Add(-1 * time.Second),
			want:      true,
		},
		{
			name:      "exactly now is expired",
			expiresAt: time.Now(),
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := BiasResult{ExpiresAt: tt.expiresAt}
			got := b.IsExpired()
			if got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}
