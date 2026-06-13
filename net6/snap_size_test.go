package net6

import (
	"context"
	"testing"
)

// V53: WithSnapStorageSize propagates through StartReader to the reader's
// packet.SnapStorage.MaxSnaps; unset keeps the default (16), invalid is clamped.
// Protocol-unified with net7 (C2) — see net7's equivalent test.
func TestWithSnapStorageSize(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want int
	}{
		{"default", nil, 16},
		{"set", []Option{WithSnapStorageSize(48)}, 48},
		{"clamp-up", []Option{WithSnapStorageSize(1)}, 3},
		{"clamp-default", []Option{WithSnapStorageSize(-1)}, 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Port 0 never receives, so the reader just idles on timeouts.
			s, err := NewSession("127.0.0.1:34999", c.opts...)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			defer s.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.StartReader(ctx)
			defer s.StopReader()

			if got := s.reader.snaps.MaxSnaps; got != c.want {
				t.Errorf("MaxSnaps = %d, want %d", got, c.want)
			}
		})
	}
}

// V54: WithEventChanSize sets the reader event-channel buffer; unset keeps the
// default (128), <=0 falls back to default. Protocol-unified with net7.
func TestWithEventChanSize(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want int
	}{
		{"default", nil, defaultEventChanSize},
		{"set", []Option{WithEventChanSize(512)}, 512},
		{"zero-default", []Option{WithEventChanSize(0)}, defaultEventChanSize},
		{"neg-default", []Option{WithEventChanSize(-1)}, defaultEventChanSize},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := NewSession("127.0.0.1:34999", c.opts...)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.StartReader(ctx)
			defer s.StopReader()
			if got := cap(s.reader.eventCh); got != c.want {
				t.Errorf("cap(eventCh) = %d, want %d", got, c.want)
			}
		})
	}
}
