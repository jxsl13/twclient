package client

import (
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
)

// PredictedWorld holds a physics core per visible character, seeded from the
// latest acked snapshot and ticked forward to the predicted tick. The local
// character is driven by the buffered local inputs; other characters are
// extrapolated (see advanceOthers, T9a). This mirrors DDNet's CGameWorld /
// m_PredictedWorld (V9).
type PredictedWorld struct {
	col      *physics.Collision
	tun      physics.Tuning
	cores    map[int]*physics.Core
	seed     map[int]CharacterState
	baseTick int
}

// newPredictedWorld seeds a predicted world from a snapshot's character map at
// baseTick. col must be non-nil (built from the current map).
func newPredictedWorld(col *physics.Collision, tun physics.Tuning, baseTick int, chars map[int]CharacterState) *PredictedWorld {
	w := &PredictedWorld{
		col:      col,
		tun:      tun,
		cores:    make(map[int]*physics.Core, len(chars)),
		seed:     make(map[int]CharacterState, len(chars)),
		baseTick: baseTick,
	}
	for cid, ch := range chars {
		w.cores[cid] = seedCore(col, tun, ch)
		w.seed[cid] = ch
	}
	return w
}

// seedCore builds a physics core from a snapshot character state. Snapshot
// positions are world units; velocity is stored fixed-point (x256).
func seedCore(col *physics.Collision, tun physics.Tuning, ch CharacterState) *physics.Core {
	c := physics.NewCore(col, physics.Vec2{X: float32(ch.X), Y: float32(ch.Y)})
	c.SetTuning(tun)
	c.Vel = physics.Vec2{X: float32(ch.VelX) / 256, Y: float32(ch.VelY) / 256}
	c.Direction = ch.Direction
	c.Angle = ch.Angle
	c.Jumped = ch.Jumped
	c.HookState = ch.HookState
	return c
}

// advanceOwn re-simulates the local character from baseTick to predTick by
// replaying the buffered local inputs. Missing inputs stop the re-sim (the
// state is left at the last applied tick).
func (w *PredictedWorld) advanceOwn(localCID, predTick int, inputs *predInputBuffer) {
	core := w.cores[localCID]
	if core == nil {
		return
	}
	for tick := w.baseTick + 1; tick <= predTick; tick++ {
		in, ok := inputs.get(tick)
		if !ok {
			break
		}
		core.Step(inputToPhysics(in))
	}
}

// advanceOthers extrapolates every non-local character to predTick. With no
// inputs available for other players, DDNet reuses their last-seen intent
// (movement direction and hook); we hold that constant over the window (V9a).
// Accuracy is lower than the local re-sim and is corrected on the next
// snapshot reconcile.
func (w *PredictedWorld) advanceOthers(localCID, predTick int) {
	steps := predTick - w.baseTick
	if steps <= 0 {
		return
	}
	for cid, core := range w.cores {
		if cid == localCID {
			continue
		}
		in := extrapolatedInput(w.seed[cid])
		for i := 0; i < steps; i++ {
			core.Step(in)
		}
	}
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
