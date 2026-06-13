package client

import (
	"context"
	"time"
)

// dispatchFixed delivers a fixed-cadence tick state to all fixed-mode observers
// and the controller (if fixed), applying the controller's actions (V31).
func (c *Client) dispatchFixed(st TickState) {
	for _, o := range c.observerList() {
		if o.Mode() == TickModeFixed {
			o.Observe(c, st)
		}
	}
	if ctrl := c.controllerHandler(); ctrl != nil && ctrl.Mode() == TickModeFixed {
		for _, a := range ctrl.OnTick(c, st) {
			_ = c.Do(a)
		}
	}
}

// dispatchFrame delivers a frame-cadence tick state to all frame-mode observers
// and the controller (if frame), applying the controller's actions (V31).
func (c *Client) dispatchFrame(st TickState) {
	for _, o := range c.observerList() {
		if o.Mode() == TickModeFrame {
			o.Observe(c, st)
		}
	}
	if ctrl := c.controllerHandler(); ctrl != nil && ctrl.Mode() == TickModeFrame {
		for _, a := range ctrl.OnTick(c, st) {
			_ = c.Do(a)
		}
	}
}

// anyFrame reports whether any registered consumer uses frame cadence.
func (c *Client) anyFrame() bool {
	for _, o := range c.observerList() {
		if o.Mode() == TickModeFrame {
			return true
		}
	}
	if ctrl := c.controllerHandler(); ctrl != nil && ctrl.Mode() == TickModeFrame {
		return true
	}
	return false
}

// buildFrameState builds a TickState for the current tick and overlays smoothed
// positions at sub-tick fraction intra (V21/V24). It reuses the canonical
// buildTickState — there is no separate assembly path.
func (c *Client) buildFrameState(intra float32) TickState {
	st := c.buildTickState()
	st.IntraTick = intra
	st.Players = c.SmoothedCharacters(intra)
	return st
}

// frameIntra estimates the sub-tick fraction from wall-clock time since the
// last snapshot, clamped to [0,1).
func (c *Client) frameIntra() float32 {
	c.mu.RLock()
	last := c.snap.lastSnapTime
	c.mu.RUnlock()
	if last.IsZero() {
		return 0
	}
	frac := float32(time.Since(last)) / float32(tickDuration)
	if frac < 0 {
		return 0
	}
	if frac > 0.999 {
		return 0.999
	}
	return frac
}

// RunFrontends drives all registered consumers until ctx is cancelled (V31).
// A single loop builds the canonical TickState once per new predicted tick
// (shared by all fixed-cadence consumers) and, when any frame-cadence consumer
// is present, once per render frame with interpolation. Returns immediately if
// no consumer is registered.
func (c *Client) RunFrontends(ctx context.Context) {
	if !c.hasConsumers() {
		return
	}
	const fps = 60
	ticker := time.NewTicker(time.Second / fps)
	defer ticker.Stop()

	lastTick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Fixed cadence: build & dispatch once per new predicted tick.
			if pt := c.predTime.PredTick(); pt > lastTick {
				lastTick = pt
				c.dispatchFixed(c.buildTickState())
			}
			// Frame cadence: build & dispatch once per frame (only if needed).
			if c.anyFrame() {
				c.dispatchFrame(c.buildFrameState(c.frameIntra()))
			}
		}
	}
}
