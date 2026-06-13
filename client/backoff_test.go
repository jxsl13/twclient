package client

import (
	"testing"
	"time"
)

// V36: default exponential backoff doubles from 1s and caps at 1h; Reset
// returns to the base.
func TestExponentialBackoff(t *testing.T) {
	b := NewExponentialBackoff(time.Second, 2, time.Hour)
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i, w := range want {
		if got := b.Next(); got != w {
			t.Errorf("Next[%d] = %v, want %v", i, got, w)
		}
	}
	// Drive to the cap.
	for range 20 {
		b.Next()
	}
	if got := b.Next(); got != time.Hour {
		t.Errorf("capped Next = %v, want 1h", got)
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Errorf("after Reset Next = %v, want 1s", got)
	}
}

// V41: invalid inputs fall back to sane defaults (no zero-delay busy loop).
func TestExponentialBackoffValidation(t *testing.T) {
	b := NewExponentialBackoff(0, 0.5, -1)
	if d := b.Next(); d <= 0 {
		t.Errorf("base must be positive, got %v", d)
	}
}

// V36/V41: policy ctor applies defaults; options override.
func TestReconnectPolicy(t *testing.T) {
	d := DefaultReconnectPolicy()
	if !d.enabled || d.maxAttempts != 0 || d.backoff == nil || !d.resumeWithTimeout {
		t.Fatalf("default policy wrong: %+v", d)
	}

	fb := &fakeBackoff{}
	p := NewReconnectPolicy(WithMaxAttempts(5), WithBackoff(fb), WithResumeTimeout(false))
	if p.maxAttempts != 5 || p.backoff != fb || p.resumeWithTimeout {
		t.Fatalf("options not applied: %+v", p)
	}
}
