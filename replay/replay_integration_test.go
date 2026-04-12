package replay_test

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/replay"
	"github.com/jxsl13/twclient/replay/demo"
	"github.com/jxsl13/twclient/replay/ghost"
	"github.com/jxsl13/twmap"
)

const defaultServerAddr = "localhost:8303"

// probeServer tries to connect a client to the server with a short timeout.
// If the connection succeeds, it returns the address. Otherwise the test is skipped.
// The probe client is closed before returning.
func probeServer(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("TW_TARGET")
	if addr == "" {
		addr = defaultServerAddr
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	cl := client.New(addr,
		client.WithPlayerInfo("probe", "", "default", 0),
	)
	if err := cl.Connect(ctx); err != nil {
		t.Skipf("no server on %s — skipping integration test (%v)", addr, err)
	}
	cl.Close()
	return addr
}

// testNavigator adapts client.Client to replay.Navigator for tests.
type testNavigator struct {
	cl *client.Client
}

func (n *testNavigator) CharacterPos() (int, int) {
	c := n.cl.Character()
	return c.X, c.Y
}

func (n *testNavigator) SendInput(input packet.PlayerInput) error {
	return n.cl.SendInput(input)
}

func (n *testNavigator) Map() *twmap.Map {
	return n.cl.Map()
}

func (n *testNavigator) Err() error {
	return n.cl.Err()
}

func (n *testNavigator) RaceStarted() bool {
	return n.cl.RaceTime().Active
}

// noRaceNavigator wraps a Navigator but always reports RaceStarted as false.
// This allows walkToTile to run during an active race (for recovery navigation).
type noRaceNavigator struct {
	replay.Navigator
}

func (n *noRaceNavigator) RaceStarted() bool { return false }

// waitForSnapshot blocks until the client receives at least one snapshot.
func waitForSnapshot(t *testing.T, ctx context.Context, cl *client.Client) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if cl.LastSnapTick() > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("context cancelled waiting for snapshot")
		case <-deadline:
			t.Fatal("timed out waiting for first snapshot")
		case <-tick.C:
		}
	}
}

// runReplay executes the replay loop: connect, navigate to start, then
// replay recorded inputs tick-by-tick, with drift correction and stuck recovery.
//
// The ghost file contains the exact inputs that completed the race. We replay
// them at the server's tick rate (~50 TPS). When the tee's actual position
// drifts from the expected position, the input is adjusted (direction nudge,
// jump correction). When the tee gets stuck (no movement for stuckThreshold
// ticks), A* recovery navigates to the nearest reachable ghost position, then
// input replay resumes from the corresponding frame.
//
// navTimeout controls how long to spend navigating to the start line.
// replayTimeout controls total time for the input replay loop.
// Returns true if the race was finished.
func runReplay(t *testing.T, ctx context.Context, addr string, rpl *replay.Replayer, name string, logger *slog.Logger, navTimeout, replayTimeout time.Duration) bool {
	t.Helper()

	cl := client.New(addr,
		client.WithLogger(logger),
		client.WithPlayerInfo(name, "", "default", 0),
	)
	if err := cl.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cl.Close()

	waitForSnapshot(t, ctx, cl)

	// Validate map if recording specifies one.
	info := rpl.Info()
	if info.Map != "" && cl.MapName() != info.Map {
		t.Fatalf("map mismatch: server=%q, recording=%q", cl.MapName(), info.Map)
	}

	nav := &testNavigator{cl: cl}
	if err := rpl.Attach(nav); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Phase 1: Navigate to start and trigger the race.
	if !startRace(t, ctx, cl, nav, rpl, logger, navTimeout) {
		t.Log("race did not start — aborting")
		return false
	}
	t.Log("race started!")

	// Phase 2: Replay recorded inputs tick-by-tick.
	raceCtx, raceCancel := context.WithTimeout(ctx, replayTimeout)
	defer raceCancel()
	return replayInputs(t, raceCtx, cl, nav, rpl, logger)
}

// startRace navigates to the start and triggers the race timer.
// Returns true if the race is active.
func startRace(t *testing.T, ctx context.Context, cl *client.Client, nav *testNavigator, rpl *replay.Replayer, logger *slog.Logger, navTimeout time.Duration) bool {
	t.Helper()

	startX, startY, hasStart := rpl.StartPos()
	if !hasStart {
		startX, startY = nav.CharacterPos()
	}
	approach := rpl.ComputeApproach(20)

	// Try WalkToStart with a generous timeout — it sometimes triggers the race
	// by accidentally crossing the start zone during navigation.
	walkCtx, walkCancel := context.WithTimeout(ctx, navTimeout)
	err := replay.WalkToStart(walkCtx, nav, startX, startY, approach, logger)
	walkCancel()
	if err != nil {
		t.Logf("walk to start: %v (proceeding)", err)
	}
	if cl.RaceTime().Active {
		return true
	}

	// Directional push: walk right with pulsed jumps. This reliably crosses
	// the start zone on Tutorial (the tee jumps over walls and falls through
	// the start tiles). Use a generous timeout since this can take a while.
	t.Log("directional push toward start")
	pushCtx, pushCancel := context.WithTimeout(ctx, 40*time.Second)
	defer pushCancel()
	pushTicker := time.NewTicker(20 * time.Millisecond)
	defer pushTicker.Stop()
	ticks := 0
	for {
		select {
		case <-pushCtx.Done():
			goto wiggle
		case <-pushTicker.C:
		}
		if cl.RaceTime().Active {
			return true
		}
		ticks++
		cx, cy := nav.CharacterPos()
		dir := packet.DirRight
		targetX := 100
		if cx > startX+64 {
			dir = packet.DirLeft
			targetX = -100
		}
		// Pulse jumps (not held) to allow re-trigger.
		jump := packet.JumpOff
		phase := ticks % 13
		if phase < 5 || cy > startY+32 {
			jump = packet.JumpOn
		}
		_ = nav.SendInput(packet.PlayerInput{
			Direction:   dir,
			TargetX:     targetX,
			TargetY:     -100,
			PlayerFlags: packet.PlayerFlagPlaying,
			Jump:        jump,
		})
	}

wiggle:
	if cl.RaceTime().Active {
		return true
	}

	// Last resort: wiggle with jumps over the start.
	wigCtx, wigCancel := context.WithTimeout(ctx, 10*time.Second)
	_ = replay.WiggleOverStart(wigCtx, nav, startX, startY, approach, logger)
	wigCancel()
	return cl.RaceTime().Active
}

// replayInputs replays the ghost recording using time-based frame advance
// with position-gated pausing.
//
// Core design:
//   - Frame cursor advances at 25 Hz (1 frame per 2 server ticks), matching
//     the ghost recording rate.
//   - The cursor pauses if the tee falls far behind the ghost in the ghost's
//     primary movement direction (X for horizontal segments). This prevents
//     sending jump/hook inputs for obstacles the tee hasn't reached yet.
//   - When the tee is physically stuck (no position change for 10s), A*
//     recovery navigates to the nearest reachable ghost position.
//   - Direction correction nudges the tee toward the ghost path when the
//     delta is small. Jump, hook, and fire are NEVER overridden.
func replayInputs(t *testing.T, ctx context.Context, cl *client.Client, nav *testNavigator, rpl *replay.Replayer, logger *slog.Logger) bool {
	t.Helper()

	cx, cy := nav.CharacterPos()
	frameIdx := 0
	t.Logf("replay start: pos=(%d,%d) frame=%d %s", cx, cy, frameIdx,
		formatReplayDebug(rpl, frameIdx, cx, cy, 0, 0, 0, 0))

	noRaceNav := &noRaceNavigator{nav}

	const (
		stuckTicks             = 500 // ticks without movement → recovery (10s)
		frameStallTicks        = 250 // ticks without frame progress → recovery (5s)
		recovTimeout           = 8 * time.Second
		logInterval            = 500
		skipAhead              = 100
		momentumRewindLookback = 40
		momentumRewindMaxDx    = 24
		stuckRewindLookback    = 80
		stuckRewindMaxScore    = 32
	)

	tick := 0
	subTick := 0
	lastLogTick := 0
	lastX, lastY := cx, cy
	noMoveTicks := 0
	prevObsX, prevObsY := cx, cy
	logSampleX, logSampleY := cx, cy
	logSampleTick := 0
	lastProgressFrame := frameIdx
	frameStall := 0
	lastMomentumRewindFrom := -1

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Logf("replay timeout — frame=%d/%d", frameIdx, rpl.NumFrames())
			return false
		case <-ticker.C:
		}
		tick++

		rt := cl.RaceTime()
		if rt.Finished && rt.FinishTime > 0 {
			t.Logf("RACE FINISHED! time=%v frame=%d", rt.FinishTime, frameIdx)
			return true
		}
		if err := cl.Err(); err != nil {
			t.Fatalf("connection error: %v", err)
		}

		cx, cy := nav.CharacterPos()
		actualVxTick := cx - prevObsX
		actualVyTick := cy - prevObsY
		prevObsX, prevObsY = cx, cy
		actualVxAvg, actualVyAvg := averageVelocityWindow(cx, cy, tick, logSampleX, logSampleY, logSampleTick)

		// Finished all frames — keep walking right.
		if frameIdx >= rpl.NumFrames() {
			_ = nav.SendInput(packet.PlayerInput{
				Direction:   packet.DirRight,
				TargetX:     100,
				PlayerFlags: packet.PlayerFlagPlaying,
			})
			continue
		}

		// --- Physical stuck detection ---
		adx := cx - lastX
		if adx < 0 {
			adx = -adx
		}
		ady := cy - lastY
		if ady < 0 {
			ady = -ady
		}
		if adx >= 5 || ady >= 5 {
			noMoveTicks = 0
			lastX, lastY = cx, cy
		} else {
			noMoveTicks++
		}

		if frameIdx != lastProgressFrame {
			lastProgressFrame = frameIdx
			frameStall = 0
		} else {
			frameStall++
		}

		if noMoveTicks >= stuckTicks || frameStall >= frameStallTicks {
			if frameStall >= frameStallTicks && frameIdx > 10 && lastMomentumRewindFrom != frameIdx {
				rewindIdx, rewindDx, rewindOK := findNearestPastFrameX(rpl, cx, frameIdx, momentumRewindLookback, momentumRewindMaxDx)
				if rewindOK {
					curFrame, curFrameOK := rpl.DebugFrame(frameIdx)
					rewindFrame, rewindFrameOK := rpl.DebugFrame(rewindIdx)
					curVx, _, curVOK := rpl.ExpectedVelocity(frameIdx)
					rewindVx, _, rewindVOK := rpl.ExpectedVelocity(rewindIdx)
					if curFrameOK && rewindFrameOK && curVOK && rewindVOK &&
						absInt(rewindFrame.GhostDirection) == 1 &&
						curFrame.GhostDirection == rewindFrame.GhostDirection &&
						math.Abs(curVx) >= 6 && math.Abs(rewindVx) >= 6 &&
						math.Abs(actualVxAvg) < math.Abs(curVx)*0.5 {
						t.Logf("momentum rewind: frame %d -> %d dx=%d noMove=%d frameStall=%d %s",
							frameIdx, rewindIdx, rewindDx, noMoveTicks, frameStall,
							formatReplayDebug(rpl, frameIdx, cx, cy, actualVxTick, actualVyTick, actualVxAvg, actualVyAvg))
						if rewindFrame.HasPos {
							syncCtx, syncCancel := context.WithTimeout(ctx, 3*time.Second)
							_ = replay.WalkToPosition(syncCtx, noRaceNav, rewindFrame.ExpectedX, rewindFrame.ExpectedY, logger)
							syncCancel()
							cx, cy = nav.CharacterPos()
							actualVxTick = cx - prevObsX
							actualVyTick = cy - prevObsY
							prevObsX, prevObsY = cx, cy
							actualVxAvg, actualVyAvg = averageVelocityWindow(cx, cy, tick, logSampleX, logSampleY, logSampleTick)
							t.Logf("momentum sync: pos=(%d,%d) target=(%d,%d) frame=%d %s",
								cx, cy, rewindFrame.ExpectedX, rewindFrame.ExpectedY, rewindIdx,
								formatReplayDebug(rpl, rewindIdx, cx, cy, actualVxTick, actualVyTick, actualVxAvg, actualVyAvg))
						}
						lastMomentumRewindFrom = frameIdx
						frameIdx = rewindIdx
						subTick = 0
						noMoveTicks = 0
						frameStall = 0
						lastProgressFrame = frameIdx
						lastX, lastY = cx, cy
						continue
					}
				}
			}

			if frameIdx > 10 {
				lo := frameIdx - stuckRewindLookback
				if lo < 0 {
					lo = 0
				}
				bestIdx, bestScore := findNearestFrameXY(rpl, cx, cy, lo, frameIdx)
				if bestIdx+8 < frameIdx && bestScore <= stuckRewindMaxScore {
					t.Logf("stuck rewind: frame %d -> %d pos=(%d,%d) bestScore=%d noMove=%d frameStall=%d %s",
						frameIdx, bestIdx, cx, cy, bestScore, noMoveTicks, frameStall,
						formatReplayDebug(rpl, frameIdx, cx, cy, actualVxTick, actualVyTick, actualVxAvg, actualVyAvg))
					frameIdx = bestIdx
					subTick = 0
					noMoveTicks = 0
					frameStall = 0
					lastProgressFrame = frameIdx
					lastX, lastY = cx, cy
					lastMomentumRewindFrom = -1
					continue
				}
			}

			// Physically stuck or frame-progress stuck. Try recovery.
			ex, ey, recovIdx, recovOK := rpl.FindRecoveryTarget(frameIdx, cx, cy)
			if recovOK {
				t.Logf("stuck at (%d,%d) frame=%d → recovery frame=%d (%d,%d) noMove=%d frameStall=%d %s",
					cx, cy, frameIdx, recovIdx, ex, ey, noMoveTicks, frameStall,
					formatReplayDebug(rpl, frameIdx, cx, cy, actualVxTick, actualVyTick, actualVxAvg, actualVyAvg))
				recoverCtx, recoverCancel := context.WithTimeout(ctx, recovTimeout)
				_ = replay.WalkToPosition(recoverCtx, noRaceNav, ex, ey, logger)
				recoverCancel()
				cx, cy = nav.CharacterPos()
				actualVxTick = cx - prevObsX
				actualVyTick = cy - prevObsY
				prevObsX, prevObsY = cx, cy
				actualVxAvg, actualVyAvg = averageVelocityWindow(cx, cy, tick, logSampleX, logSampleY, logSampleTick)
				// Advance frame to nearest to recovery target (forward only).
				newFrame := findNearestFrameX(rpl, cx, recovIdx, frameIdx)
				if newFrame > frameIdx {
					frameIdx = newFrame
				}
				t.Logf("recovered: pos=(%d,%d) frame=%d %s", cx, cy, frameIdx,
					formatReplayDebug(rpl, frameIdx, cx, cy, actualVxTick, actualVyTick, actualVxAvg, actualVyAvg))
			} else {
				// No target — skip ahead.
				newIdx := frameIdx + skipAhead
				if newIdx >= rpl.NumFrames() {
					newIdx = rpl.NumFrames() - 1
				}
				t.Logf("skip: frame %d → %d", frameIdx, newIdx)
				frameIdx = newIdx
			}
			noMoveTicks = 0
			frameStall = 0
			lastProgressFrame = frameIdx
			lastX, lastY = cx, cy
			lastMomentumRewindFrom = -1
			continue
		}

		// --- Frame advance: time-based at 25 Hz with X-gating ---
		subTick++
		if subTick >= 2 {
			subTick = 0
			if frameIdx+1 < rpl.NumFrames() {
				ex, _, eok := rpl.ExpectedPos(frameIdx)
				if eok {
					// Determine ghost's primary movement direction.
					// Look 5 frames ahead to average out noise.
					ghostDir := 0 // +1=right, -1=left, 0=stationary
					lookAhead := frameIdx + 5
					if lookAhead >= rpl.NumFrames() {
						lookAhead = rpl.NumFrames() - 1
					}
					if fx, _, fok := rpl.ExpectedPos(lookAhead); fok {
						ddx := fx - ex
						if ddx > 10 {
							ghostDir = 1
						} else if ddx < -10 {
							ghostDir = -1
						}
					}

					canAdvance := true
					switch ghostDir {
					case 1: // ghost moving right
						if cx < ex-160 {
							canAdvance = false // tee >200 units behind
						}
					case -1: // ghost moving left
						if cx > ex+48 {
							canAdvance = false // keep reverse segments tightly aligned
						}
					default: // stationary / wall setup
						if absInt(cx-ex) > 48 {
							canAdvance = false
						}
					}

					if canAdvance {
						frameIdx++
					}
				} else {
					frameIdx++ // no pos data, advance anyway
				}
			}
		}

		// --- Periodic log ---
		if tick-lastLogTick >= logInterval {
			ex, ey, _ := rpl.ExpectedPos(frameIdx)
			t.Logf("tick=%d frame=%d/%d pos=(%d,%d) exp=(%d,%d) %s",
				tick, frameIdx, rpl.NumFrames(), cx, cy, ex, ey,
				formatReplayDebug(rpl, frameIdx, cx, cy, actualVxTick, actualVyTick, actualVxAvg, actualVyAvg))
			lastLogTick = tick
			logSampleX, logSampleY = cx, cy
			logSampleTick = tick
		}

		// --- Send input ---
		input, ok := rpl.ReplayFrameCorrected(frameIdx, cx, cy)
		if !ok {
			input = packet.PlayerInput{PlayerFlags: packet.PlayerFlagPlaying}
		}

		if err := nav.SendInput(input); err != nil {
			logger.Warn("replay: send failed", "error", err)
		}
	}
}

// findNearestFrameX finds the frame whose expected X is closest to the
// tee's actual X, searching [minFrame, startFrame+200). This ignores Y
// to avoid getting trapped by Y-variation in jump arcs.
func findNearestFrameX(rpl *replay.Replayer, cx, startFrame, minFrame int) int {
	bestIdx := startFrame
	bestDx := int(^uint(0) >> 1)
	lo := minFrame
	hi := startFrame + 200
	if hi > rpl.NumFrames() {
		hi = rpl.NumFrames()
	}
	for i := lo; i < hi; i++ {
		ex, _, ok := rpl.ExpectedPos(i)
		if !ok {
			continue
		}
		ddx := cx - ex
		if ddx < 0 {
			ddx = -ddx
		}
		if ddx < bestDx {
			bestDx = ddx
			bestIdx = i
		}
	}
	return bestIdx
}

func findNearestPastFrameX(rpl *replay.Replayer, cx, frameIdx, lookback, maxDx int) (idx, dx int, ok bool) {
	bestIdx := -1
	bestDx := int(^uint(0) >> 1)
	lo := frameIdx - lookback
	if lo < 0 {
		lo = 0
	}
	for i := lo; i < frameIdx-6; i++ {
		ex, _, posOK := rpl.ExpectedPos(i)
		if !posOK {
			continue
		}
		ddx := absInt(cx - ex)
		if ddx < bestDx {
			bestDx = ddx
			bestIdx = i
		}
	}
	if bestIdx >= 0 && bestDx <= maxDx {
		return bestIdx, bestDx, true
	}
	return 0, 0, false
}

func findNearestFrameXY(rpl *replay.Replayer, cx, cy, startFrame, endFrame int) (idx, score int) {
	bestIdx := startFrame
	bestScore := int(^uint(0) >> 1)
	if endFrame > rpl.NumFrames() {
		endFrame = rpl.NumFrames()
	}
	for i := startFrame; i < endFrame; i++ {
		ex, ey, ok := rpl.ExpectedPos(i)
		if !ok {
			continue
		}
		s := absInt(ex-cx) + absInt(ey-cy)
		if s < bestScore {
			bestScore = s
			bestIdx = i
		}
	}
	return bestIdx, bestScore
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func averageVelocityWindow(cx, cy, tick, sampleX, sampleY, sampleTick int) (float64, float64) {
	dt := tick - sampleTick
	if dt <= 0 {
		return 0, 0
	}
	return float64(cx-sampleX) / float64(dt), float64(cy-sampleY) / float64(dt)
}

func formatReplayDebug(rpl *replay.Replayer, frameIdx, cx, cy, actualVxTick, actualVyTick int, actualVxAvg, actualVyAvg float64) string {
	frame, ok := rpl.DebugFrame(frameIdx)
	if !ok {
		return "debug=<no-frame>"
	}

	expectedVx, expectedVy, expectedVelOK := rpl.ExpectedVelocity(frameIdx)
	if !expectedVelOK {
		expectedVx = float64(frame.GhostVelX)
		expectedVy = float64(frame.GhostVelY)
	}

	deltaX := frame.ExpectedX - cx
	deltaY := frame.ExpectedY - cy
	actualSpeedTick := math.Hypot(float64(actualVxTick), float64(actualVyTick))
	actualSpeedAvg := math.Hypot(actualVxAvg, actualVyAvg)
	expectedSpeed := math.Hypot(expectedVx, expectedVy)

	return fmt.Sprintf(
		"dPos=(%d,%d) velIstTick=(%d,%d|%.1f) velIstAvg=(%.1f,%.1f|%.1f) velSoll=(%.1f,%.1f|%.1f) ghost[tick=%d rawVel=(%d,%d) rawDir=%d inputDir=%d jump=%d hook=%d fire=%d aim=(%d,%d) angle=%d weapon=%d hookState=%d hookPos=(%d,%d) attackTick=%d]",
		deltaX, deltaY,
		actualVxTick, actualVyTick, actualSpeedTick,
		actualVxAvg, actualVyAvg, actualSpeedAvg,
		expectedVx, expectedVy, expectedSpeed,
		frame.Tick,
		frame.GhostVelX, frame.GhostVelY,
		frame.GhostDirection,
		frame.Input.Direction,
		frame.Input.Jump,
		frame.Input.Hook,
		frame.Input.Fire,
		frame.Input.TargetX, frame.Input.TargetY,
		frame.GhostAngle,
		frame.GhostWeapon,
		frame.GhostHookState,
		frame.GhostHookX, frame.GhostHookY,
		frame.GhostAttackTick,
	)
}

// TestReplayGhost replays the Tutorial ghost file against a live server
// and verifies the race finishes.
//
// Automatically skipped if no DDNet server is reachable on localhost:8303
// (or TW_TARGET if set).
func TestReplayGhost(t *testing.T) {
	addr := probeServer(t)

	const ghostFile = "../testdata/Tutorial.gho"
	if _, err := os.Stat(ghostFile); os.IsNotExist(err) {
		t.Skipf("test data not found: %s", ghostFile)
	}

	g, err := ghost.Open(ghostFile)
	if err != nil {
		t.Fatalf("ghost.Open: %v", err)
	}

	firstFrame, peekErr := g.NextCharacter()
	adapter := replay.NewPeekedCharAdapter(g, firstFrame, peekErr == nil)
	rpl, err := replay.NewReplayer(adapter)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	defer rpl.Close()

	if peekErr == nil {
		rpl.SetStartPos(firstFrame.X, firstFrame.Y)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rpl.SetLogger(logger)

	info := rpl.Info()
	t.Logf("ghost: map=%s player=%s frames=%d", info.Map, info.Player, rpl.NumFrames())

	// Replay the ghost's recorded inputs with drift correction.
	// The ghost is ~144 seconds (7197 frames). Allow extra time for
	// start navigation and stuck recoveries.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	finished := runReplay(t, ctx, addr, rpl, "ghost-test", logger,
		30*time.Second, // nav timeout
		5*time.Minute,  // replay timeout (144s ghost + recovery margin)
	)

	if !finished {
		t.Fatal("tee did not reach the finish line")
	}
}

// TestReplayDemo replays the Tutorial demo file against a live server
// and verifies the race finishes.
//
// Automatically skipped if no DDNet server is reachable on localhost:8303
// (or TW_TARGET if set).
func TestReplayDemo(t *testing.T) {
	addr := probeServer(t)

	const demoFile = "../testdata/Tutorial.demo"
	if _, err := os.Stat(demoFile); os.IsNotExist(err) {
		t.Skipf("test data not found: %s", demoFile)
	}

	d, err := demo.Open(demoFile, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}

	// Tutorial.demo is a DDNet server demo with Character items but no
	// PlayerInput items. Wrap with CharacterToInputAdapter to derive inputs.
	adapter := replay.NewCharacterToInputAdapter(d)

	rpl, err := replay.NewReplayer(adapter)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	defer rpl.Close()

	// Server demos don't embed start positions. Set the Tutorial map's
	// known start area so WalkToStart can navigate there and trigger the race.
	rpl.SetStartPos(801, 2332)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rpl.SetLogger(logger)

	info := rpl.Info()
	t.Logf("demo: map=%s player=%s frames=%d", info.Map, info.Player, rpl.NumFrames())

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	finished := runReplay(t, ctx, addr, rpl, "demo-test", logger,
		30*time.Second,
		5*time.Minute,
	)

	if !finished {
		t.Fatal("tee did not reach the finish line")
	}
}

// TestReplayGhostLoadsFrames verifies that the ghost file can be loaded
// and contains expected data (offline test — no server needed).
func TestReplayGhostLoadsFrames(t *testing.T) {
	const ghostFile = "../testdata/Tutorial.gho"
	if _, err := os.Stat(ghostFile); os.IsNotExist(err) {
		t.Skipf("test data not found: %s", ghostFile)
	}

	g, err := ghost.Open(ghostFile)
	if err != nil {
		t.Fatalf("ghost.Open: %v", err)
	}

	firstFrame, err := g.NextCharacter()
	if err != nil {
		t.Fatalf("NextCharacter: %v", err)
	}

	adapter := replay.NewPeekedCharAdapter(g, firstFrame, true)
	rpl, err := replay.NewReplayer(adapter)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	defer rpl.Close()

	rpl.SetStartPos(firstFrame.X, firstFrame.Y)
	info := rpl.Info()

	t.Logf("format=%s map=%s player=%s frames=%d", info.Format, info.Map, info.Player, rpl.NumFrames())

	if rpl.NumFrames() < 100 {
		t.Errorf("expected ≥100 frames, got %d", rpl.NumFrames())
	}

	sx, sy, ok := rpl.StartPos()
	if !ok {
		t.Error("StartPos not set")
	}
	if sx == 0 && sy == 0 {
		t.Error("StartPos is (0,0)")
	}
	t.Logf("start_pos=(%d,%d)", sx, sy)

	// Verify frames have positions set (HasPos).
	input, ok := rpl.ReplayFrame(0)
	if !ok {
		t.Fatal("ReplayFrame(0) returned false")
	}
	if input.PlayerFlags == 0 {
		t.Error("frame 0 has no PlayerFlags")
	}
}

// TestReplayDemoLoadsFrames verifies that the demo file can be loaded
// and contains expected data (offline test — no server needed).
func TestReplayDemoLoadsFrames(t *testing.T) {
	const demoFile = "../testdata/Tutorial.demo"
	if _, err := os.Stat(demoFile); os.IsNotExist(err) {
		t.Skipf("test data not found: %s", demoFile)
	}

	d, err := demo.Open(demoFile, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}

	// Tutorial.demo is a DDNet server demo — use character adapter.
	adapter := replay.NewCharacterToInputAdapter(d)
	rpl, err := replay.NewReplayer(adapter)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	defer rpl.Close()

	info := rpl.Info()
	t.Logf("format=%s map=%s player=%s frames=%d", info.Format, info.Map, info.Player, rpl.NumFrames())

	if rpl.NumFrames() < 100 {
		t.Errorf("expected ≥100 frames, got %d", rpl.NumFrames())
	}
}

// TestReplayClientDemoLoadsFrames verifies that the client-recorded demo
// can be loaded and contains expected data (offline test — no server needed).
func TestReplayClientDemoLoadsFrames(t *testing.T) {
	const demoFile = "../testdata/Tutorial_client.demo"
	if _, err := os.Stat(demoFile); os.IsNotExist(err) {
		t.Skipf("test data not found: %s", demoFile)
	}

	d, err := demo.Open(demoFile, -1)
	if err != nil {
		t.Fatalf("demo.Open: %v", err)
	}

	// Client demo also uses Character items — wrap with adapter.
	adapter := replay.NewCharacterToInputAdapter(d)
	rpl, err := replay.NewReplayer(adapter)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	defer rpl.Close()

	info := rpl.Info()
	t.Logf("format=%s map=%s player=%s frames=%d cid=%d",
		info.Format, info.Map, info.Player, rpl.NumFrames(), info.SelectedCID)

	if rpl.NumFrames() < 100 {
		t.Errorf("expected ≥100 frames, got %d", rpl.NumFrames())
	}

	// Verify inputs are non-trivial (not all zeros).
	hasNonZeroDir := false
	for i := 0; i < rpl.NumFrames(); i++ {
		if f, ok := rpl.ReplayFrame(i); ok && f.Direction != 0 {
			hasNonZeroDir = true
			break
		}
	}
	if !hasNonZeroDir {
		t.Error("client demo: all frames have Direction=0, expected real inputs")
	}
}
