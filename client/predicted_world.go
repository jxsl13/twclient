package client

import (
	"maps"
	"sort"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
)

// PredictedWorld holds a physics core per visible character, seeded from the
// latest acked snapshot and ticked forward to the predicted tick. The local
// character is driven by the buffered local inputs; other characters are
// extrapolated (see advance, V9a). This mirrors DDNet's CGameWorld /
// m_PredictedWorld (V9).
type PredictedWorld struct {
	col      *physics.Collision
	tun      physics.Tuning
	cfg      physics.WorldConfig
	cores    map[int]*physics.Core
	seed     map[int]CharacterState
	baseTick int
	// tuneAt resolves per-tile tuning (DDNet tune zones, V29). When set, each
	// core's tuning is updated to its current tile before every Step.
	tuneAt func(tileX, tileY int) physics.Tuning
}

// applyZoneTuning sets the core's tuning to the tune-zone at its current tile.
func (w *PredictedWorld) applyZoneTuning(core *physics.Core) {
	if w.tuneAt == nil {
		return
	}
	core.SetTuning(w.tuneAt(int(core.Pos.X)/physics.TileSize, int(core.Pos.Y)/physics.TileSize))
}

// newPredictedWorld seeds a predicted world from a snapshot's character map at
// baseTick. col must be non-nil (built from the current map).
func newPredictedWorld(col *physics.Collision, tun physics.Tuning, cfg physics.WorldConfig, baseTick int, chars map[int]CharacterState) *PredictedWorld {
	w := &PredictedWorld{
		col:      col,
		tun:      tun,
		cfg:      cfg,
		cores:    make(map[int]*physics.Core, len(chars)),
		seed:     make(map[int]CharacterState, len(chars)),
		baseTick: baseTick,
	}
	for cid, ch := range chars {
		w.cores[cid] = seedCore(col, tun, cfg, ch)
		w.seed[cid] = ch
	}
	return w
}

// seedCore builds a physics core from a snapshot character state. Snapshot
// positions are world units; velocity is stored fixed-point (x256).
func seedCore(col *physics.Collision, tun physics.Tuning, cfg physics.WorldConfig, ch CharacterState) *physics.Core {
	c := physics.NewCore(col, physics.Vec2{X: float32(ch.X), Y: float32(ch.Y)})
	c.SetTuning(tun)
	c.SetWorldConfig(cfg)
	c.Vel = physics.Vec2{X: float32(ch.VelX) / 256, Y: float32(ch.VelY) / 256}
	c.Direction = ch.Direction
	c.Angle = ch.Angle
	c.Jumped = ch.Jumped
	c.HookState = ch.HookState
	return c
}

// advance re-simulates the predicted world from baseTick to predTick as ONE
// lockstep whole-world tick (physics.WorldStep), so tee↔tee collision (T199)
// and hook-drag (T204) act on the real prediction, not just the golden harness
// (V21). The local character replays its buffered inputs; every other character
// holds its last-seen intent constant over the window (V9a). A missing local
// input STOPS the entire advance at the last fully-driven tick (V9) — others
// never run past the local. With antiping off only the local core is advanced;
// others stay at the seed and are returned raw (V11).
//
// WorldStep indexes peers (and physics HookedPlayer) by slice position, so the
// core slice order is fixed once (sorted by client ID) and reused every tick.
func (w *PredictedWorld) advance(localCID, predTick int, inputs *predInputBuffer, antiping bool) {
	cids := w.orderedCIDs(localCID, antiping)
	if len(cids) == 0 {
		return
	}
	cores := make([]*physics.Core, len(cids))
	localIdx := -1
	for i, cid := range cids {
		cores[i] = w.cores[cid]
		if cid == localCID {
			localIdx = i
		}
	}

	ins := make([]physics.Input, len(cores))
	for tick := w.baseTick + 1; tick <= predTick; tick++ {
		var localIn physics.Input
		if localIdx >= 0 {
			netIn, ok := inputs.get(tick)
			if !ok {
				break // missing local input stops the whole advance (V9)
			}
			localIn = inputToPhysics(netIn)
		}
		for i, cid := range cids {
			if i == localIdx {
				ins[i] = localIn
			} else {
				ins[i] = extrapolatedInput(w.seed[cid])
			}
			w.applyZoneTuning(cores[i])
		}
		physics.WorldStep(cores, ins)
	}
}

// orderedCIDs returns the client IDs to advance, sorted ascending for a stable
// WorldStep peer order. With antiping off only the local character is predicted
// (others stay raw, V11); with it on, every character in the world participates.
func (w *PredictedWorld) orderedCIDs(localCID int, antiping bool) []int {
	if !antiping {
		if _, ok := w.cores[localCID]; ok {
			return []int{localCID}
		}
		return nil
	}
	cids := make([]int, 0, len(w.cores))
	for cid := range w.cores {
		cids = append(cids, cid)
	}
	sort.Ints(cids)
	return cids
}

// extrapolatedInput reconstructs a held input from a character's last-seen
// state: keep walking in the same direction and keep hooking if the hook was
// active.
func extrapolatedInput(ch CharacterState) physics.Input {
	hooking := ch.HookState == physics.HookFlying || ch.HookState == physics.HookGrabbed
	return physics.Input{
		Direction: ch.Direction,
		TargetX:   ch.HookDx,
		TargetY:   ch.HookDy,
		Hook:      hooking,
	}
}

// characters returns the predicted state for every character in the world.
func (w *PredictedWorld) characters() map[int]CharacterState {
	out := make(map[int]CharacterState, len(w.cores))
	for cid := range w.cores {
		if st, ok := w.character(cid); ok {
			out[cid] = st
		}
	}
	return out
}

// character returns the predicted character state for cid, or false if the
// world has no core for it.
func (w *PredictedWorld) character(cid int) (CharacterState, bool) {
	core, ok := w.cores[cid]
	if !ok {
		return CharacterState{}, false
	}
	x, y := core.QuantizedPos()
	return CharacterState{
		X:         x,
		Y:         y,
		VelX:      int(core.Vel.X * 256),
		VelY:      int(core.Vel.Y * 256),
		Direction: core.Direction,
		Angle:     core.Angle,
		Jumped:    core.Jumped,
		HookState: core.HookState,
	}, true
}

// reconcilePrediction rebuilds the predicted world from the latest acked
// snapshot and re-simulates forward to the predicted tick (V10). It mirrors
// DDNet copying the snapshot world into the predicted world each frame: the
// prediction always starts from authoritative state, so errors never
// accumulate across snapshots (V9).
func (c *Client) reconcilePrediction() {
	if !c.predictEnabled {
		return
	}

	// Build the map collision lazily once the map is available, and derive the
	// world physics config (vanilla vs DDRace) from the map at the same time
	// (V10b). Both depend only on the static map, so this happens once.
	if c.predCol == nil {
		if m := c.Map(); m != nil {
			col := physics.NewCollision(m)
			cfg := physics.DefaultWorldConfig()
			if mv := c.MapView(); mv != nil && mv.IsDDRace() {
				cfg = physics.DDRaceWorldConfig()
			}
			c.mu.Lock()
			c.predCol = col
			c.predCfg = cfg
			c.mu.Unlock()
		}
	}

	predTick := c.predTime.PredTick()

	mv := c.MapView()

	c.mu.Lock()
	col := c.predCol
	cfg := c.predCfg
	tun := c.predTun
	chars := c.snap.charactersCopy()
	base := c.snap.lastTick
	local := c.snap.localCID
	antiping := c.antiping
	// snapshot per-zone tunings so the resolver needs no per-tick locking.
	var zoneTun map[int]physics.Tuning
	if mv != nil && len(c.tunings) > 0 {
		zoneTun = make(map[int]physics.Tuning, len(c.tunings))
		maps.Copy(zoneTun, c.tunings)
	}
	c.mu.Unlock()

	if col == nil || predTick <= 0 {
		return
	}

	w := newPredictedWorld(col, tun, cfg, base, chars)
	// Per-tile tuning via tune zones (V29), only when the map has tune data.
	if mv != nil && zoneTun != nil {
		w.tuneAt = func(tileX, tileY int) physics.Tuning {
			if t, ok := zoneTun[mv.TuneZone(tileX, tileY)]; ok {
				return t
			}
			return tun
		}
	}
	w.advance(local, predTick, &c.predInputs, antiping)

	c.mu.Lock()
	c.prevPredWorld = c.predWorld
	c.predWorld = w
	c.mu.Unlock()
}

// SmoothedCharacters returns predicted characters with positions interpolated
// between the previous and current predicted worlds by intra∈[0,1], for
// teleport-free rendering between ticks (V21). intra=0 yields the previous
// tick, intra=1 the current. Non-position fields come from the current world.
func (c *Client) SmoothedCharacters(intra float32) map[int]CharacterState {
	if intra < 0 {
		intra = 0
	} else if intra > 1 {
		intra = 1
	}
	c.mu.RLock()
	cur := c.predWorld
	prev := c.prevPredWorld
	c.mu.RUnlock()

	if cur == nil {
		return c.PredictedCharacters()
	}
	out := cur.characters()
	if prev == nil {
		return out
	}
	for cid, ch := range out {
		p, ok := prev.character(cid)
		if !ok {
			continue
		}
		ch.X = p.X + int(float32(ch.X-p.X)*intra)
		ch.Y = p.Y + int(float32(ch.Y-p.Y)*intra)
		out[cid] = ch
	}
	return out
}

// PredictedCharacter returns the predicted local character state. With
// prediction disabled it equals Character() (V11).
func (c *Client) PredictedCharacter() CharacterState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.predictEnabled && c.predWorld != nil {
		if st, ok := c.predWorld.character(c.snap.localCID); ok {
			return st
		}
	}
	return c.snap.character
}

// PredictedCharacters returns the predicted state of every visible character.
// With antiping enabled all characters are predicted; with only base
// prediction enabled, the local character is predicted and others are raw;
// with prediction disabled all are raw snapshot state (V11).
func (c *Client) PredictedCharacters() map[int]CharacterState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	base := c.snap.charactersCopy()
	if c.predictEnabled && c.predWorld != nil {
		if c.antiping {
			return c.predWorld.characters()
		}
		if st, ok := c.predWorld.character(c.snap.localCID); ok {
			base[c.snap.localCID] = st
		}
	}
	return base
}

// RangePlayers calls fn for each player in the latest snapshot, keyed by client
// ID, and stops early if fn returns false. It holds the read lock for the
// duration and yields each CharacterState BY VALUE without allocating a result
// map — a zero-allocation alternative to PredictedCharacters for read-only
// loops (e.g. scanning all players each tick). The states are RAW snapshot
// values (prediction-off semantics); use PredictedCharacters when you need the
// predicted local/antiping positions. fn must not retain references beyond the
// call and must not call back into the Client (it runs under the read lock).
func (c *Client) RangePlayers(fn func(id int, ch CharacterState) bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for id, ch := range c.snap.characters {
		if !fn(id, ch) {
			return
		}
	}
}

// PredictedProjectiles returns every projectile from the latest snapshot with
// its position advanced to the predicted tick using the ballistic model
// (V9, B2). With prediction disabled the positions are the snapshot's current
// positions (advanced to the latest received tick).
func (c *Client) PredictedProjectiles() []packet.ProjectileState {
	c.mu.RLock()
	tun := c.predTun
	enabled := c.predictEnabled
	lastTick := c.snap.lastTick
	projs := make([]packet.ProjectileState, 0, len(c.snap.projectiles))
	for _, p := range c.snap.projectiles {
		projs = append(projs, p)
	}
	c.mu.RUnlock()

	targetTick := lastTick
	if enabled {
		if pt := c.predTime.PredTick(); pt > 0 {
			targetTick = pt
		}
	}

	out := make([]packet.ProjectileState, 0, len(projs))
	for _, p := range projs {
		t := float32(targetTick-p.StartTick) / physics.TickSpeed
		if t < 0 {
			t = 0
		}
		dir := physics.Vec2{X: float32(p.VelX) / 100, Y: float32(p.VelY) / 100}
		pos := tun.ProjectilePos(physics.Vec2{X: float32(p.X), Y: float32(p.Y)}, dir, int(p.Type), t)
		pp := p
		pp.X = int(pos.X)
		pp.Y = int(pos.Y)
		out = append(out, pp)
	}
	return out
}

// inputToPhysics converts a network player input into the physics tick input.
// FireGrenade is set when the fire counter is in the pressed state (odd) while
// the grenade is the wanted weapon, matching the server's rocket-jump impulse.
func inputToPhysics(in packet.PlayerInput) physics.Input {
	return physics.Input{
		Direction:   int(in.Direction),
		TargetX:     in.TargetX,
		TargetY:     in.TargetY,
		Jump:        in.Jump == packet.JumpOn,
		Hook:        in.Hook == packet.HookOn,
		FireGrenade: int(in.Fire)%2 == 1 && in.WantedWeapon == packet.WeaponGrenade,
	}
}
