package client

import (
	"encoding/binary"
	"time"

	"github.com/jxsl13/twclient/net6"
	"github.com/jxsl13/twclient/packer"
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
	// lastSnap is the most recently processed snapshot, used by deriveEvents
	// to read transient event-objects (explosions, deaths, projectiles, …).
	lastSnap *packet.Snapshot
	// prevProjectiles/prevLasers hold the entity IDs seen last snapshot so a
	// newly appearing projectile/laser can be reported as "fired" (V14).
	prevProjectiles map[int]struct{}
	prevLasers      map[int]struct{}
	// projectiles holds the latest snapshot's projectile data for prediction.
	projectiles map[int]packet.ProjectileState
	gameInfo    GameInfoState
	raceTime    RaceTime

	// previous game/flag/round state for diffing (T5d). *Init flags suppress
	// spurious events on the first snapshot.
	prevGameState  int
	gameStateInit  bool
	prevScores     map[int]int
	prevFlagRed    int
	prevFlagBlue   int
	flagInit       bool
	prevSpecTarget int
	specInit       bool
	// roster: ObjClientInfo ids seen last snapshot, for join/leave on 0.6
	// (the 0.7 reader emits these as messages instead, V15a).
	prevClientIDs map[int]struct{}
	rosterInit    bool

	// previous DDNet ext-object state per client, for change-triggered events
	// (T4e2). Keyed by client ID.
	prevDDChar   map[int]ddCharState
	prevDDPlayer map[int]ddPlayerState
	prevSpecChar map[int][2]int // client -> {x,y}
}

// ddCharState/ddPlayerState capture the DDNet ext-object fields we diff.
type ddCharState struct {
	flags       int
	freezeEnd   int
	freezeStart int
	jumps       int
	jumpedTotal int
}

type ddPlayerState struct {
	flags     int
	authLevel int
}

// OFFSET_UUID_TYPE marks snapshot item types that are UUID-based ext objects;
// a NETOBJTYPE_EX marker item (type 0, id >= this) carries the UUID.
const offsetUUIDType = 0x4000

// DDNet ext-object UUIDs.
var (
	uuidDDNetCharacter = packer.CalculateUUID("character@netobj.ddnet.tw")
	uuidDDNetPlayer    = packer.CalculateUUID("player@netobj.ddnet.tw")
	uuidSpecChar       = packer.CalculateUUID("spec-char@netobj.ddnet.tw")
	uuidFinish         = packer.CalculateUUID("finish@netevent.ddnet.org")
)

// DDNetPlayer m_Flags bit (EXPLAYERFLAG order: AFK, PAUSED, SPEC).
const exPlayerFlagAfk = 1 << 0

// uuidFromFields reads a 16-byte UUID from the first four snapshot int fields
// (big-endian uint32 per int, matching DDNet's CUuid packing).
func uuidFromFields(f []int) [16]byte {
	var u [16]byte
	for i := 0; i < 4; i++ {
		binary.BigEndian.PutUint32(u[i*4:], uint32(int32(f[i])))
	}
	return u
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

	// Most ticks emit on the order of one event per visible player (move /
	// attack / hook). Preallocating to len(cur) avoids the slice regrowth
	// reallocations that otherwise dominate this path's allocation count
	// alongside the unavoidable packet.Event interface boxing (V51, §PERF T35).
	evs := make([]packet.Event, 0, len(cur))

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

	// Per-visible-player motion/state changes (V13). Only for characters
	// present in both snapshots so we have a baseline to diff against.
	for id, c := range cur {
		p, ok := prev[id]
		if !ok {
			continue
		}

		// Movement, throttled by a minimum delta to avoid per-tick flooding.
		if dx, dy := c.X-p.X, c.Y-p.Y; abs(dx)+abs(dy) >= moveEventThreshold {
			evs = append(evs, packet.EventPlayerMove{ClientID: id, X: c.X, Y: c.Y})
		}
		// Jump: a new jump bit set since last snapshot.
		if c.Jumped&^p.Jumped != 0 {
			evs = append(evs, packet.EventPlayerJump{ClientID: id})
		}
		// Movement direction change (-1/0/1).
		if c.Direction != p.Direction {
			evs = append(evs, packet.EventPlayerDir{ClientID: id, Direction: c.Direction})
		}
		// Weapon fired: AttackTick advanced.
		if c.AttackTick > p.AttackTick {
			evs = append(evs, packet.EventPlayerAttack{ClientID: id, Weapon: packet.Weapon(c.Weapon)})
		}
		// Active weapon swap (others; the local change is EventWeaponChange).
		if id != ss.localCID && c.Weapon != p.Weapon {
			evs = append(evs, packet.EventPlayerWeapon{ClientID: id, Weapon: packet.Weapon(c.Weapon)})
		}
		// Hook state/target transition (generalizes grab/release/hook-by).
		if c.HookState != p.HookState || c.HookedPlayer != p.HookedPlayer {
			evs = append(evs, packet.EventPlayerHook{
				ClientID:  id,
				HookState: c.HookState,
				HookedID:  c.HookedPlayer,
			})
		}
		// Emote change.
		if c.Emote != p.Emote {
			evs = append(evs, packet.EventPlayerEmote{ClientID: id, Emote: c.Emote})
		}
	}

	// Append directly into evs instead of allocating a fresh slice per
	// sub-diff and copying it back (V51).
	evs = ss.deriveTransient(evs)
	evs = ss.deriveGame(evs)
	evs = ss.deriveExt(evs)

	return evs
}

// deriveExt resolves DDNet UUID-based snapshot ext objects and emits their
// events (T4e2). Ext items arrive as raw SnapItems: NETOBJTYPE_EX marker items
// (TypeID 0, ID >= offsetUUIDType) carry the UUID for an internal type id, and
// the actual ext object items use that internal type id (>= offsetUUIDType).
// These objects are DDNet-only; vanilla servers never send them.
func (ss *SnapStorage) deriveExt(evs []packet.Event) []packet.Event {
	if ss.lastSnap == nil {
		return evs
	}

	// Map internal ext type id -> UUID from the marker items.
	markers := make(map[int][16]byte)
	for _, it := range ss.lastSnap.Items {
		if it.TypeID == 0 && it.ID >= offsetUUIDType && len(it.Fields) >= 4 {
			markers[it.ID] = uuidFromFields(it.Fields)
		}
	}
	if len(markers) == 0 {
		return evs
	}

	for _, it := range ss.lastSnap.Items {
		if it.TypeID < offsetUUIDType {
			continue
		}
		uuid, ok := markers[it.TypeID]
		if !ok {
			continue
		}
		switch uuid {
		case uuidDDNetCharacter:
			evs = append(evs, ss.diffDDChar(it.ID, it.Fields)...)
		case uuidDDNetPlayer:
			evs = append(evs, ss.diffDDPlayer(it.ID, it.Fields)...)
		case uuidSpecChar:
			evs = append(evs, ss.diffSpecChar(it.ID, it.Fields)...)
		case uuidFinish:
			evs = append(evs, packet.EventFinish{ClientID: it.ID})
		}
	}
	return evs
}

func (ss *SnapStorage) diffDDChar(cid int, f []int) []packet.Event {
	if len(f) < 8 {
		return nil
	}
	cur := ddCharState{
		flags:       f[0],
		freezeEnd:   f[1],
		jumps:       f[2],
		jumpedTotal: f[5],
		freezeStart: f[7],
	}
	if ss.prevDDChar == nil {
		ss.prevDDChar = make(map[int]ddCharState)
	}
	prev, had := ss.prevDDChar[cid]
	ss.prevDDChar[cid] = cur
	if !had {
		return nil
	}

	var evs []packet.Event
	if cur.freezeEnd != prev.freezeEnd || cur.freezeStart != prev.freezeStart {
		evs = append(evs, packet.EventFreeze{
			ClientID: cid,
			Frozen:   cur.freezeEnd > ss.lastTick || cur.freezeEnd == -1,
			EndTick:  cur.freezeEnd,
		})
	}
	if cur.flags != prev.flags {
		evs = append(evs, packet.EventPlayerFlags{ClientID: cid, Flags: cur.flags})
	}
	if cur.jumps != prev.jumps || cur.jumpedTotal != prev.jumpedTotal {
		evs = append(evs, packet.EventJumpsChange{ClientID: cid, Jumps: cur.jumps, JumpedTotal: cur.jumpedTotal})
	}
	return evs
}

func (ss *SnapStorage) diffDDPlayer(cid int, f []int) []packet.Event {
	if len(f) < 2 {
		return nil
	}
	cur := ddPlayerState{flags: f[0], authLevel: f[1]}
	if ss.prevDDPlayer == nil {
		ss.prevDDPlayer = make(map[int]ddPlayerState)
	}
	prev, had := ss.prevDDPlayer[cid]
	ss.prevDDPlayer[cid] = cur
	if !had {
		return nil
	}

	var evs []packet.Event
	if cur.authLevel != prev.authLevel {
		evs = append(evs, packet.EventPlayerAuth{ClientID: cid, Level: cur.authLevel})
	}
	if (cur.flags & exPlayerFlagAfk) != (prev.flags & exPlayerFlagAfk) {
		evs = append(evs, packet.EventPlayerAfk{ClientID: cid, Afk: cur.flags&exPlayerFlagAfk != 0})
	}
	return evs
}

func (ss *SnapStorage) diffSpecChar(cid int, f []int) []packet.Event {
	if len(f) < 2 {
		return nil
	}
	pos := [2]int{f[0], f[1]}
	if ss.prevSpecChar == nil {
		ss.prevSpecChar = make(map[int][2]int)
	}
	prev, had := ss.prevSpecChar[cid]
	ss.prevSpecChar[cid] = pos
	if had && prev == pos {
		return nil
	}
	return []packet.Event{packet.EventSpecChar{ClientID: cid, X: pos[0], Y: pos[1]}}
}

// deriveGame emits game/flag/round-state events by diffing the latest
// snapshot's GameInfo, PlayerInfo scores, GameData flag carriers, and
// SpectatorInfo target against the previous snapshot's values (D in the
// catalog).
func (ss *SnapStorage) deriveGame(evs []packet.Event) []packet.Event {
	if ss.lastSnap == nil {
		return evs
	}

	// Round state: game-state flags changed (warmup/paused/game-over/...).
	if ss.gameStateInit && ss.gameInfo.GameStateFlags != ss.prevGameState {
		evs = append(evs, packet.EventRoundState{StateFlags: ss.gameInfo.GameStateFlags})
	}
	ss.prevGameState = ss.gameInfo.GameStateFlags
	ss.gameStateInit = true

	// Per-player score changes.
	curScores := make(map[int]int)
	for _, it := range ss.lastSnap.Items {
		if it.TypeID == net6.ObjPlayerInfo && len(it.Fields) >= net6.SizePlayerInfo {
			cid := it.Fields[1]
			score := it.Fields[3]
			curScores[cid] = score
			if prev, ok := ss.prevScores[cid]; ok && prev != score {
				evs = append(evs, packet.EventScoreChange{ClientID: cid, Score: score})
			}
		}
	}
	ss.prevScores = curScores

	// Flag carriers (CTF grab/drop/capture) from GameData.
	for _, it := range ss.lastSnap.Items {
		if it.TypeID == net6.ObjGameData && len(it.Fields) >= net6.SizeGameData {
			red, blue := it.Fields[2], it.Fields[3]
			if ss.flagInit {
				if red != ss.prevFlagRed {
					evs = append(evs, packet.EventFlag{Team: 0, CarrierID: red})
				}
				if blue != ss.prevFlagBlue {
					evs = append(evs, packet.EventFlag{Team: 1, CarrierID: blue})
				}
			}
			ss.prevFlagRed, ss.prevFlagBlue = red, blue
			ss.flagInit = true
			break
		}
	}

	// Roster: ObjClientInfo appearing/disappearing => player join/leave. On 0.6
	// this is the only join/leave source; on 0.7 the reader emits messages and
	// these snapshot objects are absent (V15a). Names/skins are not decoded
	// from the snapshot here, so only ClientID is reported.
	curClientIDs := make(map[int]struct{})
	for _, it := range ss.lastSnap.Items {
		if it.TypeID == net6.ObjClientInfo {
			curClientIDs[it.ID] = struct{}{}
			if ss.rosterInit {
				if _, seen := ss.prevClientIDs[it.ID]; !seen {
					evs = append(evs, packet.EventPlayerJoin{ClientID: it.ID})
				}
			}
		}
	}
	if ss.rosterInit {
		for id := range ss.prevClientIDs {
			if _, ok := curClientIDs[id]; !ok {
				evs = append(evs, packet.EventPlayerLeave{ClientID: id})
			}
		}
	}
	ss.prevClientIDs = curClientIDs
	ss.rosterInit = true

	// Spectated target change (local spectator).
	for _, it := range ss.lastSnap.Items {
		if it.TypeID == net6.ObjSpectatorInfo && len(it.Fields) >= net6.SizeSpectatorInfo {
			target := it.Fields[0]
			if ss.specInit && target != ss.prevSpecTarget {
				evs = append(evs, packet.EventSpecTarget{ClientID: ss.localCID, TargetID: target})
			}
			ss.prevSpecTarget = target
			ss.specInit = true
			break
		}
	}

	return evs
}

// deriveTransient emits events for the one-tick world-event objects in the
// latest snapshot (explosion/spawn/death/hammer-hit/sound) and for newly
// appearing projectiles/lasers (V14).
func (ss *SnapStorage) deriveTransient(evs []packet.Event) []packet.Event {
	if ss.lastSnap == nil {
		return evs
	}

	curProj := make(map[int]struct{})
	curLaser := make(map[int]struct{})
	projData := make(map[int]packet.ProjectileState)

	for _, it := range ss.lastSnap.Items {
		f := it.Fields
		switch it.TypeID {
		case net6.ObjExplosion:
			if len(f) >= net6.SizeExplosion {
				evs = append(evs, packet.EventExplosion{X: f[0], Y: f[1]})
			}
		case net6.ObjSpawn:
			if len(f) >= net6.SizeSpawn {
				evs = append(evs, packet.EventSpawn{X: f[0], Y: f[1]})
			}
		case net6.ObjHammerHit:
			if len(f) >= net6.SizeHammerHit {
				evs = append(evs, packet.EventHammerHit{X: f[0], Y: f[1]})
			}
		case net6.ObjDeath:
			if len(f) >= net6.SizeDeath {
				evs = append(evs, packet.EventDeath{X: f[0], Y: f[1], ClientID: f[2]})
			}
		case net6.ObjSoundWorld:
			if len(f) >= net6.SizeSoundWorld {
				evs = append(evs, packet.EventSoundWorld{X: f[0], Y: f[1], SoundID: f[2]})
			}
		case net6.ObjDamageIndicator:
			if len(f) >= net6.SizeDamageIndicator {
				evs = append(evs, packet.EventDamageInd{X: f[0], Y: f[1], Angle: f[2]})
			}
		case net6.ObjProjectile:
			curProj[it.ID] = struct{}{}
			if len(f) >= net6.SizeProjectile {
				projData[it.ID] = packet.ProjectileState{
					ID: it.ID, X: f[0], Y: f[1], VelX: f[2], VelY: f[3],
					Type: packet.Weapon(f[4]), StartTick: f[5],
				}
				if _, seen := ss.prevProjectiles[it.ID]; !seen {
					evs = append(evs, packet.EventProjectileFired{
						X: f[0], Y: f[1], VelX: f[2], VelY: f[3],
						Type: packet.Weapon(f[4]), Owner: -1,
					})
				}
			}
		case net6.ObjLaser:
			curLaser[it.ID] = struct{}{}
			if _, seen := ss.prevLasers[it.ID]; !seen && len(f) >= net6.SizeLaser {
				evs = append(evs, packet.EventLaserFired{
					X: f[0], Y: f[1], FromX: f[2], FromY: f[3], Owner: -1,
				})
			}
		}
	}

	ss.prevProjectiles = curProj
	ss.prevLasers = curLaser
	ss.projectiles = projData
	return evs
}

// moveEventThreshold is the minimum Manhattan position delta (world units)
// before an EventPlayerMove fires, throttling the otherwise per-tick stream
// of position updates (V13).
const moveEventThreshold = 16

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
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
	ss.lastSnap = snap
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
	// Double-buffer (V51): reuse the now-stale prevCharacters backing map
	// (cleared + refilled) as the new current, so steady-state ticks allocate
	// no map. Safe because callers receive COPIES via charactersCopy (V52) —
	// ss.characters is never handed out by reference — and processSnapshot runs
	// under the write lock while readers hold the read lock.
	newChars := ss.prevCharacters
	if newChars == nil {
		newChars = make(map[int]CharacterState, len(snap.Items))
	} else {
		clear(newChars)
	}
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
