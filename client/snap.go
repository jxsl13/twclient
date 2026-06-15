package client

import (
	"encoding/binary"
	"maps"
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
	// lastSnap is the most recently processed RAW snapshot (kept for the DDNet
	// ext-object path, which is UUID-keyed and not in the shared repr).
	lastSnap *packet.Snapshot
	// objs is the decoded, protocol-neutral content of lastSnap (V112). All
	// snap-derived state/events read THIS, not net6/net7 object ids. decode is
	// the version-appropriate decoder (net6/net7 DecodeSnap), set by the client;
	// nil defaults to net6 (0.6). is07 guards the 0.6-only ObjClientInfo names
	// path (net6 id 11 == net7 PlayerInfo id, V115).
	objs   packet.SnapObjects
	decode func(*packet.Snapshot) packet.SnapObjects
	is07   bool
	// moveEventThreshold is the per-Client EventPlayerMove throttle (V127), set
	// from the Client at construction. Zero falls back to DefaultMoveEventThreshold
	// so a bare SnapStorage{} keeps the original behavior (V48).
	moveEventThreshold int
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
	prevTeams      map[int]int // 0.6 ObjPlayerInfo m_Team, for EventTeamSet diff (T122)
	prevFlagRed    int
	prevFlagBlue   int
	flagInit       bool
	prevSpecTarget int
	specInit       bool
	// roster: ObjClientInfo ids seen last snapshot, for join/leave on 0.6
	// (the 0.7 reader emits these as messages instead, V15a).
	prevClientIDs map[int]struct{}
	prevSkins     map[int][6]int // 0.6 ObjClientInfo skin ints, for EventSkinChange diff (T123)
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
	for i := range 4 {
		binary.BigEndian.PutUint32(u[i*4:], uint32(int32(f[i])))
	}
	return u
}

// charactersCopy returns a shallow copy of the latest per-client character
// map. Caller must hold the Client mutex.
func (ss *SnapStorage) charactersCopy() map[int]CharacterState {
	out := make(map[int]CharacterState, len(ss.characters))
	maps.Copy(out, ss.characters)
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
	moveThreshold := ss.moveEventThreshold
	if moveThreshold <= 0 { // zero-value SnapStorage → default (V48/V127)
		moveThreshold = DefaultMoveEventThreshold
	}
	for id, c := range cur {
		p, ok := prev[id]
		if !ok {
			continue
		}

		// Movement, throttled by a minimum delta to avoid per-tick flooding.
		if dx, dy := c.X-p.X, c.Y-p.Y; abs(dx)+abs(dy) >= moveThreshold {
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

	// Per-player score + team changes (0.6 ObjPlayerInfo m_Team idx 2, m_Score
	// idx 3). 0.7 sends team via Sv_Team (net7), so this is the 0.6 team feed
	// (T122) and keeps the change-only semantics of EventScoreChange.
	curScores := make(map[int]int)
	curTeams := make(map[int]int)
	for cid, p := range ss.objs.Players {
		curScores[cid] = p.Score
		curTeams[cid] = p.Team
		if prev, ok := ss.prevScores[cid]; ok && prev != p.Score {
			evs = append(evs, packet.EventScoreChange{ClientID: cid, Score: p.Score})
		}
		if prev, ok := ss.prevTeams[cid]; ok && prev != p.Team {
			evs = append(evs, packet.EventTeamSet{ClientID: cid, Team: p.Team})
		}
	}
	ss.prevScores = curScores
	ss.prevTeams = curTeams

	// Flag carriers (CTF grab/drop/capture) from GameData.
	if ss.objs.HasGameData {
		red, blue := ss.objs.GameData.FlagCarrierRed, ss.objs.GameData.FlagCarrierBlue
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
	}

	// Roster: ObjClientInfo appearing/disappearing => player join/leave. This is
	// a 0.6-ONLY snapshot path (names): on 0.7 the reader emits Sv_ClientInfo
	// messages instead, and net6.ObjClientInfo's id (11) collides with net7's
	// ObjPlayerInfo, so it MUST NOT run on a 0.7 snapshot (V115). Names are the
	// documented exception not carried in the shared SnapObjects.
	if !ss.is07 {
		evs = ss.deriveRoster06(evs)
	}

	// Spectated target change (local spectator).
	if ss.objs.HasSpectator {
		target := ss.objs.Spectator.SpectatorID
		if ss.specInit && target != ss.prevSpecTarget {
			evs = append(evs, packet.EventSpecTarget{ClientID: ss.localCID, TargetID: target})
		}
		ss.prevSpecTarget = target
		ss.specInit = true
	}

	return evs
}

// deriveRoster06 is the 0.6-only ObjClientInfo join/leave/skin diff (names live
// in the snapshot on 0.6, in messages on 0.7 — V115).
func (ss *SnapStorage) deriveRoster06(evs []packet.Event) []packet.Event {
	curClientIDs := make(map[int]struct{})
	curSkins := make(map[int][6]int)
	for _, it := range ss.lastSnap.Items {
		if it.TypeID == net6.ObjClientInfo {
			curClientIDs[it.ID] = struct{}{}
			skin, _ := net6.SkinInts(it.Fields)
			curSkins[it.ID] = skin
			// Emit a join for any id NOT in the previous snapshot — INCLUDING the
			// FIRST snapshot (prevClientIDs is then empty, so every present client
			// joins). Gating this on rosterInit dropped the entire INITIAL roster on
			// 0.6 — players present from snapshot #1 never produced a join, so the
			// registry stayed empty (issue #3, B24). The dedup is the prevClientIDs
			// membership check, not a first-snapshot skip.
			if _, seen := ss.prevClientIDs[it.ID]; !seen {
				ci := net6.DecodeClientInfo(it.Fields)
				evs = append(evs, packet.EventPlayerJoin{
					ClientID: it.ID,
					Name:     ci.Name,
					Clan:     ci.Clan,
					Country:  ci.Country,
					Skin:     ci.Skin,
				})
			} else if prev, ok := ss.prevSkins[it.ID]; ok && prev != skin {
				// Same player, skin object changed — decode only on change (T123).
				evs = append(evs, packet.EventSkinChange{ClientID: it.ID, Skin: net6.DecodeClientInfo(it.Fields).Skin})
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
	ss.prevSkins = curSkins
	ss.rosterInit = true
	return evs
}

// deriveTransient emits events for the one-tick world-event objects in the
// latest snapshot (explosion/spawn/death/hammer-hit/sound) and for newly
// appearing projectiles/lasers (V14).
func (ss *SnapStorage) deriveTransient(evs []packet.Event) []packet.Event {
	if ss.lastSnap == nil {
		return evs
	}

	// Transient one-tick events are decoded into the shared form already.
	evs = append(evs, ss.objs.Events...)

	curProj := make(map[int]struct{})
	curLaser := make(map[int]struct{})
	projData := make(map[int]packet.ProjectileState)

	for _, p := range ss.objs.Projectiles {
		curProj[p.ID] = struct{}{}
		// packet.Projectile and packet.ProjectileState are field-identical (one
		// canonical projectile type) → direct conversion (S1016).
		projData[p.ID] = packet.ProjectileState(p)
		if _, seen := ss.prevProjectiles[p.ID]; !seen {
			evs = append(evs, packet.EventProjectileFired{
				X: p.X, Y: p.Y, VelX: p.VelX, VelY: p.VelY, Type: p.Type, Owner: -1,
			})
		}
	}
	for _, l := range ss.objs.Lasers {
		curLaser[l.ID] = struct{}{}
		if _, seen := ss.prevLasers[l.ID]; !seen {
			evs = append(evs, packet.EventLaserFired{
				X: l.X, Y: l.Y, FromX: l.FromX, FromY: l.FromY, Owner: -1,
			})
		}
	}

	ss.prevProjectiles = curProj
	ss.prevLasers = curLaser
	ss.projectiles = projData
	return evs
}

// DefaultMoveEventThreshold is the minimum Manhattan position delta (world
// units) before an EventPlayerMove fires, throttling the otherwise per-tick
// stream of position updates (V13). It is the default for the per-Client
// WithMoveEventThreshold option (V127); a zero-value SnapStorage falls back to
// it so internal/test construction keeps the original behavior (V48).
const DefaultMoveEventThreshold = 16

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
	decode := ss.decode
	if decode == nil {
		decode = net6.DecodeSnap // 0.6 default (keeps existing behavior/tests)
	}
	ss.objs = decode(snap)
	ss.updateFromSnap(snap)
}

const tickDuration = 20 * time.Millisecond

func (ss *SnapStorage) updateFromSnap(snap *packet.Snapshot) {
	o := &ss.objs

	// Local player id: 0.6 marks it via Player.Local; 0.7 carries no snapshot
	// Local bit (set separately from Sv_ClientInfo, T140) so this leaves it
	// untouched on 0.7.
	for _, p := range o.Players {
		if p.Local {
			ss.localCID = p.ClientID
		}
	}

	// Game info — only when the snapshot actually carried a GameInfo/GameData
	// object (a gameinfo-less snap must NOT zero the retained state).
	if o.HasGameInfo {
		ss.gameInfo = GameInfoState{
			GameFlags:      o.GameInfo.GameFlags,
			GameStateFlags: o.GameInfo.GameStateFlags,
			RoundStartTick: o.GameInfo.RoundStartTick,
			WarmupTimer:    o.GameInfo.WarmupTimer,
			ScoreLimit:     o.GameInfo.ScoreLimit,
			TimeLimit:      o.GameInfo.TimeLimit,
			RoundNum:       o.GameInfo.RoundNum,
			RoundCurrent:   o.GameInfo.RoundCurrent,
		}
		ss.updateRaceTime(snap.Tick)
	}

	// Build the per-client character map for this snapshot, rotating the
	// previous map into prevCharacters so snap-derived events can diff them.
	// Double-buffer (V51): reuse the now-stale prevCharacters backing map.
	// packet.Character and CharacterState are field-identical (one canonical
	// character type, V25) so the conversion is a direct cast.
	newChars := ss.prevCharacters
	if newChars == nil {
		newChars = make(map[int]CharacterState, len(o.Characters))
	} else {
		clear(newChars)
	}
	for id, c := range o.Characters {
		cs := CharacterState(c)
		// On 0.7 the player flags (PLAYERFLAG_*) live in PlayerInfo, not the
		// Character object (its 7th tail slot is m_TriggeredEvents), so the
		// decoder left CharacterState.PlayerFlags at 0. Overlay it from the
		// decoded Player so PlayerFlags is populated on BOTH protocols (V107/V115);
		// 0.6 already carries it in the character (Player.PlayerFlags == 0 there).
		if ss.is07 {
			if p, ok := o.Players[id]; ok {
				cs.PlayerFlags = p.PlayerFlags
			}
		}
		newChars[id] = cs
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
