package client

import "testing"

type fakeFrontend struct {
	mode  TickMode
	ticks int
	acts  []Action
}

func (f *fakeFrontend) Mode() TickMode { return f.mode }
func (f *fakeFrontend) OnTick(_ *Client, _ TickState) []Action {
	f.ticks++
	return f.acts
}

// V20: one Frontend plugs into the client via With/SetFrontend.
func TestFrontendRegistration(t *testing.T) {
	c := &Client{}
	if c.frontendHandler() != nil {
		t.Error("no frontend expected initially")
	}

	f := &fakeFrontend{mode: TickModeFixed}
	c.SetFrontend(f)
	if c.frontendHandler() != f {
		t.Error("SetFrontend did not register")
	}
	if c.frontendHandler().Mode() != TickModeFixed {
		t.Error("mode wrong")
	}

	c.SetFrontend(nil)
	if c.frontendHandler() != nil {
		t.Error("SetFrontend(nil) should clear")
	}

	// WithFrontend at construction.
	c2 := &Client{}
	WithFrontend(f)(c2)
	if c2.frontendHandler() != f {
		t.Error("WithFrontend did not register")
	}
}
