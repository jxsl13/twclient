package client

import (
	"time"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/packet"
)

// GameStateFlags from obj_game_info.
const (
	GameStateFlagGameOver    = 1
	GameStateFlagSuddenDeath = 2
	GameStateFlagPaused      = 4
	GameStateFlagRaceTime    = 8
)

// GameInfoState holds the decoded state from an obj_game_info snap item.
type GameInfoState struct {
	GameFlags      int
	GameStateFlags int
	RoundStartTick int
	WarmupTimer    int // Negative when RACETIME is set; race_ticks = gameTick + WarmupTimer
	ScoreLimit     int
	TimeLimit      int
	RoundNum       int
	RoundCurrent   int
}

// RaceTime tracks the current race time as precisely as possible.
type RaceTime struct {
	Active         bool          // Whether a race is currently running (RACETIME flag set)
	TickBased      time.Duration // Race time derived from snapshot ticks (20ms resolution)
	WallClock      time.Duration // Race time interpolated via client wall-clock (sub-ms resolution)
	StartedAt      time.Time     // Wall-clock time when race was first detected
	TickAtStart    int           // Game tick when race was first detected
	CurrentTick    int           // Latest game tick
	Finished       bool          // Whether the race was finished
	FinishTime     time.Duration // Finish time (centisecond precision, from DDRaceTime msg)
	CheckpointDiff time.Duration // Last checkpoint time diff (negative = faster)
}

// CharacterState holds the decoded state from an obj_character snap item.
type CharacterState struct {
	Tick         int
	X            int
	Y            int
	VelX         int
	VelY         int
	Angle        int
	Direction    int
	Jumped       int
	HookedPlayer int
	HookState    int
	HookTick     int
	HookX        int
	HookY        int
	HookDx       int
	HookDy       int
	PlayerFlags  int
	Health       int
	Armor        int
	AmmoCount    int
	Weapon       int
	Emote        int
	AttackTick   int
}

func characterFromFields(fields []int) CharacterState {
	if len(fields) < net6.SizeCharacter {
		return CharacterState{}
	}
	return CharacterState{
		Tick:         fields[0],
		X:            fields[1],
		Y:            fields[2],
		VelX:         fields[3],
		VelY:         fields[4],
		Angle:        fields[5],
		Direction:    fields[6],
		Jumped:       fields[7],
		HookedPlayer: fields[8],
		HookState:    fields[9],
		HookTick:     fields[10],
		HookX:        fields[11],
		HookY:        fields[12],
		HookDx:       fields[13],
		HookDy:       fields[14],
		PlayerFlags:  fields[15],
		Health:       fields[16],
		Armor:        fields[17],
		AmmoCount:    fields[18],
		Weapon:       fields[19],
		Emote:        fields[20],
		AttackTick:   fields[21],
	}
}

// SnapStorage tracks game state extracted from parsed snapshots.
// It is embedded in Client and accessed under Client.mu.
// Delta decompression is handled by the session; SnapStorage only
// interprets the fully decoded items.
type SnapStorage struct {
	lastTick     int
	lastSnapTime time.Time
	localCID     int
	character    CharacterState
	// characters holds every player's character state from the latest
	// snapshot; prevCharacters holds the previous snapshot's map. Both are
	// keyed by client ID. Snap-derived events diff these (V12).
	characters     map[int]CharacterState
	prevCharacters map[int]CharacterState
	gameInfo       GameInfoState
	raceTime       RaceTime
}

// charactersCopy returns a shallow copy of the latest per-client character
// map. Caller must hold the Client mutex.
func (ss *SnapStorage) charactersCopy() map[int]CharacterState {
	out := make(map[int]CharacterState, len(ss.characters))
	for id, c := range ss.characters {
		out[id] = c
	}
	return out
}

// deriveEvents diffs the latest snapshot's character map against the previous
// one and returns the snap-derived events to dispatch. Caller must hold the
// Client mutex; the returned events are dispatched after it is released (V2).
//
// Covers the core snap-derived events (V5, V13): presence (enter/leave sight),
// someone hooking the local tee, and the server changing the local weapon.
func (ss *SnapStorage) deriveEvents() []packet.Event {
	cur := ss.characters
	prev := ss.prevCharacters
	if len(cur) == 0 && len(prev) == 0 {
		return nil
	}

	var evs []packet.Event

	// Presence: enter/leave sight (edge-triggered on set membership).
	for id := range cur {
		if _, ok := prev[id]; !ok {
			evs = append(evs, packet.EventPlayerEnterSight{ClientID: id})
		}
	}
	for id := range prev {
		if _, ok := cur[id]; !ok {
			evs = append(evs, packet.EventPlayerLeaveSight{ClientID: id})
		}
	}

	// Someone hooks the local character: another player's HookedPlayer
	// transitions to localCID this snapshot.
	for id, c := range cur {
		if id == ss.localCID || c.HookedPlayer != ss.localCID {
			continue
		}
		if p, ok := prev[id]; ok && p.HookedPlayer == ss.localCID {
			continue // already hooking us last snap — edge already fired
		}
		evs = append(evs, packet.EventHookedBy{ClientID: id})
	}

	// Server changed the local player's weapon.
	if local, ok := cur[ss.localCID]; ok {
		if p, ok := prev[ss.localCID]; ok && p.Weapon != local.Weapon {
			evs = append(evs, packet.EventWeaponChange{Weapon: packet.Weapon(local.Weapon)})
		}
	}

	return evs
}

func (ss *SnapStorage) raceTimeState() RaceTime {
	rt := ss.raceTime
	if rt.Active && !rt.Finished && !rt.StartedAt.IsZero() {
		rt.WallClock = time.Since(rt.StartedAt)
	}
	return rt
}

func (ss *SnapStorage) setDDRaceTime(timeCentis, checkCentis int, finish bool) {
	ss.raceTime.TickBased = time.Duration(timeCentis) * 10 * time.Millisecond
	if finish {
		ss.raceTime.Finished = true
		ss.raceTime.FinishTime = time.Duration(timeCentis) * 10 * time.Millisecond
	}
	ss.raceTime.CheckpointDiff = time.Duration(checkCentis) * 10 * time.Millisecond
}

func (ss *SnapStorage) processSnapshot(snap *packet.Snapshot) {
	ss.lastTick = snap.Tick
	ss.lastSnapTime = time.Now()
	ss.updateFromSnap(snap)
}

const tickDuration = 20 * time.Millisecond

func (ss *SnapStorage) updateFromSnap(snap *packet.Snapshot) {
	for _, item := range snap.Items {
		if item.TypeID == net6.ObjPlayerInfo && len(item.Fields) >= net6.SizePlayerInfo {
			if item.Fields[0] != 0 {
				ss.localCID = item.Fields[1]
			}
		}
	}
	for _, item := range snap.Items {
		if item.TypeID == net6.ObjGameInfo && len(item.Fields) >= net6.SizeGameInfo {
			ss.gameInfo = GameInfoState{
				GameFlags:      item.Fields[0],
				GameStateFlags: item.Fields[1],
				RoundStartTick: item.Fields[2],
				WarmupTimer:    item.Fields[3],
				ScoreLimit:     item.Fields[4],
				TimeLimit:      item.Fields[5],
				RoundNum:       item.Fields[6],
				RoundCurrent:   item.Fields[7],
			}
			ss.updateRaceTime(snap.Tick)
		}
	}
	// Build the per-client character map for this snapshot, rotating the
	// previous map into prevCharacters so snap-derived events can diff them.
	newChars := make(map[int]CharacterState)
	for _, item := range snap.Items {
		if item.TypeID == net6.ObjCharacter {
			newChars[item.ID] = characterFromFields(item.Fields)
		}
	}
	ss.prevCharacters = ss.characters
	ss.characters = newChars
	if local, ok := newChars[ss.localCID]; ok {
		ss.character = local
	}
}

func (ss *SnapStorage) updateRaceTime(gameTick int) {
	raceActive := ss.gameInfo.GameStateFlags&GameStateFlagRaceTime != 0
	wasActive := ss.raceTime.Active

	ss.raceTime.CurrentTick = gameTick

	if raceActive {
		raceTicks := max(gameTick+ss.gameInfo.WarmupTimer, 0)
		ss.raceTime.TickBased = time.Duration(raceTicks) * tickDuration
		ss.raceTime.Active = true

		if !wasActive {
			ss.raceTime.StartedAt = time.Now()
			ss.raceTime.TickAtStart = gameTick
			ss.raceTime.Finished = false
			ss.raceTime.FinishTime = 0
			ss.raceTime.CheckpointDiff = 0
		}
	} else if wasActive {
		ss.raceTime.Active = false
	}
}
