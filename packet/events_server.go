package packet

// Server-event types delivered on a Session's EventCh, in addition to the
// core events in event.go. Every type implements Event via eventTag().
//
// Protocol-unified (V17): one struct per logical event. The net6 and net7
// readers both emit the SAME struct; consumers never branch on protocol
// version. Events that exist in only one protocol are documented as such.
//
// Field semantics and source mapping are documented in SPEC.md §I.catalog,
// verified against DDNet (datasrc/network.py, sixup_translate_game.cpp) and
// Teeworlds 0.7 (datasrc/network.py).

// ---- chat / text (msg-derived) ----

// EventChat is a public/team chat line. Team: 0=all, 1=team (DDNet m_Team
// -2..3). ClientID is the author (-1 = server, delivered as EventServerMsg).
type EventChat struct {
	Team     int
	ClientID int
	Msg      string
}

func (EventChat) eventTag() {}

// EventServerMsg is a server-authored chat line (Sv_Chat with ClientID == -1).
type EventServerMsg struct {
	Msg string
}

func (EventServerMsg) eventTag() {}

// EventWhisper is a private message. Unified across protocols (V15):
// 0.6 DDNet Sv_Chat m_Team==TEAM_WHISPER_RECV/SEND; 0.7 Sv_Chat mode==WHISPER.
type EventWhisper struct {
	FromID int
	ToID   int
	Msg    string
}

func (EventWhisper) eventTag() {}

// EventBroadcast is the centered broadcast text (Sv_Broadcast).
type EventBroadcast struct {
	Text string
}

func (EventBroadcast) eventTag() {}

// EventMotd is the message-of-the-day (Sv_Motd).
type EventMotd struct {
	Text string
}

func (EventMotd) eventTag() {}

// ---- kill / emote / pickup ----

// EventKill is a kill message (Sv_KillMsg).
type EventKill struct {
	Killer      int
	Victim      int
	Weapon      Weapon
	ModeSpecial int
}

func (EventKill) eventTag() {}

// EventEmoticon is another player showing an emoticon (Sv_Emoticon).
type EventEmoticon struct {
	ClientID int
	Emoticon Emoticon
}

func (EventEmoticon) eventTag() {}

// EventWeaponPickup notifies the local player picked up a weapon
// (Sv_WeaponPickup).
type EventWeaponPickup struct {
	Weapon Weapon
}

func (EventWeaponPickup) eventTag() {}

// ---- sound / tuning ----

// EventSoundGlobal is a global UI/world sound (Sv_SoundGlobal). 0.6/DDNet only;
// 0.7 routes sounds differently.
type EventSoundGlobal struct {
	SoundID int
}

func (EventSoundGlobal) eventTag() {}

// EventTuneParams carries the server tuning. Raw holds the fixed-point values
// (value*100) in protocol field order; feeds prediction Tuning (V9b).
type EventTuneParams struct {
	Raw []int32
}

func (EventTuneParams) eventTag() {}

// ---- vote ----

// EventVoteSet starts/clears a vote (Sv_VoteSet). Timeout>0 = vote started,
// Timeout==0 = vote ended.
type EventVoteSet struct {
	Timeout int
	Desc    string
	Reason  string
}

func (EventVoteSet) eventTag() {}

// EventVoteStatus is the running tally (Sv_VoteStatus).
type EventVoteStatus struct {
	Yes   int
	No    int
	Pass  int
	Total int
}

func (EventVoteStatus) eventTag() {}

// VoteOptionOp is the kind of vote-option list change.
type VoteOptionOp int

// VoteOptionOp values: the votable-option menu was cleared, an option added,
// or an option removed (DDNet Sv_VoteClearOptions / Sv_VoteOptionAdd / Remove).
const (
	VoteOptionClear  VoteOptionOp = iota // Sv_VoteClearOptions
	VoteOptionAdd                        // Sv_VoteOptionAdd / ListAdd
	VoteOptionRemove                     // Sv_VoteOptionRemove
)

// EventVoteOption mutates the votable-option menu (Sv_VoteOption*).
type EventVoteOption struct {
	Op   VoteOptionOp
	Desc string
}

func (EventVoteOption) eventTag() {}

// EventYourVote reports whether the local player already voted (DDNet ext
// Sv_YourVote).
type EventYourVote struct {
	Voted int
}

func (EventYourVote) eventTag() {}

// ---- system / rcon ----

// EventRconLine is a remote-console output line (NETMSG_RCON_LINE).
type EventRconLine struct {
	Line string
}

func (EventRconLine) eventTag() {}

// EventRconAuth reports rcon authentication state (RCON_AUTH_ON/OFF / status).
type EventRconAuth struct {
	Authed bool
	Level  int
}

func (EventRconAuth) eventTag() {}

// RconCmdOp is whether a command was added or removed.
type RconCmdOp int

// RconCmdOp values: a command was added to, or removed from, the rcon
// completion list (DDNet NETMSG_RCON_CMD_ADD / NETMSG_RCON_CMD_REM).
const (
	RconCmdAdd RconCmdOp = iota
	RconCmdRemove
)

// EventRconCmd is a remote-console command-list change (RCON_CMD_ADD/REM).
type EventRconCmd struct {
	Op     RconCmdOp
	Cmd    string
	Help   string
	Params string
}

func (EventRconCmd) eventTag() {}

// EventServerError is a server-side error message (NETMSG_SV_ERROR).
type EventServerError struct {
	Msg string
}

func (EventServerError) eventTag() {}

// ---- DDNet ext messages ----

// EventTeamsState reports DDRace team membership (DDNet ext Sv_TeamsState).
// Team maps clientID -> ddrace team number (0 = no team).
type EventTeamsState struct {
	Team map[int]int
}

func (EventTeamsState) eventTag() {}

// EventKillMsgTeam is a team-wide kill message (DDNet ext Sv_KillMsgTeam).
type EventKillMsgTeam struct {
	Team  int
	First int
}

func (EventKillMsgTeam) eventTag() {}

// EventCommandInfo registers a chat command (DDNet ext Sv_CommandInfo).
type EventCommandInfo struct {
	Name    string
	Help    string
	ArgsFmt string
}

func (EventCommandInfo) eventTag() {}

// EventCommandInfoRemove unregisters a chat command (Sv_CommandInfoRemove).
type EventCommandInfoRemove struct {
	Name string
}

func (EventCommandInfoRemove) eventTag() {}

// EventChangeInfoCooldown reports the cooldown tick before info can change
// again (DDNet ext Sv_ChangeInfoCooldown).
type EventChangeInfoCooldown struct {
	WaitUntilTick int
}

func (EventChangeInfoCooldown) eventTag() {}

// EventMapSoundGlobal is a map-defined global sound (DDNet ext Sv_MapSoundGlobal).
type EventMapSoundGlobal struct {
	SoundID int
}

func (EventMapSoundGlobal) eventTag() {}

// ---- 0.7 message / 0.6 snap-object unified (V15a) ----

// EventPlayerJoin fires when a player appears. 0.7: Sv_ClientInfo;
// 0.6: ObjClientInfo first seen in snapshot.
type EventPlayerJoin struct {
	ClientID int
	Name     string
	Clan     string
	Country  int
	Skin     string
	Team     int
}

func (EventPlayerJoin) eventTag() {}

// EventPlayerLeave fires when a player drops. 0.7: Sv_ClientDrop (with reason);
// 0.6: ObjClientInfo gone (Reason empty).
type EventPlayerLeave struct {
	ClientID int
	Reason   string
}

func (EventPlayerLeave) eventTag() {}

// EventSkinChange fires when a player changes appearance. 0.7: Sv_SkinChange;
// 0.6: ObjClientInfo diff.
type EventSkinChange struct {
	ClientID int
	Skin     string
}

func (EventSkinChange) eventTag() {}

// EventTeamSet fires when a player's team changes. 0.7: Sv_Team;
// 0.6/DDNet: derived from team state.
type EventTeamSet struct {
	ClientID int
	Team     int
	Silent   bool
}

func (EventTeamSet) eventTag() {}

// EventGameInfo carries the current game rules/flags. 0.7: Sv_GameInfo;
// 0.6: ObjGameInfo snapshot item.
type EventGameInfo struct {
	GameFlags      int
	GameStateFlags int
	ScoreLimit     int
	TimeLimit      int
}

func (EventGameInfo) eventTag() {}

// EventGameMsg is a 0.7 system game message (Sv_GameMsg, e.g. round/team
// outcomes). 0.7 only.
type EventGameMsg struct {
	GameMsgID int
	Params    []int32
}

func (EventGameMsg) eventTag() {}

// EventServerSettings reports server permission flags. 0.7: Sv_ServerSettings.
type EventServerSettings struct {
	KickVote    bool
	KickMin     int
	SpecVote    bool
	TeamLock    bool
	TeamBalance bool
	PlayerSlots int
}

func (EventServerSettings) eventTag() {}

// ---- snap-derived: presence (A) ----

// EventPlayerEnterSight fires when a character enters the snapshot set (becomes
// visible to the local tee). X, Y are its position at the entering edge.
type EventPlayerEnterSight struct {
	ClientID int
	X        int
	Y        int
}

func (EventPlayerEnterSight) eventTag() {}

// EventPlayerLeaveSight fires when a character leaves the snapshot set.
type EventPlayerLeaveSight struct {
	ClientID int
}

func (EventPlayerLeaveSight) eventTag() {}

// ---- snap-derived: motion / state (B) ----

// EventHookedBy fires when another player hooks the local character.
type EventHookedBy struct {
	ClientID int // the hooker
}

func (EventHookedBy) eventTag() {}

// EventWeaponChange fires when the server changes the local player's weapon.
type EventWeaponChange struct {
	Weapon Weapon
}

func (EventWeaponChange) eventTag() {}

// EventPlayerMove fires when a visible player's position changes (throttled).
type EventPlayerMove struct {
	ClientID int
	X        int
	Y        int
}

func (EventPlayerMove) eventTag() {}

// EventPlayerJump fires on a visible player's jump.
type EventPlayerJump struct {
	ClientID int
}

func (EventPlayerJump) eventTag() {}

// EventPlayerDir fires when a visible player's movement direction changes.
type EventPlayerDir struct {
	ClientID  int
	Direction int // -1, 0, 1
}

func (EventPlayerDir) eventTag() {}

// EventPlayerAttack fires when a visible player fires a weapon (AttackTick up).
type EventPlayerAttack struct {
	ClientID int
	Weapon   Weapon
}

func (EventPlayerAttack) eventTag() {}

// EventPlayerWeapon fires when any visible player's active weapon changes.
type EventPlayerWeapon struct {
	ClientID int
	Weapon   Weapon
}

func (EventPlayerWeapon) eventTag() {}

// EventPlayerHook fires on any hook-state transition of a visible player,
// generalizing grab/release/hook-by. HookedID is the hooked player (-1 = none).
type EventPlayerHook struct {
	ClientID  int
	HookState int
	HookedID  int
}

func (EventPlayerHook) eventTag() {}

// EventPlayerEmote fires when a visible player's emote/eye changes.
type EventPlayerEmote struct {
	ClientID int
	Emote    int
}

func (EventPlayerEmote) eventTag() {}

// EventPlayerHP fires when a visible player's health/armor changes
// (vanilla only; DDRace freezes HP).
type EventPlayerHP struct {
	ClientID int
	Health   int
	Armor    int
}

func (EventPlayerHP) eventTag() {}

// ---- snap-derived: transient world objects (C) ----

// EventExplosion is a one-tick explosion effect.
type EventExplosion struct {
	X int
	Y int
}

func (EventExplosion) eventTag() {}

// EventSpawn is a one-tick spawn effect.
type EventSpawn struct {
	X int
	Y int
}

func (EventSpawn) eventTag() {}

// EventDeath is a one-tick death effect for a player.
type EventDeath struct {
	X        int
	Y        int
	ClientID int
}

func (EventDeath) eventTag() {}

// EventHammerHit is a one-tick hammer-hit effect.
type EventHammerHit struct {
	X int
	Y int
}

func (EventHammerHit) eventTag() {}

// EventSoundWorld is a positional world sound.
type EventSoundWorld struct {
	X       int
	Y       int
	SoundID int
}

func (EventSoundWorld) eventTag() {}

// EventProjectileFired fires when a new projectile appears (someone shot).
type EventProjectileFired struct {
	X     int
	Y     int
	VelX  int
	VelY  int
	Type  Weapon
	Owner int
}

func (EventProjectileFired) eventTag() {}

// ProjectileState is a projectile's state. When returned from
// PredictedProjectiles, X and Y are the predicted positions at the predicted
// tick; VelX/VelY are the launch direction (×100, as on the wire).
type ProjectileState struct {
	ID        int
	X         int
	Y         int
	VelX      int
	VelY      int
	Type      Weapon
	StartTick int
}

// EventLaserFired fires when a new laser appears (someone shot).
type EventLaserFired struct {
	FromX int
	FromY int
	X     int
	Y     int
	Owner int
}

func (EventLaserFired) eventTag() {}

// EventDamageInd is a damage indicator (took/dealt damage).
type EventDamageInd struct {
	X     int
	Y     int
	Angle int
}

func (EventDamageInd) eventTag() {}

// EventFinish is the DDNet finish effect (ext NetEvent Finish).
type EventFinish struct {
	ClientID int
}

func (EventFinish) eventTag() {}

// ---- snap-derived: DDNet ext objects (E) ----

// EventFreeze fires on a freeze state change (DDNetCharacter m_FreezeEnd).
type EventFreeze struct {
	ClientID int
	Frozen   bool
	EndTick  int
}

func (EventFreeze) eventTag() {}

// EventPlayerFlags fires when a DDNetCharacter's flags change (solo, collision,
// hook, etc).
type EventPlayerFlags struct {
	ClientID int
	Flags    int
}

func (EventPlayerFlags) eventTag() {}

// EventJumpsChange fires when a player's jump count/usage changes.
type EventJumpsChange struct {
	ClientID    int
	Jumps       int
	JumpedTotal int
}

func (EventJumpsChange) eventTag() {}

// EventPlayerAuth fires when a player's authentication level changes
// (DDNetPlayer m_AuthLevel).
type EventPlayerAuth struct {
	ClientID int
	Level    int
}

func (EventPlayerAuth) eventTag() {}

// EventPlayerAfk fires on AFK/paused/spectator state change (DDNetPlayer flags).
type EventPlayerAfk struct {
	ClientID int
	Afk      bool
}

func (EventPlayerAfk) eventTag() {}

// EventSpecChar fires with a spectated free-view character position (SpecChar).
type EventSpecChar struct {
	ClientID int
	X        int
	Y        int
}

func (EventSpecChar) eventTag() {}

// ---- snap-derived: game / flag / round state (D) ----

// EventRoundState fires when the game state flags change (warmup, paused,
// game-over, round-over). Normalized across 0.6 GameInfo / 0.7 game state.
type EventRoundState struct {
	StateFlags int
}

func (EventRoundState) eventTag() {}

// EventScoreChange fires when a player's score changes.
type EventScoreChange struct {
	ClientID int
	Score    int
}

func (EventScoreChange) eventTag() {}

// EventFlag fires on CTF flag state change (grab/drop/capture).
type EventFlag struct {
	Team      int // flag team
	CarrierID int // -1 = at base / dropped
	X         int
	Y         int
}

func (EventFlag) eventTag() {}

// EventSpecTarget fires when the local spectator's target changes.
type EventSpecTarget struct {
	ClientID int
	TargetID int
}

func (EventSpecTarget) eventTag() {}
