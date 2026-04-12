package replay

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twmap"
)

// Navigator provides the interface needed by WalkToStart to interact
// with a connected game client. This decouples the replay package from
// the concrete client.Client type.
type Navigator interface {
	// CharacterPos returns the current player world position.
	CharacterPos() (x, y int)
	// SendInput sends a player input to the server.
	SendInput(input packet.PlayerInput) error
	// Map returns the parsed map.
	Map() *twmap.Map
	// Err returns any connection error.
	Err() error
	// RaceStarted reports whether the race timer has started.
	RaceStarted() bool
}

// WalkToStart uses A* pathfinding on the game layer to walk the bot
// to the recording's start position, crossing it with the correct momentum.
//
// The algorithm is map-agnostic and works in four phases:
//
//  1. Lead-in: back-extrapolate a position from the start along the inverse
//     of the approach velocity. Scale the vector down until a reachable,
//     passable tile is found. Navigate there with A*.
//
//  2. Run-through: from the lead-in, send synthetic inputs that build up
//     velocity matching the approach type (direction, jump, hook). Continue
//     until the race timer starts or timeout.
//
//  3. Direct: if the race hasn't started, navigate directly to the start tile.
//
//  4. Wiggle: walk back and forth over the start position as last resort.
//
// The function returns as soon as either:
//   - The race timer starts (the bot crossed the start line)
//   - The context is cancelled or timeout expires
func WalkToStart(ctx context.Context, nav Navigator, targetWX, targetWY int, approach ApproachInfo, logger *slog.Logger) error {
	m := nav.Map()
	if m == nil {
		return fmt.Errorf("navigate: no map available")
	}

	grid := NewTileGrid(m)

	if nav.RaceStarted() {
		return nil
	}

	fromX, fromY := nav.CharacterPos()
	startTile := WorldToTile(targetWX, targetWY)

	logger.Info("WalkToStart: planning",
		"from", fmt.Sprintf("(%d,%d)", fromX, fromY),
		"start", fmt.Sprintf("(%d,%d)", targetWX, targetWY),
		"start_tile", startTile,
		"approach", approach.Type.String(),
		"vel", fmt.Sprintf("(%.1f,%.1f)", approach.VelX, approach.VelY))

	// Phase 1: Navigate to a lead-in position.
	//
	// The lead-in is back-extrapolated from the start along the inverse
	// of the approach velocity. From the lead-in the tee can build up
	// speed toward the start. We scale the lead-in vector down (100% →
	// 10%) to find the largest reachable, passable position.
	leadInReached := false
	if approach.Valid && (approach.VelX != 0 || approach.VelY != 0) {
		fromTile := WorldToTile(fromX, fromY)

		// The vector from start toward lead-in.
		fullDX := float64(approach.LeadInX - targetWX)
		fullDY := float64(approach.LeadInY - targetWY)

		for scale := 10; scale >= 1; scale-- {
			frac := float64(scale) / 10.0
			liX := targetWX + int(fullDX*frac)
			liY := targetWY + int(fullDY*frac)

			lip := WorldToTile(liX, liY)
			if grid.isSolid(lip.X, lip.Y) {
				lip = clampToPassable(grid, lip, approach.Type)
				liX, liY = TileToWorld(lip)
			}
			if grid.isSolid(lip.X, lip.Y) {
				continue
			}

			if FindPath(grid, fromTile, lip) != nil {
				logger.Info("WalkToStart: navigating to lead-in",
					"scale", fmt.Sprintf("%d%%", scale*10),
					"lead_in", fmt.Sprintf("(%d,%d)", liX, liY))

				// Limit lead-in phase to avoid consuming the entire timeout.
				liCtx, liCancel := context.WithTimeout(ctx, 3*time.Second)
				err := walkToTile(liCtx, nav, grid, lip, liX, liY, logger)
				liCancel()
				if nav.RaceStarted() {
					logger.Info("WalkToStart: race started during lead-in nav")
					return nil
				}
				if err != nil {
					logger.Info("WalkToStart: lead-in nav failed", "error", err)
				} else {
					leadInReached = true
				}
				break
			}
		}
	}

	if nav.RaceStarted() {
		return nil
	}

	// Phase 2: Run-through — build up velocity and cross the start.
	//
	// From whatever position we're at (lead-in or current), generate
	// synthetic inputs appropriate for the approach type to drive the
	// tee through the start line:
	//   Walk  → run in approach direction
	//   Fall  → run horizontally, gravity provides vertical speed
	//   Jump  → run + jump to gain upward velocity
	//   Swing → run + hook toward first recorded hook target
	//
	// If no lead-in was reached, we still try the run-through; the
	// tee might be close enough that a few ticks of running will cross
	// the start line.
	if approach.Valid {
		logger.Info("WalkToStart: starting run-through",
			"type", approach.Type.String(),
			"lead_in_reached", leadInReached)
		err := runThrough(ctx, nav, targetWX, targetWY, approach, logger)
		if nav.RaceStarted() {
			return nil
		}
		if err != nil {
			logger.Info("WalkToStart: run-through ended", "error", err)
		}
	}

	if nav.RaceStarted() {
		return nil
	}

	// Phase 3: Direct navigation to start tile.
	goalTile := startTile
	if grid.isSolid(goalTile.X, goalTile.Y) {
		goalTile.Y--
	}

	curX, curY := nav.CharacterPos()
	curTile := WorldToTile(curX, curY)
	if FindPath(grid, curTile, goalTile) != nil {
		logger.Info("WalkToStart: navigating to start tile", "goal", goalTile)
		err := walkToTile(ctx, nav, grid, goalTile, targetWX, targetWY, logger)
		if nav.RaceStarted() {
			return nil
		}
		if err != nil {
			logger.Info("WalkToStart: direct nav failed", "error", err)
		}
	}

	if nav.RaceStarted() {
		return nil
	}

	// Phase 4: Wiggle over the start position as last resort.
	logger.Info("WalkToStart: wiggling over start tile")
	return WiggleOverStart(ctx, nav, targetWX, targetWY, approach, logger)
}

// runThrough sends inputs to drive the tee through the start line.
//
// It prefers the reconstructed inputs from the first recorded frames because
// those preserve the original aim, hook timing, and jump cadence at the exact
// moment the race started. If those inputs are unavailable or exhausted, it
// falls back to a coarse synthetic approximation derived from the approach
// velocity.
//
// If the tee gets stuck (no meaningful position change for stuckLimit ticks),
// the function returns early so a fallback strategy can take over.
func runThrough(ctx context.Context, nav Navigator, targetWX, targetWY int, approach ApproachInfo, logger *slog.Logger) error {
	const timeout = 8 * time.Second
	const stuckLimit = 100 // 2s without movement → bail out
	deadline := time.After(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	// Horizontal direction from approach velocity.
	runDir := packet.DirNone
	if approach.VelX > 1.0 {
		runDir = packet.DirRight
	} else if approach.VelX < -1.0 {
		runDir = packet.DirLeft
	}

	// Aim in the approach direction.
	aimX := int(approach.VelX * 10)
	aimY := int(approach.VelY * 10)
	if aimX == 0 && aimY == 0 {
		aimX = int(runDir) * 100
	}

	// For swing approaches, extract hook target from first recorded input.
	var hookAimX, hookAimY int
	hookActive := false
	if approach.Type == ApproachSwing && len(approach.Inputs) > 0 {
		for _, inp := range approach.Inputs {
			if inp.Hook == packet.HookOn {
				hookAimX = inp.TargetX
				hookAimY = inp.TargetY
				hookActive = true
				break
			}
		}
	}

	// Use the recorded opening inputs whenever possible.
	lastRecordedInput := packet.PlayerInput{
		PlayerFlags: packet.PlayerFlagPlaying,
		Direction:   runDir,
		TargetX:     aimX,
		TargetY:     aimY,
	}
	if len(approach.Inputs) > 0 {
		lastRecordedInput = approach.Inputs[len(approach.Inputs)-1]
		if lastRecordedInput.PlayerFlags == 0 {
			lastRecordedInput.PlayerFlags = packet.PlayerFlagPlaying
		}
	}

	tick := 0
	lastX, lastY := nav.CharacterPos()
	stuckCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("runThrough: timeout (%s approach)", approach.Type)
		case <-ticker.C:
		}

		if err := nav.Err(); err != nil {
			return fmt.Errorf("runThrough: connection: %w", err)
		}

		if nav.RaceStarted() {
			logger.Info("WalkToStart: race started during run-through",
				"type", approach.Type.String(), "ticks", tick)
			return nil
		}

		// Detect if the tee is stuck (frozen or blocked).
		cx, cy := nav.CharacterPos()
		dx := cx - lastX
		if dx < 0 {
			dx = -dx
		}
		dy := cy - lastY
		if dy < 0 {
			dy = -dy
		}
		if dx < 4 && dy < 4 {
			stuckCount++
		} else {
			stuckCount = 0
			lastX, lastY = cx, cy
		}
		if stuckCount > stuckLimit {
			return fmt.Errorf("runThrough: tee stuck for %d ticks", stuckCount)
		}

		var input packet.PlayerInput
		if tick < len(approach.Inputs) {
			input = approach.Inputs[tick]
			if input.PlayerFlags == 0 {
				input.PlayerFlags = packet.PlayerFlagPlaying
			}
			lastRecordedInput = input
		} else {
			input = lastRecordedInput
			if input.PlayerFlags == 0 {
				input.PlayerFlags = packet.PlayerFlagPlaying
			}

			// If the recorded prefix did not trigger the start line,
			// continue with a conservative synthetic approximation.
			if len(approach.Inputs) == 0 {
				input.Direction = runDir
				input.TargetX = aimX
				input.TargetY = aimY
			}

			switch approach.Type {
			case ApproachJump:
				// Maintain edge-triggered jump pulses after the recorded prefix.
				if tick%2 == 0 {
					input.Jump = packet.JumpOn
				} else {
					input.Jump = packet.JumpOff
				}

			case ApproachSwing:
				if hookActive {
					input.Hook = packet.HookOn
					input.TargetX = hookAimX
					input.TargetY = hookAimY
				}
			}
		}

		if err := nav.SendInput(input); err != nil {
			logger.Warn("runThrough: send input failed", "error", err)
		}

		tick++
	}
}

// clampToPassable finds the nearest passable tile near lip.
func clampToPassable(grid *TileGrid, lip TilePos, atype ApproachType) TilePos {
	switch atype {
	case ApproachFall:
		for dy := -1; dy >= -10; dy-- {
			if !grid.isSolid(lip.X, lip.Y+dy) {
				return TilePos{lip.X, lip.Y + dy}
			}
		}
	case ApproachJump:
		for dy := 1; dy <= 10; dy++ {
			if !grid.isSolid(lip.X, lip.Y+dy) {
				return TilePos{lip.X, lip.Y + dy}
			}
		}
	default:
		for dy := -1; dy >= -5; dy-- {
			if !grid.isSolid(lip.X, lip.Y+dy) {
				return TilePos{lip.X, lip.Y + dy}
			}
		}
	}
	return lip
}

// WiggleOverStart walks the tee back and forth across the start position
// to trigger the start tile. Used as a fallback when navigating to the
// start tile didn't trigger the race (e.g., because the tee was already
// standing on it, or the start tile requires a specific approach angle).
func WiggleOverStart(ctx context.Context, nav Navigator, targetWX, targetWY int, approach ApproachInfo, logger *slog.Logger) error {
	const timeout = 8 * time.Second
	deadline := time.After(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	// Determine initial direction. If approach has velocity, use it.
	// Otherwise wiggle right first.
	dir := packet.DirRight
	if approach.Valid && approach.VelX < -1.0 {
		dir = packet.DirLeft
	}

	tick := 0
	lastDirChange := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("navigate: timeout during wiggle over start")
		case <-ticker.C:
		}

		if err := nav.Err(); err != nil {
			return fmt.Errorf("navigate: connection: %w", err)
		}

		if nav.RaceStarted() {
			logger.Info("WalkToStart: race started during wiggle", "ticks", tick)
			return nil
		}

		// Reverse direction periodically if we've gone far from start.
		cx, _ := nav.CharacterPos()
		distFromStart := cx - targetWX
		if distFromStart < 0 {
			distFromStart = -distFromStart
		}

		// Reverse if we've moved >3 tiles from start or every 50 ticks.
		if (distFromStart > 96 || tick-lastDirChange > 50) && tick > lastDirChange+10 {
			if dir == packet.DirRight {
				dir = packet.DirLeft
			} else {
				dir = packet.DirRight
			}
			lastDirChange = tick
		}

		input := packet.PlayerInput{
			PlayerFlags: packet.PlayerFlagPlaying,
			Direction:   dir,
			TargetX:     int(dir) * 100,
			Jump:        packet.JumpOn, // jump to cross start tiles above/below
		}

		if err := nav.SendInput(input); err != nil {
			logger.Warn("navigate: send input failed", "error", err)
		}

		tick++
	}
}

// WalkToPosition walks the bot to a specific world coordinate using A*.
func WalkToPosition(ctx context.Context, nav Navigator, targetX, targetY int, logger *slog.Logger) error {
	m := nav.Map()
	if m == nil {
		return fmt.Errorf("navigate: no map available")
	}

	grid := NewTileGrid(m)
	goal := WorldToTile(targetX, targetY)

	// If goal tile is solid, try the tile above (tee stands on solid).
	if grid.isSolid(goal.X, goal.Y) {
		goal.Y--
	}

	return walkToTile(ctx, nav, grid, goal, targetX, targetY, logger)
}

// TeePhysicalSize is the tee's bounding box side length in world units.
// From DDNet: CCharacterCore::PhysicalSize() = 28.0f.
const TeePhysicalSize = 28

// TeeRadius is half the tee's bounding box — the distance from center
// to edge. The tee triggers tiles when its center overlaps the tile,
// and collides with solid tiles when any corner of the 28×28 box hits.
const TeeRadius = TeePhysicalSize / 2 // 14

// walkToTile drives the bot along an A* path to the goal tile.
// goalWX/goalWY is the exact world-coordinate target (e.g. from a ghost
// file). Intermediate waypoints use tile centers; the final arrival check
// uses the exact world target. Also returns immediately if the race starts.
func walkToTile(ctx context.Context, nav Navigator, grid *TileGrid, goal TilePos, goalWX, goalWY int, logger *slog.Logger) error {
	const timeout = 30 * time.Second
	// Arrival: the tee's bounding box edge must reach the target.
	// Distance from tee center to target must be ≤ TeeRadius.
	const arrivalThreshold = TeeRadius

	deadline := time.After(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	var path []TilePos
	var lastComputedFrom TilePos
	lastComputedFrom.X = -9999 // force initial path computation

	logTick := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("navigate: timeout reaching tile (%d, %d)", goal.X, goal.Y)
		case <-ticker.C:
		}

		if err := nav.Err(); err != nil {
			return fmt.Errorf("navigate: connection: %w", err)
		}

		// If the race already started, we've crossed the start line — done.
		if nav.RaceStarted() {
			logger.Info("navigate: race started, navigation done")
			return nil
		}

		cx, cy := nav.CharacterPos()
		cur := WorldToTile(cx, cy)

		logTick++
		if logTick%25 == 0 {
			logger.Debug("navigate: progress",
				"world", fmt.Sprintf("(%d,%d)", cx, cy),
				"tile", cur,
				"goal", goal,
				"goal_center", fmt.Sprintf("(%d,%d)", goalWX, goalWY))
		}

		// Check arrival using world coordinates against tile center.
		dwx := cx - goalWX
		dwy := cy - goalWY
		if dwx < 0 {
			dwx = -dwx
		}
		if dwy < 0 {
			dwy = -dwy
		}
		if dwx <= arrivalThreshold && dwy <= arrivalThreshold {
			logger.Info("navigate: reached goal tile center",
				"world", fmt.Sprintf("(%d,%d)", cx, cy),
				"tile", cur,
				"goal", goal,
				"goal_center", fmt.Sprintf("(%d,%d)", goalWX, goalWY))
			return nil
		}

		// Recompute A* path if tile position changed.
		if cur != lastComputedFrom {
			path = FindPath(grid, cur, goal)
			if path == nil {
				return fmt.Errorf("navigate: no path from (%d,%d) to (%d,%d)", cur.X, cur.Y, goal.X, goal.Y)
			}
			lastComputedFrom = cur
			logger.Debug("navigate: path recomputed", "from", cur, "to", goal, "steps", len(path))
		}

		// Find current position in path and pick next waypoint.
		nextIdx := 1
		for i, p := range path {
			if p == cur && i+1 < len(path) {
				nextIdx = i + 1
				break
			}
		}
		if nextIdx >= len(path) {
			nextIdx = len(path) - 1
		}

		// Target: use world-coordinate center of next tile for sub-tile steering.
		next := path[nextIdx]
		nextWX, nextWY := TileToWorld(next)

		var input packet.PlayerInput
		input.PlayerFlags = packet.PlayerFlagPlaying

		// When the next waypoint is significantly below us and our bounding
		// box might be resting on an adjacent solid tile, steer toward the
		// nearest drop edge rather than directly at the goal X. This prevents
		// oscillation when the A* expects us to fall but our physical-size
		// bounding box straddles a solid tile edge.
		onGround := grid.isSolid(cur.X, cur.Y+1) ||
			grid.isSolid(cur.X-1, cur.Y+1) ||
			grid.isSolid(cur.X+1, cur.Y+1)

		needsFall := next.Y > cur.Y+1 && onGround

		steered := false
		if needsFall {
			// Find the nearest position where we can actually fall.
			// Check left and right edges for a gap in the ground.
			dropX := 0
			dropFound := false
			for dist := 0; dist <= 6; dist++ {
				for _, dx := range []int{-dist, dist} {
					tx := cur.X + dx
					if tx < 0 || tx >= grid.W || grid.isSolid(tx, cur.Y) {
						continue
					}
					// Can fall here: must have air below for the full tee width.
					canFall := !grid.isSolid(tx, cur.Y+1) &&
						!grid.isSolid(tx-1, cur.Y+1) &&
						!grid.isSolid(tx+1, cur.Y+1)
					if canFall {
						dropX = tx
						dropFound = true
						break
					}
				}
				if dropFound {
					break
				}
			}

			if dropFound {
				dropWX, _ := TileToWorld(TilePos{dropX, cur.Y})
				if cx < dropWX-4 {
					input.Direction = packet.DirRight
					input.TargetX = 100
				} else if cx > dropWX+4 {
					input.Direction = packet.DirLeft
					input.TargetX = -100
				}
				steered = true
			}
		}

		if !steered {
			// Steer toward the center of the next waypoint tile.
			if cx < nextWX-4 {
				input.Direction = packet.DirRight
				input.TargetX = 100
			} else if cx > nextWX+4 {
				input.Direction = packet.DirLeft
				input.TargetX = -100
			}

			// Determine vertical delta to next waypoint.
			vertDelta := cy - nextWY // positive = need to go up
			horzDelta := nextWX - cx
			if horzDelta < 0 {
				horzDelta = -horzDelta
			}

			needsHook := vertDelta > 5*int(twmap.TileSize) || // >5 tiles up — beyond jump
				(vertDelta > 2*int(twmap.TileSize) && horzDelta > 3*int(twmap.TileSize)) // far diagonal

			if needsHook {
				// Find nearest hookable solid tile above/toward the waypoint.
				hookX, hookY, found := findHookTarget(grid, cur, path[nextIdx])
				if found {
					// Aim at the hookable tile's center.
					hwx, hwy := TileToWorld(TilePos{hookX, hookY})
					input.TargetX = hwx - cx
					input.TargetY = hwy - cy
					input.Hook = packet.HookOn
				}
				// Also jump to gain initial height.
				input.Jump = packet.JumpOn
			} else if nextWY < cy-4 {
				// Simple jump — next waypoint is above us.
				input.Jump = packet.JumpOn
			}
		}

		if err := nav.SendInput(input); err != nil {
			logger.Warn("navigate: send input failed", "error", err)
		}
	}
}

// findHookTarget finds the best solid tile to hook onto when trying to
// reach the goal tile from the current position. Prefers tiles that are
// above and between cur and goal, with line-of-sight.
func findHookTarget(grid *TileGrid, cur, goal TilePos) (int, int, bool) {
	const maxRange = 10
	bestDist := -1
	var bestX, bestY int
	found := false

	// Prefer hooking above us and toward the goal.
	for dy := -maxRange; dy <= 2; dy++ {
		for dx := -maxRange; dx <= maxRange; dx++ {
			tx, ty := cur.X+dx, cur.Y+dy
			dist := dx*dx + dy*dy
			if dist > maxRange*maxRange || dist < 2 {
				continue
			}
			if !grid.isSolid(tx, ty) {
				continue
			}
			if grid.At(tx, ty) == twmap.TileUnhookable {
				continue
			}
			if !hasLineOfSight(grid, cur.X, cur.Y, tx, ty) {
				continue
			}
			// Score: prefer tiles above us and toward the goal.
			goalDx := goal.X - tx
			goalDy := goal.Y - ty
			score := goalDx*goalDx + goalDy*goalDy + dy*dy
			if !found || score < bestDist {
				bestDist = score
				bestX, bestY = tx, ty
				found = true
			}
		}
	}
	return bestX, bestY, found
}

// FindNearestTile scans the grid for the tile ID closest to origin.
func FindNearestTile(grid *TileGrid, origin TilePos, tileID uint8) (TilePos, bool) {
	bestDist := -1
	var best TilePos
	for y := 0; y < grid.H; y++ {
		for x := 0; x < grid.W; x++ {
			if grid.At(x, y) == tileID {
				dx := x - origin.X
				dy := y - origin.Y
				dist := dx*dx + dy*dy
				if bestDist < 0 || dist < bestDist {
					bestDist = dist
					best = TilePos{x, y}
				}
			}
		}
	}
	return best, bestDist >= 0
}

// findWalkableNearTile finds the closest reachable tile that overlaps with
// or is adjacent to a tile of the given type. It prefers:
//  1. The target tile itself (if passable and A*-reachable)
//  2. Tiles with solid ground below (stable standing position)
//  3. Other passable tiles near the target
func findWalkableNearTile(grid *TileGrid, origin TilePos, tileID uint8) (TilePos, bool) {
	type candidate struct {
		pos      TilePos
		dist     int
		priority int // 0 = is target tile, 1 = grounded, 2 = floating
	}

	var best *candidate

	for y := 0; y < grid.H; y++ {
		for x := 0; x < grid.W; x++ {
			if grid.At(x, y) != tileID {
				continue
			}
			// Check the tile itself and neighbors (±2 in each direction).
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					tx, ty := x+dx, y+dy
					if tx < 0 || ty < 0 || tx >= grid.W || ty >= grid.H {
						continue
					}
					if !grid.isPassable(tx, ty) {
						continue
					}

					ddx := tx - origin.X
					ddy := ty - origin.Y
					dist := ddx*ddx + ddy*ddy

					// Determine priority.
					prio := 2 // floating
					if grid.At(tx, ty) == tileID {
						prio = 0 // is target tile — best priority
					} else if ty+1 < grid.H && grid.isSolid(tx, ty+1) {
						prio = 1 // grounded
					}

					if best == nil || prio < best.priority ||
						(prio == best.priority && dist < best.dist) {
						best = &candidate{TilePos{tx, ty}, dist, prio}
					}
				}
			}
		}
	}

	if best != nil {
		return best.pos, true
	}
	return TilePos{}, false
}
