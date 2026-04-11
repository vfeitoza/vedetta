package notify

import (
	"testing"
	"time"
)

func TestCooldownCache(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	c := NewCooldownCache(3*time.Minute, clock)

	// First Check → not suppressed.
	if c.Check("alice:front:person") {
		t.Fatalf("first check should not be suppressed")
	}
	c.Mark("alice:front:person")

	// Check within window → suppressed.
	now = now.Add(1 * time.Minute)
	if !c.Check("alice:front:person") {
		t.Fatalf("second check within window should be suppressed")
	}

	// Different key is independent.
	if c.Check("alice:front:car") {
		t.Fatalf("different key should not be suppressed")
	}

	// Check after window → not suppressed.
	now = now.Add(3 * time.Minute)
	if c.Check("alice:front:person") {
		t.Fatalf("check after window should not be suppressed")
	}
}

func TestCooldownSweep(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	c := NewCooldownCache(1*time.Minute, clock)

	c.Mark("a")
	c.Mark("b")
	// Advance well past 2× window.
	now = now.Add(5 * time.Minute)
	c.Sweep()
	if c.Size() != 0 {
		t.Fatalf("expected empty after sweep, got %d entries", c.Size())
	}
}
