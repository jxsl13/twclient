package client

import (
	"context"
	"errors"
	"testing"

	"github.com/jxsl13/twmap"
)

// mapFailStub is a session whose map download always fails.
type mapFailStub struct {
	*stubSession
}

func (s *mapFailStub) DownloadMap(context.Context) (*twmap.Map, error) {
	return nil, errors.New("simulated map download failure")
}

// issue #8 / V144: WithRequireMap → Connect returns ErrMapDownload when the map
// download fails, instead of silently succeeding mapless.
func TestRequireMapErrorsOnDownloadFailure(t *testing.T) {
	stub := &mapFailStub{stubSession: &stubSession{}}
	c := New("x:8303", WithRequireMap())
	c.newSessionFn = func() (Session, error) { return stub, nil }

	err := c.Connect(context.Background())
	t.Cleanup(func() { _ = c.Close() })
	if !errors.Is(err, ErrMapDownload) {
		t.Fatalf("Connect err = %v, want ErrMapDownload", err)
	}
}

// issue #8 / V144: default (no WithRequireMap) keeps connecting mapless, but the
// state is DETECTABLE — HasMap() reports false (was: silent success).
func TestDefaultMaplessConnectDetectable(t *testing.T) {
	stub := &mapFailStub{stubSession: &stubSession{}}
	c := New("x:8303")
	c.newSessionFn = func() (Session, error) { return stub, nil }

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect (default, mapless) should not error: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if c.HasMap() {
		t.Error("HasMap() = true, want false after a failed map download")
	}
}

// issue #9 / V145: WithMapDownloadProgress wires a callback onto the client
// (threaded into the session's DownloadMap in newSession).
func TestWithMapDownloadProgressSetsCallback(t *testing.T) {
	var got [2]int
	c := New("x:8303", WithMapDownloadProgress(func(received, total int) {
		got = [2]int{received, total}
	}))
	if c.mapProgress == nil {
		t.Fatal("WithMapDownloadProgress did not set the callback")
	}
	c.mapProgress(7, 16)
	if got != [2]int{7, 16} {
		t.Errorf("callback got %v, want [7 16]", got)
	}
}
