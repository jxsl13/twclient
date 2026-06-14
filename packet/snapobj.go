package packet

// This file defines the CANONICAL, protocol-neutral snapshot object set (V112).
// Both the net6 (0.6) and net7 (0.7) decoders populate these SAME types from
// their own wire layouts; the client and all consumers read ONLY these, never a
// net6/net7 object type-id or positional field layout. Per-version field/id
// differences are resolved in the decoders (V113). It is the snapshot analogue
// of the protocol-unified message events (V15a/V17).

// Character is one player's character state for a snapshot tick. On the wire it
// is 0.6 ObjCharacter (id 9) or 0.7 ObjCharacter (id 10) — both = CharacterCore
// (15 ints) + 7 tail fields. The tail differs slightly (0.6 has m_PlayerFlags,
// 0.7 has m_TriggeredEvents in that slot), normalized by the decoders.
type Character struct {
	Tick         int
	X, Y         int
	VelX, VelY   int
	Angle        int
	Direction    int
	Jumped       int
	HookedPlayer int
	HookState    int
	HookTick     int
	HookX, HookY int
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

// Player is protocol-neutral scoreboard info for one player. 0.6 ObjPlayerInfo
// (id 10) is 5 positional ints {Local, ClientID, Team, Score, Latency}; 0.7
// ObjPlayerInfo (id 11) is 3 ints {PlayerFlags, Score, Latency} with the client
// id carried as the snapshot ITEM ID and Team delivered separately (Sv_Team).
// The decoders map both into this shape; ClientID is always the player id.
type Player struct {
	ClientID int
	Local    bool
	Team     int // 0 on 0.7 until a team message arrives (V113)
	Score    int
	Latency  int
	// PlayerFlags carries CNetObj_PlayerInfo.m_PlayerFlags on 0.7 (PLAYERFLAG_*:
	// playing/in-menu/chatting/scoreboard/aim). 0.6 keeps these flags in the
	// Character object instead (Character.PlayerFlags), so this stays 0 on 0.6;
	// the client overlays it onto the 0.7 character post-decode so CharacterState.
	// PlayerFlags is populated on BOTH protocols (V107/V115).
	PlayerFlags int
}

// Pickup is a visible pickup entity (weapon/health/armor/ninja).
type Pickup struct {
	X, Y    int
	Type    int
	Subtype int
}

// Flag is a CTF flag entity.
type Flag struct {
	X, Y int
	Team int
}

// Laser is a visible laser beam.
type Laser struct {
	ID        int
	X, Y      int
	FromX     int
	FromY     int
	StartTick int
}

// Projectile is a visible projectile entity (its weapon kind + spawn tick let a
// consumer extrapolate the ballistic position).
type Projectile struct {
	ID        int
	X, Y      int
	VelX      int
	VelY      int
	Type      Weapon
	StartTick int
}

// SpectatorInfo is the local spectator's view target + camera position.
type SpectatorInfo struct {
	SpectatorID int
	X, Y        int
}

// GameInfo is protocol-neutral game rules/state (0.6 ObjGameInfo / 0.7 ObjGameData
// game-state fields). Fields a protocol does not provide stay zero (V113).
type GameInfo struct {
	GameFlags      int
	GameStateFlags int
	RoundStartTick int // 0.6 ObjGameInfo / 0.7 ObjGameData m_GameStartTick (same concept)
	WarmupTimer    int // 0.6 only (race_ticks = gameTick + WarmupTimer); 0.7 → 0
	ScoreLimit     int // 0.6 only; 0.7 → 0
	TimeLimit      int // 0.6 only; 0.7 → 0
	RoundNum       int // 0.6 only; 0.7 → 0
	RoundCurrent   int // 0.6 only; 0.7 → 0
	// GameStateEndTick is a 0.7 ObjGameData field; 0.6 has no equivalent → 0.
	GameStateEndTick int
}

// GameData is protocol-neutral team + flag-carrier scores (0.6 ObjGameData / 0.7
// ObjGameDataTeam + ObjGameDataFlag).
type GameData struct {
	TeamscoreRed    int
	TeamscoreBlue   int
	FlagCarrierRed  int
	FlagCarrierBlue int
}

// SnapObjects is the decoded, protocol-neutral content of one snapshot — the
// SHARED representation both decoders populate and the client consumes (V112).
// Per-player maps are keyed by client id.
type SnapObjects struct {
	Tick         int
	Characters   map[int]Character
	Players      map[int]Player
	Projectiles  []Projectile
	Lasers       []Laser
	Pickups      []Pickup
	Flags        []Flag
	GameInfo     GameInfo
	GameData     GameData
	HasGameInfo  bool // a GameInfo/GameData item was present this snapshot (gate updates)
	HasGameData  bool // a GameData (team/flag scores) item was present (gate flag-event updates)
	HasSpectator bool // a SpectatorInfo item was present (gate spec-target updates)
	Spectator    SpectatorInfo
	// Events are the transient one-tick events in this snapshot (explosion,
	// spawn, hammerhit, death, sound, damage), already as the protocol-unified
	// Event values (EventExplosion/…). Both decoders extract them from their
	// CNetEvent_* objects so the consumer derives them version-agnostically (V115).
	Events []Event
}
