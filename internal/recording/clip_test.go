package recording

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "00:00:00.000"},
		{5 * time.Second, "00:00:05.000"},
		{90 * time.Second, "00:01:30.000"},
		{time.Hour + 30*time.Minute + 15*time.Second + 500*time.Millisecond, "01:30:15.500"},
		{2*time.Hour + 500*time.Millisecond, "02:00:00.500"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
