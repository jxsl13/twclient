package client

import "testing"

type recObserver struct {
	mode  TickMode
	ticks int
}

func (o *recObserver) Mode() TickMode             { return o.mode }
func (o *recObserver) Observe(*Client, TickState) { o.ticks++ }

type recController struct {
	mode TickMode
	emit []Action
	seen []TickState
}

func (c *recController) Mode() TickMode { return c.mode }
func (c *recController) OnTick(_ *Client, st TickState) []Action {
	c.seen = append(c.seen, st)
	return c.emit
}

// V20/V31: many observers register; AddObserver returns an idempotent remove.
func TestObserverRegistry(t *testing.T) {
	c := &Client{}
	o1, o2 := &recObserver{}, &recObserver{}
	rem1 := c.AddObserver(o1)
	c.AddObserver(o2)

	if got := len(c.observerList()); got != 2 {
		t.Fatalf("want 2 observers, got %d", got)
	}
	rem1()
	rem1() // idempotent
	if got := len(c.observerList()); got != 1 {
		t.Errorf("after remove: want 1, got %d", got)
	}
}

// V20: exactly one controller; SetController replaces it, nil clears.
func TestControllerSingle(t *testing.T) {
	c := &Client{}
	if c.controllerHandler() != nil {
		t.Error("no controller initially")
	}
	ctrl := &recController{}
	c.SetController(ctrl)
	if c.controllerHandler() != ctrl {
		t.Error("SetController did not register")
	}
	c.SetController(nil)
	if c.controllerHandler() != nil {
		t.Error("SetController(nil) should clear")
	}

	// Construction-time options.
	o := &recObserver{}
	c2 := &Client{}
	WithObserver(o)(c2)
	WithController(ctrl)(c2)
	if len(c2.observerList()) != 1 || c2.controllerHandler() != ctrl {
		t.Error("WithObserver/WithController did not register")
	}
}
