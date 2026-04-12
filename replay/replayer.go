package replay

import (
	"io"
	"log/slog"
	"math"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
)

// Replayer is the high-level replay controller. It wraps a recording file
// and provides a tick-driven input stream. Navigation to the recording's
// start position is handled reactively by WalkToStart before playback.
//
// Use NewReplayer to create one, then call Attach to bind it to a connected
// game client.
type Replayer struct {
	provider InputProvider
	info     RecordingInfo

	// startPos is the world position where the recording begins.
	// For ghost files this comes from the first CharacterFrame.
	// Zero means unknown (provider is pure input without positions).
	startX, startY int
	hasStartPos    bool

	// frames holds all pre-buffered recording inputs.
	frames []InputFrame

	// frameIdx is the cursor into frames during playback.
	frameIdx int

	// attached is true after Attach has been called.
	attached bool

	// nav is the attached client navigator (set by Attach).
	nav Navigator

	// grid is the tile grid extracted from the map (set by Attach).
	grid *TileGrid

	// resyncIdx is the target frame index when drift is too large.
	// Set by findResyncFrame; reset when drift decreases.
	resyncIdx int

	// map reference from the attached client.
	gameMap *twmap.Map

	// charX, charY is the last known character position.
	charX, charY int

	logger *slog.Logger
}

// NewReplayer creates a Replayer from an already-opened InputProvider.
// All frames are buffered immediately and the provider is closed.
//
// If the recording has a known start position (e.g. from a ghost file's
// first character frame), pass it via SetStartPos after construction.
func NewReplayer(provider InputProvider) (*Replayer, error) {
	r := &Replayer{
		provider: provider,
		info:     provider.Info(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// Buffer all frames.
	for {
		frame, err := provider.NextInput()
		if err != nil {
			break
		}
		r.frames = append(r.frames, frame)
	}
	provider.Close()

	if len(r.frames) == 0 {
		return nil, ErrNoFrames
	}

	// Shift jump triggers back by 1 frame. The input adapter detects
	// jumps from Y-position delta (the tee moved upward). This signal
	// appears 1 frame AFTER the actual jump input because the physics
	// apply the impulse before the position update. Shifting the OFF→ON
	// transition back by 1 frame aligns the reconstructed input with the
	// original player's timing.
	for i := 1; i < len(r.frames); i++ {
		if r.frames[i].Input.Jump == packet.JumpOn && r.frames[i-1].Input.Jump == packet.JumpOff {
			r.frames[i-1].Input.Jump = packet.JumpOn
		}
	}

	// Convert sustained jump holds into edge-triggered pulses.
	//
	// The TW server triggers jumps on 0→1 transitions of m_Jump only.
	// Holding jump=1 continuously fires the ground jump on the first
	// tick but NEVER the air jump (because m_Jumped bit 0 stays set).
	// The original player tapped jump briefly and re-pressed for air
	// jumps, but the ghost only records character positions, so the
	// adapter cannot distinguish held vs. pulsed.
	//
	// Fix: for each sustained jump=1 sequence:
	// 1. Keep jump=1 for the first 2 frames (ground jump trigger)
	// 2. Set all subsequent frames to jump=0 (release)
	// 3. Detect air jumps via trajectory analysis (sudden ddy < -4
	//    = upward acceleration spike) and re-insert jump=1 there
	for i := 0; i < len(r.frames); i++ {
		if r.frames[i].Input.Jump != packet.JumpOn {
			continue
		}

		// Find extent of this jump=1 run.
		start := i
		end := i
		for end < len(r.frames) && r.frames[end].Input.Jump == packet.JumpOn {
			end++
		}

		if end-start <= 2 {
			// Short hold (1-2 frames): already a pulse, skip.
			i = end - 1
			continue
		}

		// Convert frames [start+2, end) to JumpOff.
		for j := start + 2; j < end; j++ {
			r.frames[j].Input.Jump = packet.JumpOff
		}

		// Detect air jump via trajectory: scan for sudden increase in
		// upward velocity (ddy < -4) at least 3 frames into the arc.
		for j := start + 3; j < end; j++ {
			if !r.frames[j].HasPos || !r.frames[j-1].HasPos || !r.frames[j-2].HasPos {
				continue
			}
			dy := r.frames[j].ExpectedY - r.frames[j-1].ExpectedY
			prevDy := r.frames[j-1].ExpectedY - r.frames[j-2].ExpectedY
			ddy := dy - prevDy
			// Air jump signature: ddy < -4 (sudden upward impulse)
			// while already ascending (dy more negative than previous).
			if ddy < -4 && dy < prevDy {
				r.frames[j].Input.Jump = packet.JumpOn
				break // one air jump per ground jump
			}
		}

		i = end - 1
	}

	return r, nil
}

// ErrNoFrames is returned when a recording contains no input frames.
var ErrNoFrames = io.EOF

// SetStartPos sets the world position where the recording begins.
// For ghost files, this is typically the X,Y from the first CharacterFrame.
func (r *Replayer) SetStartPos(x, y int) {
	r.startX = x
	r.startY = y
	r.hasStartPos = true
}

// Info returns recording metadata.
func (r *Replayer) Info() RecordingInfo { return r.info }

// NumFrames returns the number of buffered recording frames.
func (r *Replayer) NumFrames() int { return len(r.frames) }

// StartPos returns the world position where the recording begins.
// ok is false if the start position is unknown (non-ghost formats).
func (r *Replayer) StartPos() (x, y int, ok bool) {
	return r.startX, r.startY, r.hasStartPos
}

// SetLogger sets the logger for navigation and replay diagnostics.
func (r *Replayer) SetLogger(l *slog.Logger) {
	if l != nil {
		r.logger = l
	}
}

// Attach binds the replayer to a connected game client via the Navigator
// interface. It reads the current map and character position.
//
// This must be called after the client is connected and has received its
// first snapshot (so the map and character position are available).
//
// Navigation to the start position is performed separately via
// WalkToStart or WalkToPosition — Attach only stores references.
func (r *Replayer) Attach(nav Navigator) error {
	r.nav = nav
	r.attached = true

	// Get the map.
	r.gameMap = nav.Map()
	if r.gameMap == nil {
		r.logger.Warn("replayer: no map available")
		return nil
	}
	r.grid = NewTileGrid(r.gameMap)

	// Get the current character position (spawn position).
	r.charX, r.charY = nav.CharacterPos()
	r.logger.Info("replayer attached",
		"spawn_x", r.charX, "spawn_y", r.charY,
		"has_start_pos", r.hasStartPos,
		"start_x", r.startX, "start_y", r.startY)

	return nil
}

// Map returns the parsed game map, or nil if not yet attached.
func (r *Replayer) Map() *twmap.Map { return r.gameMap }

// Grid returns the tile grid, or nil if not yet attached.
func (r *Replayer) Grid() *TileGrid { return r.grid }

// CharacterPos returns the last observed character position.
func (r *Replayer) CharacterPos() (int, int) {
	if r.nav != nil {
		r.charX, r.charY = r.nav.CharacterPos()
	}
	return r.charX, r.charY
}

// UpdateCharacter refreshes the character position from the navigator.
func (r *Replayer) UpdateCharacter() {
	if r.nav != nil {
		r.charX, r.charY = r.nav.CharacterPos()
	}
}

// NextInput returns the next recording input. Returns io.EOF when done.
func (r *Replayer) NextInput() (packet.PlayerInput, error) {
	if r.frameIdx < len(r.frames) {
		input := r.frames[r.frameIdx].Input
		if input.PlayerFlags == 0 {
			input.PlayerFlags = packet.PlayerFlagPlaying
		}
		r.frameIdx++
		return input, nil
	}
	return packet.PlayerInput{}, io.EOF
}

// ReplayFrame returns the recording input at the given frame index.
// This is useful for tick-synchronized playback where the caller maps
// predicted ticks to frame indices directly.
func (r *Replayer) ReplayFrame(idx int) (packet.PlayerInput, bool) {
	if idx < 0 || idx >= len(r.frames) {
		return packet.PlayerInput{}, false
	}
	input := r.frames[idx].Input
	if input.PlayerFlags == 0 {
		input.PlayerFlags = packet.PlayerFlagPlaying
	}
	return input, true
}

// DebugFrame returns the buffered frame metadata at idx for diagnostics.
func (r *Replayer) DebugFrame(idx int) (InputFrame, bool) {
	if idx < 0 || idx >= len(r.frames) {
		return InputFrame{}, false
	}
	return r.frames[idx], true
}

// Reset rewinds the replayer to the beginning of the recording.
func (r *Replayer) Reset() {
	r.frameIdx = 0
}

// FindRecoveryTarget finds a reachable position on the ghost's trajectory
// for the tee to navigate to when stuck. It requires the target to be at
// least minTileDist tiles away AND at a different Y level (to escape ledges).
//
// Returns (worldX, worldY, recoveryIdx, true) on success.
func (r *Replayer) FindRecoveryTarget(fromIdx, actualX, actualY int) (wx, wy, recoveryIdx int, ok bool) {
	return r.FindRecoveryTargetRange(fromIdx, len(r.frames), actualX, actualY)
}

// FindRecoveryTargetRange is like FindRecoveryTarget but restricts the search
// to frames in [fromIdx, toIdx).
func (r *Replayer) FindRecoveryTargetRange(fromIdx, toIdx, actualX, actualY int) (wx, wy, recoveryIdx int, ok bool) {
	if r.grid == nil {
		return 0, 0, 0, false
	}
	if toIdx > len(r.frames) {
		toIdx = len(r.frames)
	}
	// Prefer local recoveries. Searching too far ahead can select a frame on
	// the same Y level but thousands of world units away, which is useless when
	// the tee is stuck at a nearby obstacle.
	toIdx = min(toIdx, fromIdx+300)

	curTile := WorldToTile(actualX, actualY)

	// Minimum tile distance to avoid targeting the same area.
	const minDist2 = 5 * 5
	const yPenalty = 12
	const framePenaltyDiv = 10

	bestScore := int(^uint(0) >> 1)
	bestWX, bestWY, bestIdx := 0, 0, 0
	searchStart := max(
		// only search forward
		fromIdx+10, 0)
	for i := searchStart; i < toIdx; i += 5 {
		f := r.frames[i]
		if !f.HasPos {
			continue
		}
		gt := WorldToTile(f.ExpectedX, f.ExpectedY)
		// Search the ghost tile and up to 8 tiles downward to find a
		// grounded tile near the ghost's position (ghost may be airborne).
		for _, dy := range []int{0, -1, -2, 1, 2, 3, 4, 5, 6, 7, 8} {
			ty := gt.Y + dy
			if ty < 0 || ty >= r.grid.H {
				continue
			}
			if !r.grid.isPassable(gt.X, ty) {
				continue
			}
			dx := gt.X - curTile.X
			ddy := ty - curTile.Y
			dist2 := dx*dx + ddy*ddy
			// Require target to be on solid ground (walkable: air with solid below).
			if ty+1 >= r.grid.H || !r.grid.isSolid(gt.X, ty+1) {
				continue // try next dy offset
			}
			if dist2 < minDist2 {
				break // too close regardless of dy
			}

			// Prefer physically nearby candidates and only gently penalize Y and
			// frame distance. A hard same-level preference causes absurd jumps far
			// ahead in the ghost when the local obstacle requires a vertical move.
			absDY := ddy
			if absDY < 0 {
				absDY = -absDY
			}
			score := dist2 + yPenalty*absDY + (i-fromIdx)/framePenaltyDiv
			if score < bestScore {
				bestScore = score
				bestIdx = i
				bestWX, bestWY = TileToWorld(TilePos{gt.X, ty})
			}
			break
		}
	}

	if bestScore != int(^uint(0)>>1) {
		return bestWX, bestWY, bestIdx, true
	}
	return 0, 0, 0, false
}

// ExpectedPos returns the expected world position at the given frame index.
// ok is false if the frame does not exist or has no position data.
func (r *Replayer) ExpectedPos(idx int) (x, y int, ok bool) {
	if idx < 0 || idx >= len(r.frames) {
		return 0, 0, false
	}
	f := r.frames[idx]
	return f.ExpectedX, f.ExpectedY, f.HasPos
}

// ExpectedVelocity returns the local trajectory velocity around idx in world
// units per recording tick. It derives the value from neighboring buffered
// positions so it stays useful even when raw ghost velocity fields are zero.
func (r *Replayer) ExpectedVelocity(idx int) (vx, vy float64, ok bool) {
	if idx < 0 || idx >= len(r.frames) || !r.frames[idx].HasPos {
		return 0, 0, false
	}

	prev := -1
	for i := idx - 1; i >= 0; i-- {
		if r.frames[i].HasPos {
			prev = i
			break
		}
	}
	next := -1
	for i := idx + 1; i < len(r.frames); i++ {
		if r.frames[i].HasPos {
			next = i
			break
		}
	}

	switch {
	case prev >= 0 && next >= 0:
		dt := r.frames[next].Tick - r.frames[prev].Tick
		if dt == 0 {
			return 0, 0, false
		}
		return float64(r.frames[next].ExpectedX-r.frames[prev].ExpectedX) / float64(dt),
			float64(r.frames[next].ExpectedY-r.frames[prev].ExpectedY) / float64(dt), true
	case next >= 0:
		dt := r.frames[next].Tick - r.frames[idx].Tick
		if dt == 0 {
			return 0, 0, false
		}
		return float64(r.frames[next].ExpectedX-r.frames[idx].ExpectedX) / float64(dt),
			float64(r.frames[next].ExpectedY-r.frames[idx].ExpectedY) / float64(dt), true
	case prev >= 0:
		dt := r.frames[idx].Tick - r.frames[prev].Tick
		if dt == 0 {
			return 0, 0, false
		}
		return float64(r.frames[idx].ExpectedX-r.frames[prev].ExpectedX) / float64(dt),
			float64(r.frames[idx].ExpectedY-r.frames[prev].ExpectedY) / float64(dt), true
	default:
		return 0, 0, false
	}
}

// Close releases resources.
func (r *Replayer) Close() error {
	return nil // frames already buffered and provider closed
}

// ApproachType classifies how the tee crosses the start line.
type ApproachType int

const (
	// ApproachWalk — primarily horizontal. Lead-in is behind on X axis.
	ApproachWalk ApproachType = iota
	// ApproachFall — primarily downward (positive velY). Lead-in is above.
	ApproachFall
	// ApproachSwing — hook active in early frames + multi-axis velocity.
	// Lead-in is back-extrapolated along the arc; inputs include hook.
	ApproachSwing
	// ApproachJump — upward launch (negative velY). Lead-in is below.
	ApproachJump
)

func (a ApproachType) String() string {
	switch a {
	case ApproachWalk:
		return "walk"
	case ApproachFall:
		return "fall"
	case ApproachSwing:
		return "swing"
	case ApproachJump:
		return "jump"
	default:
		return "unknown"
	}
}

// ApproachInfo describes the tee's trajectory at the start of the recording.
// It is computed by examining the first N frames' positions to determine
// the velocity and movement type at which the tee crossed the start line.
type ApproachInfo struct {
	// Type classifies the approach (walk, fall, swing, jump).
	Type ApproachType
	// VelX, VelY is the average velocity (world units/tick) over the first N frames.
	VelX, VelY float64
	// LeadInX, LeadInY is a suggested position to navigate to before
	// running through the start line. Back-extrapolated from the start
	// position based on the approach type and velocity.
	LeadInX, LeadInY int
	// Inputs are the first N frames' derived inputs. The caller sends
	// these while approaching the start line to reproduce the original
	// movement (direction, jump, hook).
	Inputs []packet.PlayerInput
	// Valid is true if approach data could be computed.
	Valid bool
}

// ComputeApproach examines the first peekFrames frames to determine the
// approach trajectory. It classifies the movement type, computes velocity,
// back-extrapolates a lead-in position, and collects approach inputs.
//
// peekFrames is clamped to [2, NumFrames].
func (r *Replayer) ComputeApproach(peekFrames int) ApproachInfo {
	n := len(r.frames)
	if n < 2 || !r.hasStartPos {
		return ApproachInfo{}
	}
	if peekFrames < 2 {
		peekFrames = 2
	}
	if peekFrames > n {
		peekFrames = n
	}

	// Only works if we have position data.
	if !r.frames[0].HasPos || !r.frames[peekFrames-1].HasPos {
		return ApproachInfo{}
	}

	// Average velocity over the first peekFrames frames.
	dx := r.frames[peekFrames-1].ExpectedX - r.frames[0].ExpectedX
	dy := r.frames[peekFrames-1].ExpectedY - r.frames[0].ExpectedY
	ticks := float64(peekFrames - 1)
	velX := float64(dx) / ticks
	velY := float64(dy) / ticks

	absVX := velX
	if absVX < 0 {
		absVX = -absVX
	}
	absVY := velY
	if absVY < 0 {
		absVY = -absVY
	}

	// Detect hook usage in early frames.
	hookCount := 0
	for i := 0; i < peekFrames; i++ {
		if r.frames[i].Input.Hook == packet.HookOn {
			hookCount++
		}
	}
	hookActive := hookCount > peekFrames/3 // hook used in >33% of early frames

	// Classify approach type.
	approachType := ApproachWalk
	switch {
	case hookActive && (absVX > 1.0 || absVY > 1.0):
		approachType = ApproachSwing
	case velY > 3.0: // moving downward fast (TW: +Y = down)
		approachType = ApproachFall
	case velY < -3.0: // moving upward fast
		approachType = ApproachJump
	default:
		approachType = ApproachWalk
	}

	// Back-extrapolate a lead-in position based on approach type.
	// The number of lead-in ticks varies: walks need runway for
	// acceleration; falls/swings need height/arc distance.
	var leadInTicks float64
	switch approachType {
	case ApproachWalk:
		leadInTicks = 40 // 0.8s runway for ground acceleration
	case ApproachFall:
		leadInTicks = 25 // 0.5s — position above to fall through
	case ApproachSwing:
		leadInTicks = 35 // 0.7s — need space for hook + swing arc
	case ApproachJump:
		leadInTicks = 30 // 0.6s — position below to jump through
	}

	leadInX := r.startX - int(velX*leadInTicks)
	leadInY := r.startY - int(velY*leadInTicks)

	// Collect the first N frames' inputs.
	inputs := make([]packet.PlayerInput, peekFrames)
	for i := 0; i < peekFrames; i++ {
		inp := r.frames[i].Input
		if inp.PlayerFlags == 0 {
			inp.PlayerFlags = packet.PlayerFlagPlaying
		}
		inputs[i] = inp
	}

	return ApproachInfo{
		Type:    approachType,
		VelX:    velX,
		VelY:    velY,
		LeadInX: leadInX,
		LeadInY: leadInY,
		Inputs:  inputs,
		Valid:   true,
	}
}

// ReplayFrameCorrected returns the recording input at the given frame index,
// applying minimal drift correction. The recording's original inputs are
// preserved as much as possible since they represent the exact physics the
// original player experienced.
//
// Corrections are only applied when:
//   - The tee is stuck (X hasn't changed for 30+ frames) — uses A* pathfinding
//   - Drift is small enough to correct with direction nudges (14-200 units)
//
// For large drift (>200), the original input is returned unchanged. The
// recording's trajectory is the best guide even when positions diverge.
//
// actualX, actualY is the bot's current world position from the server.
func (r *Replayer) ReplayFrameCorrected(idx, actualX, actualY int) (packet.PlayerInput, bool) {
	if idx < 0 || idx >= len(r.frames) {
		return packet.PlayerInput{}, false
	}
	frame := r.frames[idx]
	input := frame.Input
	if input.PlayerFlags == 0 {
		input.PlayerFlags = packet.PlayerFlagPlaying
	}

	if !frame.HasPos {
		return input, true
	}

	dx := frame.ExpectedX - actualX
	dy := frame.ExpectedY - actualY
	dist := math.Sqrt(float64(dx*dx + dy*dy))

	// Within collision radius — no correction needed.
	if dist <= float64(TeeRadius) {
		return input, true
	}

	// Direction correction:
	// - Ghost NONE (dir=0): nudge toward expected position
	// - Ghost LEFT/RIGHT: use ghost's direction. Only override if the
	//   correction agrees (same direction) or ghost is NONE.
	//   NEVER reverse the ghost's direction — that breaks momentum-
	//   sensitive maneuvers (back-up before jump, arc trajectories).
	corrected := input
	if dist > float64(TeeRadius) {
		var corrDir packet.Direction
		switch {
		case dx > 4:
			corrDir = packet.DirRight
		case dx < -4:
			corrDir = packet.DirLeft
		}
		if input.Direction == 0 {
			// Ghost says NONE → nudge toward expected.
			corrected.Direction = corrDir
		} else if corrDir != 0 && corrDir == input.Direction {
			// Ghost and correction agree → reinforce.
			corrected.Direction = corrDir
		}
		// else: ghost disagrees with correction → keep ghost's direction.
	}
	// Jump is NOT corrected — the ghost's jump timing is critical
	// for clearing obstacles. Y-position corrections via jump would
	// override precisely-timed jumps (e.g., barrier double-jumps)
	// causing the tee to fail at obstacles.

	return corrected, true
}

// findResyncFrame scans ahead from the current frame to find a future frame
// where the ghost's expected position is close to the tee's actual position.
// This allows the replay to "skip ahead" when the tee and ghost have diverged
// beyond recovery (e.g., the ghost fell through a gap but the tee landed on
// solid ground nearby).
//
// Returns the resync frame index, or 0 if no suitable frame is found.
func (r *Replayer) findResyncFrame(currentIdx, actualX, actualY int) int {
	const (
		maxLookahead = 500 // scan up to 10 seconds ahead
		resyncRadius = 150 // world units — close enough to rejoin
	)

	end := min(currentIdx+maxLookahead, len(r.frames))

	bestIdx := 0
	bestDist := math.MaxFloat64

	for i := currentIdx + 10; i < end; i++ {
		f := r.frames[i]
		if !f.HasPos {
			continue
		}
		dx := float64(f.ExpectedX - actualX)
		dy := float64(f.ExpectedY - actualY)
		dist := math.Sqrt(dx*dx + dy*dy)

		if dist < bestDist {
			bestDist = dist
			bestIdx = i
		}
	}

	if bestDist <= resyncRadius {
		if r.logger != nil {
			r.logger.Info("replay: resync found",
				"from_frame", currentIdx,
				"to_frame", bestIdx,
				"skip", bestIdx-currentIdx,
				"dist", int(bestDist),
				"target", [2]int{r.frames[bestIdx].ExpectedX, r.frames[bestIdx].ExpectedY})
		}
		return bestIdx
	}

	return 0
}

// ResyncIdx returns the current resync target frame index, or 0 if no
// resync is active. The caller should skip ahead to this frame index
// when it's non-zero and greater than the current frame.
func (r *Replayer) ResyncIdx() int {
	return r.resyncIdx
}

// PeekedCharAdapter wraps a CharacterProvider and replays a previously-peeked
// frame before continuing with the underlying provider. This is useful for
// ghost files where the first frame is read to get the start position.
type PeekedCharAdapter struct {
	inner      CharacterProvider
	peeked     CharacterFrame
	hasPeeked  bool
	usedPeeked bool
}

// NewPeekedCharAdapter creates a CharacterToInputAdapter that replays the
// peeked frame first, then continues from the underlying CharacterProvider.
func NewPeekedCharAdapter(cp CharacterProvider, peeked CharacterFrame, hasPeeked bool) *CharacterToInputAdapter {
	wrapped := &PeekedCharAdapter{
		inner:     cp,
		peeked:    peeked,
		hasPeeked: hasPeeked,
	}
	return NewCharacterToInputAdapter(wrapped)
}

func (p *PeekedCharAdapter) NextCharacter() (CharacterFrame, error) {
	if p.hasPeeked && !p.usedPeeked {
		p.usedPeeked = true
		return p.peeked, nil
	}
	return p.inner.NextCharacter()
}

func (p *PeekedCharAdapter) Info() RecordingInfo {
	return p.inner.Info()
}

func (p *PeekedCharAdapter) Close() error {
	return p.inner.Close()
}
