package client

import (
	"maps"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/physics"
)

// LaserState is a visible laser beam.
type LaserState struct {
	X, Y, FromX, FromY, StartTick int
}

// PickupState is a visible pickup entity.
type PickupState struct {
	X, Y, Type, Subtype int
}

// FlagState is a visible CTF flag.
type FlagState struct {
	X, Y, Team int
}

// TickState is the COMPLETE observable + predicted game state for one tick
// (V19), self-contained for any consumer (UI render, ML observation). It is
// built by buildTickState; in frame cadence IntraTick∈[0,1) and positions are
// smoothed, in fixed cadence IntraTick==0 (V24).
type TickState struct {
	Tick      int
	IntraTick float32

	LocalID     int
	Players     map[int]CharacterState   // predicted, all visible client ids
	Projectiles []packet.ProjectileState // predicted ballistics
	Lasers      []LaserState
	Pickups     []PickupState
	Flags       []FlagState

	Map          *MapView
	Tuning       physics.Tuning // default (zone-0) server tuning
	ActiveTuning physics.Tuning // tuning at the local player's tile (V29)
	SelfTuneZone int

	GameInfo GameInfoState
	RaceTime RaceTime

	// Roster is the in-session player registry for this tick (id → name/clan/
	// team/score/present), so a UI renders a scoreboard without touching
	// snapshots (V98). Empty until ClientInfo/score events arrive.
	Roster map[int]PlayerState

	Events []packet.Event // server events since the previous tick
}

// buildTickState assembles the canonical TickState for the current tick
// (IntraTick=0). This is the single state-assembly path; frame cadence overlays
// smoothing on top of it (V24).
func (c *Client) buildTickState() TickState {
	// Snapshot-derived entities + scalars under a short read lock.
	c.mu.RLock()
	tick := c.snap.lastTick
	localID := c.snap.localCID
	gameInfo := c.snap.gameInfo
	raceTime := c.snap.raceTimeState()
	snap := c.snap.lastSnap
	defaultTun := c.predTun
	var roster map[int]PlayerState
	if len(c.players) > 0 {
		roster = make(map[int]PlayerState, len(c.players))
		maps.Copy(roster, c.players)
	}
	c.mu.RUnlock()

	var lasers []LaserState
	var pickups []PickupState
	var flags []FlagState
	if snap != nil {
		for _, it := range snap.Items {
			f := it.Fields
			switch it.TypeID {
			case net6.ObjLaser:
				if len(f) >= net6.SizeLaser {
					lasers = append(lasers, LaserState{X: f[0], Y: f[1], FromX: f[2], FromY: f[3], StartTick: f[4]})
				}
			case net6.ObjPickup:
				if len(f) >= net6.SizePickup {
					pickups = append(pickups, PickupState{X: f[0], Y: f[1], Type: f[2], Subtype: f[3]})
				}
			case net6.ObjFlag:
				if len(f) >= net6.SizeFlag {
					flags = append(flags, FlagState{X: f[0], Y: f[1], Team: f[2]})
				}
			}
		}
	}

	// Predicted accessors take their own locks — call them outside the lock above.
	players := c.PredictedCharacters()

	selfZone := 0
	if mv := c.MapView(); mv != nil {
		if ch, ok := players[localID]; ok {
			selfZone = mv.TuneZone(ch.X/physics.TileSize, ch.Y/physics.TileSize)
		}
	}

	return TickState{
		Tick:         tick,
		IntraTick:    0,
		LocalID:      localID,
		Players:      players,
		Projectiles:  c.PredictedProjectiles(),
		Lasers:       lasers,
		Pickups:      pickups,
		Flags:        flags,
		Map:          c.MapView(),
		Tuning:       defaultTun,
		ActiveTuning: c.ActiveTuning(),
		SelfTuneZone: selfZone,
		GameInfo:     gameInfo,
		RaceTime:     raceTime,
		Roster:       roster,
		Events:       c.drainTickEvents(),
	}
}

// drainTickEvents returns and clears the events accumulated since the last call.
func (c *Client) drainTickEvents() []packet.Event {
	c.mu.Lock()
	evs := c.tickEvents
	c.tickEvents = nil
	c.mu.Unlock()
	return evs
}
