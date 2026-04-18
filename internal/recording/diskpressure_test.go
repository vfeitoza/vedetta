package recording

import (
	"sync"
	"testing"
)

type fakeDisk struct {
	mu       sync.Mutex
	avail    uint64
	required uint64
	total    uint64
}

func (f *fakeDisk) Available() uint64   { f.mu.Lock(); defer f.mu.Unlock(); return f.avail }
func (f *fakeDisk) MinRequired() uint64 { f.mu.Lock(); defer f.mu.Unlock(); return f.required }
func (f *fakeDisk) Total() uint64       { f.mu.Lock(); defer f.mu.Unlock(); return f.total }
func (f *fakeDisk) set(avail uint64)    { f.mu.Lock(); defer f.mu.Unlock(); f.avail = avail }

func TestDiskMonitorLevelTransitions(t *testing.T) {
	d := &fakeDisk{
		avail:    10 * 1024 * 1024 * 1024,
		required: 512 * 1024 * 1024,
		total:    100 * 1024 * 1024 * 1024,
	}
	m := NewDiskMonitor(d)

	if got := m.Classify(); got != DiskLevelOK {
		t.Fatalf("start: %v, want OK", got)
	}
	d.set(900 * 1024 * 1024) // < 2*512MB but >= 512MB
	if got := m.Classify(); got != DiskLevelWarning {
		t.Fatalf("900MiB: %v, want warning", got)
	}
	d.set(400 * 1024 * 1024) // < 512MB
	if got := m.Classify(); got != DiskLevelCritical {
		t.Fatalf("400MiB: %v, want critical", got)
	}
	m.SetPaused(true)
	if got := m.Classify(); got != DiskLevelPaused {
		t.Fatalf("after pause: %v, want paused", got)
	}
	m.SetPaused(false)
	d.set(10 * 1024 * 1024 * 1024)
	if got := m.Classify(); got != DiskLevelOK {
		t.Fatalf("recovered: %v, want OK", got)
	}
}
