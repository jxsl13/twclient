package client

// TickMode selects how often the tick driver invokes a consumer (V24).
type TickMode int

const (
	// TickModeFixed fires once per predicted server tick (50Hz, IntraTick=0).
	// Deterministic — for ML training/execution.
	TickModeFixed TickMode = iota
	// TickModeFrame fires per render frame with IntraTick∈[0,1) and smoothed
	// positions. For visual UIs.
	TickModeFrame
)

// Observer is a view-only consumer (V20). Many may plug in simultaneously
// (renderers, ML-training data collectors). It receives the complete predicted
// TickState each tick but emits no actions.
type Observer interface {
	Mode() TickMode
	Observe(c *Client, st TickState)
}

// Controller is the single view+action consumer (V20): an ML policy or the user
// input source. It observes the TickState and returns actions to apply.
type Controller interface {
	Mode() TickMode
	OnTick(c *Client, st TickState) []Action
}

// AddObserver registers a view-only consumer and returns an idempotent
// unregister closure. Safe for concurrent use (V31).
func (c *Client) AddObserver(o Observer) (remove func()) {
	c.mu.Lock()
	if c.observers == nil {
		c.observers = make(map[uint64]Observer)
	}
	id := c.obsNextID
	c.obsNextID++
	c.observers[id] = o
	c.mu.Unlock()

	removed := false
	return func() {
		c.mu.Lock()
		if !removed {
			delete(c.observers, id)
			removed = true
		}
		c.mu.Unlock()
	}
}

// SetController registers (or replaces, nil to clear) the single action-emitting
// consumer.
func (c *Client) SetController(ctrl Controller) {
	c.mu.Lock()
	c.controller = ctrl
	c.mu.Unlock()
}

// WithObserver registers a view-only consumer at construction.
func WithObserver(o Observer) Option {
	return func(c *Client) { _ = c.AddObserver(o) }
}

// WithController registers the action consumer at construction.
func WithController(ctrl Controller) Option {
	return func(c *Client) { c.controller = ctrl }
}

// observerList returns a snapshot of the registered observers.
func (c *Client) observerList() []Observer {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.observers) == 0 {
		return nil
	}
	out := make([]Observer, 0, len(c.observers))
	for _, o := range c.observers {
		out = append(out, o)
	}
	return out
}

// controllerHandler returns the registered controller, or nil.
func (c *Client) controllerHandler() Controller {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.controller
}

// hasConsumers reports whether any observer or controller is registered.
func (c *Client) hasConsumers() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.observers) > 0 || c.controller != nil
}
