package client

import (
	"sync"
	"time"
)

// ServerTickSpeed is the Teeworlds server tick rate (50 ticks/sec).
const ServerTickSpeed = 50

// PredictedTime tracks the client's predicted game time, advancing at
// ServerTickSpeed (50 ticks/sec) from the last acknowledged snapshot.
//
// After each snapshot at tick T, the tracker resets its base to T and
// begins advancing. PredTick() returns the current predicted tick
// (base + elapsed ticks). Input should be sent whenever PredTick
// crosses a new tick boundary—approximately 50 times per second.
//
// When INPUTTIMING feedback arrives from the server, Adjust() shifts
// the prediction offset so that inputs arrive at the server just in time.
type PredictedTime struct {
	mu sync.Mutex

	baseTick int       // tick from last snapshot
	baseTime time.Time // wall-clock time when baseTick was set

	// adjustOffset is added to the raw elapsed prediction to account for
	// INPUTTIMING feedback. Positive means we predict further ahead.
	adjustOffset time.Duration

	initialized bool
}

// OnSnapshot updates the base tick when a new snapshot arrives.
func (p *PredictedTime) OnSnapshot(tick int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.initialized {
		p.baseTick = tick
		p.baseTime = time.Now()
		p.initialized = true
		return
	}

	// Only advance forward — ignore out-of-order snapshots.
	if tick <= p.baseTick {
		return
	}

	p.baseTick = tick
	p.baseTime = time.Now()
}

// PredTick returns the current predicted tick. The prediction is
// baseTick + elapsed time since snapshot (in ticks) + 1 (look-ahead).
// Returns 0 if no snapshot has been received yet.
func (p *PredictedTime) PredTick() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.initialized {
		return 0
	}

	elapsed := max(time.Since(p.baseTime)+p.adjustOffset, 0)
	elapsedTicks := int(elapsed / tickDuration)

	return p.baseTick + elapsedTicks + 1
}

// AckTick returns the current base tick (latest snapshot tick).
// Returns 0 if no snapshot has been received yet.
func (p *PredictedTime) AckTick() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.baseTick
}

// Adjust processes INPUTTIMING feedback from the server. The server
// tells us how many milliseconds were left (timeLeftMs) before it
// processed the tick we predicted (intendedTick). A negative timeLeft
// means we were too late; positive means too early.
//
// We want inputs to arrive ~PredictionMargin ms early, so we adjust:
//   - If timeLeft is large and positive → we're predicting too far ahead → slow down
//   - If timeLeft is negative → we're too late → speed up
//
// The adjustment is smoothed to avoid oscillation.
func (p *PredictedTime) Adjust(intendedTick int, timeLeftMs int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.initialized {
		return
	}

	// Target: arrive ~predictionMargin ms early. The DDNet default is 10ms
	// for servers with SyncWeaponInput, otherwise a fixed 10ms.
	const predictionMarginMs = 10

	// error = how much we need to adjust (positive = need to predict further)
	errorMs := predictionMarginMs - timeLeftMs

	// Smooth the adjustment to avoid oscillation (apply ~10% of error)
	adjustMs := errorMs / 10
	if adjustMs == 0 && errorMs != 0 {
		// Ensure we make at least 1ms progress toward the target
		if errorMs > 0 {
			adjustMs = 1
		} else {
			adjustMs = -1
		}
	}

	p.adjustOffset += time.Duration(adjustMs) * time.Millisecond
}

// Reset clears the predicted time state.
func (p *PredictedTime) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.baseTick = 0
	p.baseTime = time.Time{}
	p.adjustOffset = 0
	p.initialized = false
}
