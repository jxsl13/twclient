package client

import (
	"sync"
	"time"
)

// ServerTickSpeed is the Teeworlds server tick rate (50 ticks/sec).
const ServerTickSpeed = 50

// predictionMarginMs is the desired lead (in ms) with which our inputs should
// arrive at the server before it processes the corresponding tick. The smooth
// clock self-corrects to maintain roughly this margin from INPUTTIMING feedback.
const predictionMarginMs = 10

const inputRingSize = 200

// ExtraInputLead adds additional whole-tick lead to the predicted tick beyond
// the smooth clock's +1. Snapshots (which set ackTick) arrive every ~2 ticks
// and lag the server's current tick, so a small extra lead ensures inputs
// reach the server before it processes each tick. Tunable for experiments.
var ExtraInputLead = 1

// clockStart anchors a monotonic clock; timeGet returns nanoseconds since it.
var clockStart = time.Now()

func timeGet() int64 { return int64(time.Since(clockStart)) }

const timeFreq = int64(time.Second) // ticks of the monotonic clock per second (ns)

// smoothTime is a faithful port of Teeworlds' CSmoothTime: a clock that
// converges smoothly toward a moving target with a direction-dependent,
// adaptive adjust speed. All values are in timeFreq (nanosecond) units.
type smoothTime struct {
	snap, current, target int64
	adjustSpeed           [2]float64
	spikeCounter          int
}

// Init seeds the smoothed clock to target with no in-flight adjustment.
func (s *smoothTime) Init(target int64) {
	s.snap = timeGet()
	s.current = target
	s.target = target
	s.adjustSpeed[0] = 0.3
	s.adjustSpeed[1] = 0.3
	s.spikeCounter = 0
}

// Get returns the current smoothed time, interpolating from current toward
// target at the adaptive adjust speed (CSmoothTime::Get).
func (s *smoothTime) Get(now int64) int64 {
	c := s.current + (now - s.snap)
	t := s.target + (now - s.snap)
	adjust := s.adjustSpeed[0]
	if t > c {
		adjust = s.adjustSpeed[1]
	}
	a := (float64(now-s.snap) / float64(timeFreq)) * adjust
	if a > 1.0 {
		a = 1.0
	}
	return c + int64(float64(t-c)*a)
}

// UpdateInt nudges the smoothed clock toward target, easing the adjustment over
// time so the predicted tick never jumps (mirrors DDNet CSmoothTime::Update).
func (s *smoothTime) UpdateInt(now, target int64) {
	s.current = s.Get(now)
	s.snap = now
	s.target = target
}

// Update nudges the clock toward target given the server-reported TimeLeft (ms),
// adapting the adjust speed and ignoring brief ping spikes (CSmoothTime::Update).
func (s *smoothTime) Update(now, target int64, timeLeftMs, dir int) {
	updateTimer := true
	if timeLeftMs < 0 {
		isSpike := false
		if timeLeftMs < -50 {
			isSpike = true
			s.spikeCounter += 5
			if s.spikeCounter > 50 {
				s.spikeCounter = 50
			}
		}
		if isSpike && s.spikeCounter < 15 {
			updateTimer = false
		} else if s.adjustSpeed[dir] < 30.0 {
			s.adjustSpeed[dir] *= 2.0
		}
	} else {
		if s.spikeCounter > 0 {
			s.spikeCounter--
		}
		s.adjustSpeed[dir] *= 0.95
		if s.adjustSpeed[dir] < 2.0 {
			s.adjustSpeed[dir] = 2.0
		}
	}
	if updateTimer {
		s.UpdateInt(now, target)
	}
}

// inputRecord remembers when (in clock and wall time) an input for a given
// predicted tick was sent, so INPUTTIMING feedback can be applied to it.
type inputRecord struct {
	tick                int
	predictedTime, time int64
}

// PredictedTime tracks the client's predicted game time as a smooth clock,
// faithfully following the Teeworlds/DDNet prediction model. It is initialized
// once (on the second snapshot) and thereafter only nudged by INPUTTIMING
// feedback — it is NOT reset on every snapshot. This keeps the predicted tick
// stable and leading the server by ~predictionMarginMs, so inputs arrive
// just-in-time on every tick.
type PredictedTime struct {
	mu sync.Mutex

	st          smoothTime
	initialized bool
	snaps       int

	ackTick  int // latest received snapshot tick (m_AckGameTick)
	predTick int // last predicted tick we sent input for (m_PredTick)

	inputs [inputRingSize]inputRecord
	cur    int
}

// OnSnapshot records the latest snapshot tick and, once two snapshots have
// arrived, initializes the smooth predicted clock a single time.
func (p *PredictedTime) OnSnapshot(tick int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if tick > p.ackTick {
		p.ackTick = tick
	}
	p.snaps++
	if !p.initialized && p.snaps >= 2 {
		p.st.Init(int64(tick) * timeFreq / ServerTickSpeed)
		// Adjust upward fast initially so we quickly reach a leading margin.
		p.st.adjustSpeed[1] = 1000.0
		p.predTick = tick
		p.initialized = true
	}
}

// AckTick returns the latest snapshot tick. Returns 0 before initialization.
func (p *PredictedTime) AckTick() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ackTick
}

// PredTick returns the current predicted tick (diagnostic). Returns 0 before
// initialization.
func (p *PredictedTime) PredTick() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.initialized {
		return 0
	}
	predNow := p.st.Get(timeGet())
	return int(predNow*ServerTickSpeed/timeFreq) + 1 + ExtraInputLead
}

// NextInput reports whether a new predicted-tick boundary has been crossed and,
// if so, returns the (predTick, ackTick) to send and records the send timing.
// Callers should send an input exactly when send is true (mirrors the real
// client's "NewPredTick > m_PredTick -> SendInput" gate).
func (p *PredictedTime) NextInput() (predTick, ackTick int, send bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.initialized {
		return 0, 0, false
	}
	now := timeGet()
	predNow := p.st.Get(now)
	newPred := int(predNow*ServerTickSpeed/timeFreq) + 1 + ExtraInputLead
	if newPred <= p.predTick {
		return 0, 0, false
	}
	p.predTick = newPred
	p.inputs[p.cur] = inputRecord{tick: newPred, predictedTime: predNow, time: now}
	p.cur = (p.cur + 1) % inputRingSize
	return newPred, p.ackTick, true
}

// Adjust applies INPUTTIMING feedback: the server reports, for the input it
// processed at intendedTick, how many ms (timeLeftMs) were left before it
// needed it. We recompute the target predicted time so future inputs arrive
// ~predictionMarginMs early, and nudge the smooth clock toward it.
func (p *PredictedTime) Adjust(intendedTick, timeLeftMs int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.initialized {
		return
	}
	now := timeGet()
	for k := 0; k < inputRingSize; k++ {
		if p.inputs[k].tick == intendedTick {
			target := p.inputs[k].predictedTime + (now - p.inputs[k].time)
			target -= int64(float64(timeLeftMs-predictionMarginMs) / 1000.0 * float64(timeFreq))
			p.st.Update(now, target, timeLeftMs, 1)
			return
		}
	}
}

// Reset clears all prediction state.
func (p *PredictedTime) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.st = smoothTime{}
	p.initialized = false
	p.snaps = 0
	p.ackTick = 0
	p.predTick = 0
	p.inputs = [inputRingSize]inputRecord{}
	p.cur = 0
}
