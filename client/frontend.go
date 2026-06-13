package client

// TickMode selects how often the tick driver invokes a Frontend (V24).
type TickMode int

const (
	// TickModeFixed fires OnTick once per predicted server tick (50Hz,
	// IntraTick=0). Deterministic — for ML training/execution.
	TickModeFixed TickMode = iota
	// TickModeFrame fires OnTick per render frame with IntraTick∈[0,1) and
	// smoothed positions. For visual UIs.
	TickModeFrame
)

// Frontend is the single pluggable consumer interface (V20). A UI renderer, an
// ML training harness, and an ML policy all implement it the same way and plug
// into the headless Client identically. OnTick receives the complete predicted
// TickState and returns the actions to apply (UI input == ML output).
type Frontend interface {
	// Mode selects the driver cadence (fixed tick vs render frame).
	Mode() TickMode
	// OnTick observes one tick and returns actions to apply via Client.Do.
	OnTick(c *Client, st TickState) []Action
}

// WithFrontend registers a consumer at construction time.
func WithFrontend(f Frontend) Option {
	return func(c *Client) { c.frontend = f }
}

// SetFrontend registers (or replaces, with nil to clear) the consumer at
// runtime. Safe to call from any goroutine.
func (c *Client) SetFrontend(f Frontend) {
	c.mu.Lock()
	c.frontend = f
	c.mu.Unlock()
}

// frontendHandler returns the registered consumer, or nil.
func (c *Client) frontendHandler() Frontend {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.frontend
}
