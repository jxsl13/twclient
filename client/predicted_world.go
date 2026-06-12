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
	baseTick int
}

// newPredictedWorld seeds a predicted world from a snapshot's character map at
// baseTick. col must be non-nil (built from the current map).
func newPredictedWorld(col *physics.Collision, tun physics.Tuning, baseTick int, chars map[int]CharacterState) *PredictedWorld {
	w := &PredictedWorld{
		col:      col,
		tun:      tun,
		cores:    make(map[int]*physics.Core, len(chars)),
		baseTick: baseTick,
	}
	for cid, ch := range chars {
		w.cores[cid] = seedCore(col, tun, ch)
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
