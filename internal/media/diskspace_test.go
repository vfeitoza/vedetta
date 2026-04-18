package media

import (
	"errors"
	"testing"
	"time"
)

func TestMinRequiredStaticFloor(t *testing.T) {
	ds := NewDiskSpace(t.TempDir())
	ds.SetThreshold(1024*1024*1024, nil) // 1 GiB, no dynamic provider
	if got := ds.MinRequired(); got != 1024*1024*1024 {
		t.Fatalf("MinRequired = %d, want 1 GiB", got)
	}
}

func TestMinRequiredUsesLargestSegmentTimesTwo(t *testing.T) {
	ds := NewDiskSpace(t.TempDir())
	// Static floor 512 MiB, but largest recent segment is 400 MiB.
	// Expect: max(512 MiB, 2 × 400 MiB) = 800 MiB.
	provider := func(since time.Time) (int64, error) {
		return 400 * 1024 * 1024, nil
	}
	ds.SetThreshold(512*1024*1024, provider)
	if got := ds.MinRequired(); got != 800*1024*1024 {
		t.Fatalf("MinRequired = %d, want 800 MiB", got)
	}
}

func TestMinRequiredFallsBackOnProviderError(t *testing.T) {
	ds := NewDiskSpace(t.TempDir())
	provider := func(since time.Time) (int64, error) {
		return 0, errors.New("boom")
	}
	ds.SetThreshold(512*1024*1024, provider)
	if got := ds.MinRequired(); got != 512*1024*1024 {
		t.Fatalf("MinRequired with provider error = %d, want 512 MiB", got)
	}
}

func TestMinRequiredDefaultStaticFloor(t *testing.T) {
	// No SetThreshold call — default static floor preserves old MinDiskSpace = 256 MB
	ds := NewDiskSpace(t.TempDir())
	if got := ds.MinRequired(); got != 256*1024*1024 {
		t.Fatalf("default MinRequired = %d, want 256 MiB", got)
	}
}
