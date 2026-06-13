# SPEC — twclient: server-event callbacks, antiping prediction, consumer/Frontend interface

## §G — goal

Client exposes callback registration for server events (chat, whisper, server msg, vote, hook-by, weapon-change, …) + full DDNet antiping prediction (predict whole world — all chars + projectiles/lasers — ahead of snaps via `physics.Core`, smoothed reconcile) + ONE pluggable tick-driven consumer path (`Observer` view-only + single `Controller` view+action) serving UI render+input, ML training, and ML execution identically (protocol-unified) — incl. ego-centric fixed-window map observation over the complete local map. consolidate redundant types (one canonical per concept). + resilient connection: auto-reconnect that resumes the SAME tee via DDNet timeout-code after a drop, and reconnect after kick/ban by waiting out the ban while periodically polling for early unban. + connect to password-protected servers. + remote console (rcon): log in, send commands, and react to rcon log lines. + PERFORMANCE: minimize CPU + heap alloc on hot paths (snap delta decode, packet unpack/pack, prediction re-sim, per-tick event diff) of the LIBRARY client (`packer`,`packet`,`net6`,`net7`,`physics`,`client`; ⊥ `cmd/racebot`) — benchmark-driven, public API + observable behavior UNCHANGED. profile first → optimize PROVEN hot paths → re-bench.

## §C — constraints

- C1: Go 1.26.1, module `github.com/jxsl13/twclient`. No new deps.
- C2: support both `packet.Version06` (net6) & `packet.Version07` (net7). ! single shared event-type set — both protocols map to EXACT same event structs wherever feature exists in both. version diff hidden in reader, ⊥ leak to consumer.
- C3: callbacks fire from `eventLoop` goroutine (`client/client.go:363`). 1 goroutine → callbacks serialized.
- C4: existing event flow unchanged: session reader → `packet.Event` on `EventCh()` → `Client.handleEvent`. New events extend `packet.Event` interface (`eventTag()`).
- C5: 2 event classes. msg-derived = parse game msg in `net6/reader.go` `processPayload` switch (`:180`) & net7 equiv. snap-derived (hook-by, weapon-change) = diff consecutive `CharacterState` in `client/snap.go`.
- C6: timeout-code RESUME = DDNet ext (DDNet sys msg sent after join). vanilla teeworlds 0.6/0.7 ⊥ resume → feature DDNet-only, documented version-only (V37). kick/ban DETECT (CTRL_CLOSE reason) works on ALL servers — reason already surfaced as `packet.EventClose{Reason}` (`net6/reader.go:128`), today dropped at `client/client.go:499` (T23 fixes). reconnect reuses existing `Client.Connect`/`Reconnect` (`client/client.go:218,288`) — ⊥ new session path.

## §I — interfaces

### callback API (Client)
per-event `OnX`. handler ! receive `*Client` first param → response logic inline. Returns unregister closure.
```
register: func (c *Client) OnChat(fn func(*Client, packet.EventChat))       → func() // unregister
register: func (c *Client) OnWhisper(fn func(*Client, packet.EventWhisper)) → func()
register: func (c *Client) OnBroadcast(fn func(*Client, packet.EventBroadcast)) → func()
register: func (c *Client) OnServerMsg(fn func(*Client, packet.EventServerMsg)) → func()
register: func (c *Client) OnVoteSet(fn func(*Client, packet.EventVoteSet)) → func()
register: func (c *Client) OnVoteStatus(fn func(*Client, packet.EventVoteStatus)) → func()
register: func (c *Client) OnKill(fn func(*Client, packet.EventKill)) → func()
register: func (c *Client) OnEmoticon(fn func(*Client, packet.EventEmoticon)) → func()
register: func (c *Client) OnHookedBy(fn func(*Client, packet.EventHookedBy)) → func()
register: func (c *Client) OnWeaponChange(fn func(*Client, packet.EventWeaponChange)) → func()
```
ex: `c.OnChat(func(c *Client, e packet.EventChat){ c.SendChat("re: "+e.Msg) })`

`OnX` registrar per event in §I.catalog (presence/motion/transient/game). same shape: `func(*Client, packet.EventX) → func()` (event structs live in `packet`, C4).

### event catalog — DDNet research (task 2)

msg-derived (0.6 ids ← `net6/constants.go`; 0.7 ← net7 protocol):
```
id|src msg (0.6)|fields|requested
E_chat       |MsgGameSvChat 3 (m_Team -2..3, m_ClientId -1..N)|team,cid,msg|! chat
E_servermsg  |MsgGameSvChat 3 cid=-1 |msg                        |! global server msg
E_whisper    |0.6 DDNet SvChat m_Team=TEAM_WHISPER_SEND/RECV (≥2); 0.7 SvChat mode=WHISPER|fromID,toID,msg|! whisper (see V15)
E_broadcast  |MsgGameSvBroadcast 2  |text                       |. broadcast
E_motd       |MsgGameSvMotd 1       |text                       |. motd
E_killmsg    |MsgGameSvKillMsg 4    |killer,victim,weapon,modeSpecial|. kill
E_emoticon   |MsgGameSvEmoticon 10  |clientID,emoticon          |. emote (others)
E_weaponpickup|MsgGameSvWeaponPickup 9|weapon                   |. pickup notify
E_soundglobal|MsgGameSvSoundGlobal 5|soundID                    |. global sound
E_tuneparams |MsgGameSvTuneParams 6 |tuning floats              |! feeds physics.Tuning → prediction (V9)
E_voteset    |MsgGameSvVoteSet 15   |timeout,desc,reason        |! vote start (timeout>0)
E_votestatus |MsgGameSvVoteStatus 16|yes,no,pass,total          |. vote tally
E_voteoptions|MsgGameSvVote* 11-14  |option list add/rem/clear  |. votable-option menu
```
chat unify: 0.6 SV_CHAT = `team,cid,msg`; 0.7 SV_CHAT = `mode,cid,targetID,msg` (mode NONE/ALL/TEAM/WHISPER). 1 msg → split to E_chat / E_servermsg(cid=-1) / E_whisper(mode=WHISPER) by reader. handle in T4b. V17.

sys-msg-derived (ids ← `net6/constants.go:37`):
```text
id|src msg|fields|requested
E_rcon_line     |MsgSysRconLine 11      |line                |! rcon console output
E_rcon_auth     |MsgSysRconAuthStatus 10|authed,level        |. rcon auth on/off
E_rcon_cmd_list |MsgSysRconCmdAdd/Rem 25/26|cmd,help,params  |? rcon cmd completion
E_server_error  |MsgSysError 24         |msg                 |. server error
```
DDNet ext-msg (UUID NETMSGTYPE_EX, src `datasrc/network.py` NetMessageEx) — ship v1, each → own event:
`Sv_TeamsState`(team membership), `Sv_KillMsgTeam`, `Sv_YourVote`, `Sv_RaceFinish`(0.6 ext + maps 0.7), `Sv_Record`, `Sv_DDRaceTime`, `Sv_CommandInfo`/`Sv_CommandInfoRemove`(+GroupStart/End), `Sv_VoteOptionGroupStart`/`End`, `Sv_ChangeInfoCooldown`, `Sv_MyOwnMessage`, `Sv_MapSoundGlobal`.
NOTE: team/player flags = net-OBJECTS not messages (`DDNetCharacter`/`DDNetPlayer` ext snap obj), see snap-ext below.

0.7-only Sv messages (← `sixup_translate_game.cpp`; in 0.6 these are snap-OBJECTS or absent → V17 reader normalizes to SAME event):
```text
0.7 msg|0.6 equivalent|→ event
Sv_ClientInfo  |ObjClientInfo snap appear|E_player_join (+name,clan,skin,team)
Sv_ClientDrop  |ObjClientInfo snap gone  |E_player_leave (+reason — 0.6 has no reason)
Sv_SkinChange  |ObjClientInfo diff       |E_skin_change
Sv_Team        |DDNet team (Sv_TeamsState)|E_team_set (your/all team)
Sv_GameInfo    |ObjGameInfo snap         |E_game_info (rules/flags)
Sv_GameMsg     |— (0.7 only system text) |E_game_msg (win/lose/teamswap/round-end)
Sv_ServerSettings|—                      |E_server_settings (kick/spec/teams allowed)
Sv_RaceFinish  |DDRaceTimeLegacy/ext     |EventRaceFinish (exists)
```

snap-derived — needs full-snap tracking. today client tracks own char only (`localCID`, `client/snap.go:106`). ! extend `SnapStorage` → `map[clientID]CharacterState` + prev-snap copy → diff. fields ← `CharacterState` (`client/snap.go:44`), objs ← `net6/constants.go:101`.

A. presence / visibility (diff char-id set across snaps):
caveat: "sight" = membership in snap char set. server-dependent — vanilla culls by snap-distance, DDNet usually sends all in-team. ⊥ pure client guarantee; doc as "in snapshot" not literal LOS.
```text
id|detect|requested
E_player_enter_sight|cid ∈ now snap, ∉ prev (char obj appears)|! enters sight
E_player_leave_sight|cid ∈ prev, ∉ now (char obj gone)|! leaves sight
E_player_join       |ObjClientInfo cid new|. roster join
E_player_leave      |ObjClientInfo cid gone / PlayerInfo Local=0 drop|. roster leave
```
B. visible-char motion / state (diff `CharacterState` per cid):
```text
id|detect|requested
E_hookedby      |∃ other .HookedPlayer == localCID (prev≠→now=)|! someone hooks you
E_weaponchange  |my .Weapon changed|! server changed my weapon
E_player_move   |.X|.Y changed (? threshold px to throttle per-tick)|! visible player moves
E_player_jump   |.Jumped bit transition|. jump
E_player_dir    |.Direction changed (-1/0/1)|. dir change
E_player_attack |.AttackTick increased|. fired weapon
E_player_weapon |any .Weapon changed|. weapon swap (others)
E_player_hook   |.HookState/.HookedPlayer transition — classify: idle/flying/attached, grab(0→cid), release(cid→0), unhook-me|. hook state (generalizes hookedby/grab)
E_player_emote  |.Emote changed|. emote
E_player_hp     |.Health/.Armor changed (vanilla only; DDRace HP frozen)|? dmg (vanilla-only)
```
C. transient world-event objs (obj present this snap; one-tick or short-lived):
```text
id|src obj|payload|requested
E_explosion  |ObjExplosion 14 |x,y|. explosion
E_spawn      |ObjSpawn 15     |x,y|. spawn fx
E_death      |ObjDeath 17     |x,y,clientID|. death (player died)
E_hammerhit  |ObjHammerHit 16 |x,y|. hammer hit
E_sound_world|ObjSoundWorld 19|x,y,soundID|. world sound
E_projectile |ObjProjectile 2 new |x,y,vel,type,owner|. someone fired projectile
E_laser      |ObjLaser 3 new      |from,pos,owner|. someone fired laser
E_damage_ind |NetEvent DamageInd  |x,y,angle|. dmg indicator (took/dealt dmg)
E_finish     |NetEventEx Finish (ext)|—|. finish fx (DDNet)
```

D. game / flag / round state (diff `GameInfoState` / `ObjGameData` / `ObjFlag`):
caveat: 0.6 `GameInfo` flags ≠ 0.7 game-state encoding — reader ! normalize both → same E_round_state. V17.
```text
id|detect|requested
E_round_state |GameStateFlags change (warmup/paused/gameover/roundover)|. round flow
E_score_change|ObjPlayerInfo .Score delta|. score
E_flag        |ObjFlag 5 carrier/pos delta (CTF: grab/drop/capture)|. ctf flag
E_spectarget  |ObjSpectatorInfo target change|. spectate target
```
E. snap-ext objects (DDNet NetObjectEx, parsed by UUID — extend snap decode):
```text
id|src ext-obj|detect|requested
E_freeze        |DDNetCharacter .m_FreezeEnd/.m_FreezeStart change|. freeze begin/end
E_player_flags  |DDNetCharacter .m_Flags change (solo/collision/hook/etc)|. ddnet char flags
E_jumps_change  |DDNetCharacter .m_Jumps/.m_JumpedTotal|? jump count
E_player_auth   |DDNetPlayer .m_AuthLevel change (admin/mod login)|. auth level
E_player_afk    |DDNetPlayer .m_Flags afk/paused/spec bit|? afk/pause
E_spec_char     |SpecChar ext obj pos (spectated free-view)|? spec pos
```
scope: FULL — A + B(all) + C + D + E ship v1. no deferral.

### client prediction — FULL DDNet antiping
predict ALL entities (every char + projectiles + lasers + pickups), not own char only. mirror DDNet `CGameWorld` predicted world. own char driven by buffered local inputs; others extrapolated (no input avail). reconcile whole world on each snap. smooth to hide reconcile jumps.
```
type: PredictedWorld (client) — holds physics.Core per char + projectile/laser sim; ticks all forward
flow: snap @ acked tick Tack → seed world (all chars from snap CharacterCore, projectiles/lasers from objs)
      → tick world Tack→predTick: own char uses inputs[Tack..predTick]; others extrapolate (hold dir/hook/vel, run Core.Tick w/ predicted input)
      → predicted states for all cids
own:    inputs[tick] from ring buffer → exact (V9)
others: no input → DDNet rule: reuse last-seen intended dir/jump/hook/fire, run Core.Tick; lower accuracy, snap corrects
api:  func (c *Client) PredictedCharacter() CharacterState           // local, predicted
api:  func (c *Client) PredictedCharacters() map[int]CharacterState   // all visible cids, predicted
api:  func (c *Client) PredictedProjectiles() []ProjectileState       // antiping projectiles
api:  func (c *Client) WithPrediction(bool) Option / WithAntiping(bool) Option
dep:  physics.NewCore(col,pos), Core.Tick(physics.Input), physics.NewCollision(map), Tuning ← E_tuneparams
ref:  DDNet src/game/client/prediction/ (CGameWorld::Tick, CCharacter::Tick, CProjectile),
      gameclient.cpp OnNewSnapshot reconcile + smoothing (m_aClients[].m_Predicted, antiping smooth)
```
needs: ring buffer sent `physics.Input` keyed by tick (extend `inputRecord`, `predicted_time.go:105`); all-char snap map (T5); per-tick full-world re-sim; Tuning from tuneparams.

DDNet model (verified `gameclient.cpp`, `prediction/gameworld.cpp`):
- TWO worlds: `m_GameWorld` (snap-seeded, evolved authoritative) + `m_PredictedWorld` = `CopyWorld(m_GameWorld)` then `.Tick()` looped tick→predTick (`gameclient.cpp:2161,2219`). keep `m_PrevPredictedWorld` (`:2192`) for smoothing.
- per-client store `m_Predicted` + `m_PrevPredicted` core (`:2227`). render pos = `mix(m_PrevPredicted.Pos, m_Predicted.Pos, intraTick)` (`:2285`).
- `AntiPingPlayers()` = SEPARATE toggle from base `Predict()` — predict-self always, predict-others only if antiping on (`:2062`). ∴ `WithPrediction`(self) ⊥ `WithAntiping`(others) split correct.
- `WorldConfig` flags: `m_PredictWeapons`, `m_PredictFreeze`, `m_PredictTiles`, `m_PredictDDRace`, `m_IsVanilla`/`m_IsDDRace` (`gameworld.h:76`, `gameclient.cpp:2828`). prediction physics differs vanilla vs DDRace → config from game-type (GameInfoEx).
- smoothing gated `m_ClAntiPingSmooth` w/ pos-error + tick-bound checks (`:2271`).
smoothing: on reconcile lerp prev→new predicted over window. ⊥ teleport visible.

### consumer / agent interface (tick-driven, protocol-unified)
Two roles, ONE shared TickState. MANY view-only Observers + exactly ONE view+action Controller (V20, V31). Consumers ⊥ see protocol version (V18). Drive off PREDICTED state each tick.
```
type: Observer interface — view only, MANY allowed; plug via AddObserver(o) → remove()
  Mode() TickMode
  Observe(c *Client, st TickState)            // ingest predicted tick (render / ML training); NO actions
type: Controller interface — view + action, exactly ONE; plug via SetController(ctrl) / WithController(ctrl)
  Mode() TickMode
  OnTick(c *Client, st TickState) []Action    // observe predicted tick → emit actions (ML policy / user input)

obs: TickState — COMPLETE observable+predicted state for one tick (V19), self-contained:
  Tick, IntraTick float (sub-tick for smooth render, V21)
  LocalID
  Players   map[int]CharacterState       // predicted pos/vel/hook/weapon/freeze/flags, all visible cids (ONE char type, V25)
  Projectiles []ProjectileState          // predicted (T9b)
  Lasers, Pickups, Flags                  // visible entities
  Map       *MapView                      // full static map: all layers + tune-zone (T14, V28)
  Tuning       physics.Tuning             // default (zone-0) server tuning
  ActiveTuning physics.Tuning             // tuning resolved at LOCAL self tile (tune-zone, V29)
  SelfTuneZone int                        // tune-zone index at self
  GameInfo, RaceTime, Spectating
  Events    []packet.Event               // events since last tick (chat/kill/etc)
  (self weapon/health/armor/ammo live in Players[LocalID] — CharacterState, no dup V25)

act: Action — unified action set (V22), protocol-independent. covers full ddnet+0.7 client:
  ActInput{PlayerInput}          // move/aim/jump/hook/fire/wantedweapon
  ActChat{Team bool, Msg}        ActWhisper{ToID, Msg}
  ActEmoticon{Emoticon}          ActKill
  ActVote{Yes/No}                ActCallVote{Type, Value, Reason}
  ActSetTeam{Team}               ActSetSpectator{TargetID}
  (each maps to existing net6/net7 send; reader-side unify like events V17)
api: func (c *Client) Do(a Action) error   // apply one action (UI input path = ML output path)

render view: MapView + TickState = everything a UI needs to draw predicted+visible world.
ML view: same TickState = observation vector; Action = policy output. train + exec identical plug.
smoothing (T10a, now in-scope V21): keep prev+cur PredictedWorld; SmoothedCharacters(intraTick) lerps
  prev→cur per cid for render between ticks. ref DDNet mix(m_PrevPredicted,m_Predicted,intraTick).

dual cadence (V24) — each consumer declares mode; ONE driver loop dispatches to all (V31):
  TickModeFixed : per predicted tick (50Hz, IntraTick=0). ML/training. deterministic.
  TickModeFrame : per render frame; IntraTick∈[0,1) from wall-clock; positions smoothed. UI/render.
clean impl: ONE canonical builder `buildTickState(tick) TickState` (IntraTick=0). build once per
  (tick,intra) and share across all consumers of that cadence. frame overlays SmoothedCharacters(intra)
  + IntraTick. ⊥ duplicate state-assembly. observers get state; controller also returns actions → Do.
plug: two role interfaces (V20), NOT one Frontend — `Observer{ Mode() TickMode; Observe(*Client, TickState) }`
  (view-only, many) + `Controller{ Mode() TickMode; OnTick(*Client, TickState) []Action }` (view+action, one).
  `RunFrontends(ctx)` = one builder, two thin loops; headless/ML & UI share everything except cadence wrapper.
```

### MapView — environment / collision observation (T14)
spans the COMPLETE local map (downloaded on connect / cached), NOT the snapshot-visible region (V26). map is fully available offline → window can sit anywhere.
```
type: MapView (client) — queryable static map over the WHOLE map. ALL DDNet special-tile layers, not just collision (V28).
api:  Width, Height int                       // full-map tile bounds
api:  Tile(tx,ty int) TileClass               // Air|Solid|Unhook|HookThrough|Death|Freeze|Tele|Speedup|Switch|Tune|...
api:  Solid/Unhook/HookThrough/Death/Freeze/Tele/Speedup/Switch(tx,ty) bool   // OOB → Solid
api:  TuneZone(tx,ty int) int                 // tune-zone index from map Tune layer (0=default); drives position tuning (V29)
api:  IsDDRace() bool                          // map has DDRace features (tele/speedup/switch/tune layer or freeze tile); selects WorldConfig (V10b)
api:  Window(cx,cy,halfW,halfH int) []TileClass            // fixed (2halfW+1)×(2halfH+1) crop centered (cx,cy); OOB padded Solid
src:  twmap LayerKind {Game,Front,Tele,Speedup,Switch,Tune} (no new decode — twmap already parses these)
```

### ML observation (T20) — ego-centric fixed window
recommended design (V27), answering "what makes sense for ML":
```
shape: FIXED multi-channel tensor [C,H,W] every tick (ML needs constant input dims). config size;
       DEFAULT square N×N tiles (e.g. 64×64); rectangle allowed (TW motion horizontal-biased → wider ok).
center: ego-centric on predicted self tile (translation-invariant → generalizes across map). arbitrary
       center also allowed (full map local).
pad:   out-of-bounds tiles = Solid (wall), never variable size.
size:  speed/context knob — bigger = more lookahead, slower train; smaller = faster.
res:   tile-resolution (32px/tile) natural; downsample optional.
planes (EVERYTHING available, V28):
  static (MapView.Window): solid, unhook, hookthrough, death, freeze, tele, speedup, switch, tune-zone(index)
  per-tile tuning (V30): ONE plane per tuning param (gravity, ground/air control+accel+friction, jump
    impulses, hook length/fire/drag, velramp, …); cell value = TuningAt(tile) of that tile's zone.
    → model sees the physics each tile imposes, can predict movement on tiles it is about to consume.
    unknown per-zone values → default(zone-0) fallback; tune-zone index plane still distinguishes zones.
  dynamic (rasterize TickState): self, other players, projectiles, lasers, pickups, flags, hook lines, doors/draggers (ext)
scalars (per-tick, appended to obs):
  self weapon (one-hot), self health/armor/ammo, self vel, self hook state
  ACTIVE tuning vector at self tile (V29), self tune-zone index, race time, game state flags
```

### server password + optional login params (T29)
connect to password-protected servers + make login params optional with DDNet/TW defaults. wire already supports the password — `SysInfo(version, password)` in both readers. redesign moves skin/country/password OFF the positional `Login` signature into variadic options; only name+clan stay positional.
```
Session.Login: func Login(ctx, name, clan string, opts ...packet.LoginOption) error   // net6 & net7
type: packet.LoginConfig { Skin string; Country int; Password string }   // unset → defaults
type: packet.LoginOption func(*LoginConfig)
ctors: packet.WithLoginSkin(s) / WithLoginCountry(n) / WithLoginPassword(pw) ; ApplyLoginOptions(opts...)→cfg
defaults (packet consts, applied when omitted): DefaultName "nameless tee", DefaultSkin "default", DefaultCountry -1
register: func WithPassword(pw string) Option            // → packet.WithLoginPassword in NETMSG_INFO; empty = unprotected
register: func WithPlayerInfo(name,clan,skin string,country int) Option   // identity; New seeds Default* when unset
flow: Connect→Login(name,clan, WithLoginSkin(skin),WithLoginCountry(country)[,WithLoginPassword(pw)]) → SysInfo(version,pw)
  (net6 & net7, protocol-unified C2). wrong/missing pw on a protected server → CTRL_CLOSE "Wrong password" →
  DisconnectReason{Kind:WrongPassword} (V34). password+identity held on Client, reused across reconnect (V33).
  ⊥ logged in cleartext.
```

### server capabilities (T33)
DDNet servers announce capabilities in a `NETMSG_EX` sys message `capabilities@ddnet.tw` (`~/Desktop/Development/ddnet` `src/engine/shared/protocol_ex_msgs.h:33`), sent BEFORE `MAP_CHANGE`. payload = `Version int, Flags int`; flags ← `SERVERCAPFLAG_*` (`protocol_ex.h:33`): DDNET=1<<0, CHATTIMEOUTCODE=1<<1, ANYPLAYERFLAG=1<<2, PINGEX=1<<3, ALLOWDUMMY=1<<4, SYNCWEAPONINPUT=1<<5. absent (never sent before MAP_CHANGE) → all-false (vanilla/old).
```
type: ServerCapabilities (packet) — Version int; DDNet, ChatTimeoutCode, AnyPlayerFlag, PingEx, AllowDummy, SyncWeaponInput bool
event: EventServerCapabilities{Caps ServerCapabilities}   // emitted on parse (reader)
api: func (c *Client) Capabilities() ServerCapabilities    // last parsed; zero-value before/none
api: func (c *Client) OnServerCapabilities(fn func(*Client, packet.EventServerCapabilities)) func()
```
parse: net6 `processEx` UUID dispatch (`net6/events_ex.go`) → read 2 ints → ServerCapabilities; store on Session, expose via interface. net7/sixup sends none → zero caps. drives timeout-code gating (T24, ChatTimeoutCode).

### remote console — rcon (T30–T32)
log into rcon, send commands, react to log lines. wire + inbound parsing ALREADY present — `SysRconAuth(pw)`/`SysRconCmd(cmd)` (`net6/messages.go:121,129`, net7 equiv) + events `EventRconLine`/`EventRconAuth`/`EventRconCmd` (T4c, x). MISSING = the client-facing API + auth-state + re-auth on reconnect.
```
register: func WithRconPassword(pw string) Option   // auto rcon-login after each (re)connect
api: func (c *Client) RconLogin(ctx, pw string) error   // SysRconAuth → await EventRconAuth(on)/level; err on reject/timeout/ctx
api: func (c *Client) Rcon(cmd string) error            // SysRconCmd; err ErrNotAuthed if !RconAuthed()
api: func (c *Client) RconAuthed() bool                 // current auth state (from EventRconAuth, cleared on disconnect)
api: func (c *Client) OnRconLine(fn func(*Client, packet.EventRconLine)) func()   // react to console output
api: func (c *Client) OnRconAuth(fn func(*Client, packet.EventRconAuth)) func()   // auth on/off + level
api: func (c *Client) OnRconCmd(fn func(*Client, packet.EventRconCmd)) func()     // cmd-list add/rem (completion)
flow: SysRconAuth(pw) → server replies RCON_AUTH_ON+level (EventRconAuth) on success, else an EventRconLine error.
  authed → Rcon(cmd)=SysRconCmd. server output streams as EventRconLine → OnRconLine handler reacts (may issue more Rcon).
  session-level send wrappers: SendRconAuth/SendRconCmd on net6 & net7 Session (protocol-unified, V43).
```

### reconnect / timeout-code / ban (T22–T28)
resilient connection on top of existing `Connect`/`Reconnect`/`Close` + `packet.EventClose` (C6). identity (name/clan/skin/country) + timeout code held on `Client`, reused across every reconnect.
```
type: DisconnectReason — classified CTRL_CLOSE (T23)
  Kind  DisconnectKind   // Closed|Kicked|Banned|TimedOut|ShuttingDown|Full|WrongPassword|Unknown
  Text  string           // raw server reason (verbatim)
  BanDuration time.Duration // parsed when Kind=Banned & finite; 0 = unknown/permanent
type: Backoff — PLUGGABLE wait schedule (interface); user may supply own impl
  Next() time.Duration   // wait before next attempt; advances internal state
  Reset()                // return to initial delay; called after a successful connect
type: ExponentialBackoff — DEFAULT impl (unexported fields; build via ctor, V41)
  Next() doubles each consecutive retry: 1s,2s,4s,…,capped at Max=1h, then stays 1h.
  the 1h cap IS the steady-state poll interval between reconnect/unban tries.
type: ReconnectPolicy — drives auto-reconnect (T26); built via ctor/options (V41), not raw literal
  fields: MaxAttempts (0=∞), Backoff, ResumeWithTimeout — set through options, sane zero-value default.
ctors (V41 — no raw-struct init in the public contract):
  func NewExponentialBackoff(base time.Duration, factor float64, max time.Duration) *ExponentialBackoff
  func DefaultBackoff() Backoff                       // = NewExponentialBackoff(1s, 2, 1h)
  func NewReconnectPolicy(opts ...ReconnectOption) ReconnectPolicy   // functional options, matches Client Option idiom
  func DefaultReconnectPolicy() ReconnectPolicy       // ∞ attempts, DefaultBackoff(), ResumeWithTimeout=true
  opts: WithMaxAttempts(int), WithBackoff(Backoff), WithResumeTimeout(bool)
  func NewDisconnectReason(raw string) DisconnectReason   // classifier ctor (reader-side, T23); ⊥ user-built
register: func WithTimeoutCode(code string) Option   // DDNet resume token; empty → auto-gen random stable code
register: func WithReconnectPolicy(p ReconnectPolicy) Option   // default ON (DefaultReconnectPolicy); customize via NewReconnectPolicy(...)
register: func WithoutAutoReconnect() Option                   // disable auto-reconnect
api: func (c *Client) TimeoutCode() string                       // current code (stable, V32)
api: func (c *Client) Reconnect(ctx) error                       // existing method, now timeout-aware: reuses identity+stable code → resumes tee (DDNet 0.6); non-DDNet/0.7 = fresh (V37)
api: func (c *Client) ResetTimeoutCode(code ...string)          // set code (or regenerate if omitted/empty) → next Reconnect gets a FRESH tee instead of resuming (V32). (no dumb ReconnectWithTimeout wrapper — resume is intrinsic to Reconnect)
auto: AUTOMATIC — not a method. On a server-initiated drop the client itself starts a reconnect loop bound to the context passed to `Connect` (default ON). Reconnects on the `Backoff` schedule (default 1s→×2→cap 1h); Banned retries = unban polls; until connected or `MaxAttempts`. Cancelling the Connect context, or `Close()`, aborts retries promptly (V39) and `Close` sends a clean CTRL_CLOSE (V40). ⊥ a dumb `AutoReconnect(ctx)` method — resume/reconnect is intrinsic to the client lifecycle.
api: func (c *Client) OnDisconnect(fn func(*Client, DisconnectReason)) func()   // callback on CTRL_CLOSE (V38)
api: func (c *Client) LastDisconnect() DisconnectReason          // last classified disconnect
```
timeout-resume flow (DDNet 0.6 only, T22/§R VERIFIED): after entergame send chat command `/timeout <code>` (NOT a netmsg — `SendChat("/timeout "+code)`). server stores the code per player; on drop keeps the tee (SetTimeoutProtected); reconnect re-sends `/timeout <code>` → server matches code → reclaims the timed-out tee → position/hook/race resume server-side + tuning re-sent. local snap+prediction reset (`Connect`, V9), race re-syncs from first snap. 0.7/sixup cannot reclaim → vanilla/0.7 degrade to fresh tee (V37). cap-gating (`SERVERCAPFLAG_CHATTIMEOUTCODE`) not parsed yet → sent best-effort when resume enabled (`?`).
ban flow: CTRL_CLOSE reason → `DisconnectReason`. Banned → auto-reconnect keeps retrying on the `Backoff` schedule (default 1s,2s,…,cap 1h); each retry doubles as an unban poll (server may lift ban early) — first attempt with no CLOSE ends the wait + `Backoff.Reset()`. Banned+finite duration MAY seed the first wait at ≥ duration. unknown/permanent ban → retry until `MaxAttempts` (0=∞) then give up.
shutdown: every wait + the connect attempt itself `select` on `ctx.Done()`; ctx cancel returns promptly (V39). graceful stop sends a clean CTRL_CLOSE disconnect to the server (V40), so the tee is NOT left for the timeout path (timeout-resume is for UNEXPECTED drops, not deliberate quit).

### performance — hot-path optimization (T34–T40)
scope = LIBRARY client only (`packer`,`packet`,`net6`,`net7`,`physics`,`client`); ⊥ `cmd/racebot` (separate effort). method = MEASURE-then-cut: bench + pprof FIRST, optimize only profile-proven hot paths, re-bench to confirm. public API + behavior unchanged (V48); every existing test still green.
```
harness (T34): table benches w/ -benchmem, ⊥ new deps (testing.B only):
  packet:  BenchmarkProcessSnap / BenchmarkApplyDelta (full + empty delta; realistic 64-char snap)
  packer:  BenchmarkUnpackInt / BenchmarkGetString / BenchmarkPackInt+PackStr+PackMsgID
  net6/7:  BenchmarkProcessMessage (snap chunk → event)
  client:  BenchmarkPredictTick (PredictedWorld.Tick) / BenchmarkBuildTickState / BenchmarkDeriveEvents (snap.go diff)
  pprof:   `go test -bench . -benchmem -cpuprofile -memprofile` per pkg; record baseline alloc/op + ns/op.
profile (T35): rank top alloc-sites + CPU hot fns from pprof; record measured top-N here. ⊥ optimize unmeasured.
```
measured candidate hot paths (pre-profile, confirm in T35):
```
loc|cost|fix
packet/snap.go:231 applyDelta updated-item lookup|O(numUpdated × result.Items) linear scan = O(n²)/tick|index map cid→idx, O(1) (DDNet CSnapshot item hashtable)
packer NewUnpacker (73 sites)|make([]byte,len)+copy per inbound message|reuse pooled/Reset Unpacker per session reader; ⊥ alloc+copy per msg
packet/snap.go:221 absFields make([]int,size)|per updated item per tick|retained → keep alloc, but size from ItemSizeFn (no GetInt); ? small-int slab
packer PackInt/PackStr/PackMsgID|fresh []byte per field on build|append into a reused builder buffer (AppendInt(dst,n)/AppendStr); builders concat into one buf
packer GetStringSanitized:104 var buf []byte|grow-by-append, realloc churn|preallocate by RemainingSize(); single []byte→string at end
client/snap.go derive* (:283-285,charactersCopy:190)|intermediate []Event per sub-diff + map copy per tick|append into one evs (cap=prev len); reuse prev-map by swap not realloc
```
measured (T35, profiled @ 64-char snap — baselines, see commit T34):
```
path|ns/op|B/op|allocs/op|profile finding|task
packet applyDelta|17.3µs|17099|72|38.8% CPU cum (self+GetInt+Varint); 99.7% of pkg allocs. 64× absFields make([]int) (RETAINED) + O(n²) updated-item scan|T36
packet ProcessSnap|18.0µs|17099|72|= applyDelta + map retention|T36
packer NewUnpacker|83ns|256|1|per inbound msg make+copy; ×(msgs/tick). pooled Reset variant already 0-alloc|T37
packer PackInt/PackMsgID|26/28ns|8|1|per packed field on SEND path|T38
client deriveEvents|13.1µs|7024|136|59% pkg allocs; mostly packet.Event interface boxing (INHERENT, V48) + 3 sub-slice + per-tick maps|T39 (bounded)
client charactersCopy|6.9µs|13608|67|fresh map per observation build|T39 (bounded)
physics Core.Tick|217ns|0|0|0-alloc, CPU-only; NOT an opt target (V49)|—
```
chosen targets (V49): T36 (applyDelta O(1) index + single absFields backing array) = top ROI; T37 (pool Unpacker on snap path); T38 (Append* on send path); T39 (slice-cap + per-tick map reuse, bounded — event boxing inherent). physics excluded (no alloc, not flagged).
results (T40, before → after, allocs/op is the headline; ns/op noisy on event-boxing benches):
```
path|allocs before→after|B before→after|note
packet applyDelta|72 → 9|17099 → 18125|O(n²) scan → O(1) index; 64 absFields make → 1 backing array (T36)
packet ProcessSnap|72 → 9|17099 → 18130|= applyDelta (T36)
net6/7 snap parse (per msg)|1 → 0|256 → 0|NewUnpacker → reused snapUnpacker.Reset; see packer NewUnpacker(256/1) vs UnpackerReset(0/0) (T37)
SysInput build (50Hz send)|6 → 0|88 → 0|Pack* per field → Append* into one buf (T38)
packer GetStringSanitized|2 → 1|128 → 64|NUL-scan fast path, direct convert when clean (T38)
client deriveEvents|136 → 130|7024 → 6016|evs prealloc + append-into derive*; residual = inherent packet.Event boxing (T39, bounded)
```
all library pkgs (packer/packet/net6/net7/physics/client) green incl `-race`; behaviour unchanged (V48). cmd/ml fails under -race on a go4.org/unsafe/assume-no-moving-gc go1.26 dep panic — out of scope (cmd/ harness), pre-existing, unrelated to perf edits.
DDNet/TW perf refs: snapshot item hashtable for O(1) item lookup (`snapshot.cpp` `CSnapshot::GetItemIndex`); fixed MAX_SNAPSHOT_SIZE preallocated buffers, ⊥ per-tick heap; varint packed into caller-owned buffers (`AppendVarint`). Go: `sync.Pool` for transient scratch (already in `deltaScratch`), preallocate slice cap, avoid `[]byte`↔`string` copies, escape-analysis (`go build -gcflags=-m`) to keep hot locals on stack.

### snap storage size (T41)
configurable retained-snapshot window for delta decompression. `packet.SnapStorage.MaxSnaps` (delta-base ring buffer, `packet/snap.go:64,83`) is HARDCODED to 16 in `NewSnapStorage` — expose as a client+session option. targets `packet.SnapStorage` (delta ring), ⊥ `client.SnapStorage` (per-player CharacterState tracking, `client/snap.go`) — distinct types, same name.
```
packet:  func NewSnapStorage(itemSizeFn func(int) int, opts ...SnapStorageOption) *SnapStorage   // variadic, backward-compat (no opt = default)
         func WithMaxSnaps(n int) SnapStorageOption     // sets+validates MaxSnaps in ctor (V41); clamp invalid → default/min
         default MaxSnaps = 16 (UNCHANGED when no opt)
session: func WithSnapStorageSize(n int) Option         // net6 & net7 Session ctor opt (protocol-unified C2); stored on Session
register: func WithSnapStorageSize(n int) Option        // Client; plumbs to session reader's packet.SnapStorage.MaxSnaps
```
plumb: `Client.WithSnapStorageSize(n)` → `net6/net7 NewSession(WithSnapStorageSize(n))` (in `Client.newSession`) → stored on Session → `StartReader` builds `packet.NewSnapStorage(SnapItemSize, WithMaxSnaps(n))` (`net6/reader.go:42`, `net7/reader.go:42`). net7 itemSizeFn stays nil. unset → 16.
bounds: n ≤ 0 → default 16. n below the live delta-window min (server deltas against a recently-acked snap; purge at `snap.go:113-127` keys off `MaxSnaps`) → clamp UP, else the base the server deltas against gets purged → decode fails (V53).

### configurable buffer sizes (T43–T45)
generalize the WithSnapStorageSize/WithMaxSnaps pattern (V53/I.snapsize) to EVERY source-derived buffer whose capacity is a hardcoded default lifted from the original (DDNet/TW) client. each gets a variadic option; default = the CURRENT hardcoded value (opt-in, behavior + tests unchanged, V48-style); ctor-validates + clamps (V54). wire-format constants are NOT buffers and are EXCLUDED (V55) — options size LOCAL memory/queues only, never wire layout.
```
predInputRingSize (client/prediction.go:12, = 256, DDNet local-input history ring):
  register: func WithPredInputRingSize(n int) Option    // Client
  change: predInputBuffer.ring [256]predInput fixed ARRAY → []predInput slice sized at construction;
    index stays tick % len(ring); New() seeds the buffer (nil slice → mod-by-zero panic, so MUST init).
  clamp: n ≤ 0 → 256; 0 < n < min → min (min ≥ a few ticks of horizon so re-sim base is retained).
reader eventCh buffer (net6/reader.go:41 + net7/reader.go:41, = 128):
  session: func WithEventChanSize(n int) Option          // net6 & net7 (protocol-unified C2)
  register: func WithEventChanSize(n int) Option         // Client → plumbed via newSession
  use: StartReader make(chan packet.Event, n). clamp: n ≤ 0 → 128; 0 < n < min → min (min ≥ 1).
UDP read buffer (network/conn.go:70 SetReadBuffer(2*1024*1024)):
  network: func WithReadBufferSize(n int) DialOption      // same shape as WithReadTimeout/WithWriteTimeout
  session: func WithReadBufferSize(n int) Option          // net6 & net7 → network.Dial(..., WithReadBufferSize(n))
  register: func WithReadBufferSize(n int) Option         // Client → plumbed via newSession
  use: udp.SetReadBuffer(n). clamp: n ≤ 0 → 2MB default (OS further clamps to rmem_max; ⊥ forced min).
```
plumb: same Client → Session → {reader | network.Dial} path as V53 (`Client.newSession` forwards each option to net6/net7 `NewSession`). every default UNCHANGED when the option is unset.
NOT tunable (V55, wire-format — changing breaks the protocol): `MaxPacketSize` 1400, `MaxSequence` 1024, `HeaderSize` 7 / `HeaderSizeConnless` 9, `TokenRequestDataSize` 512, net7 `AntiReflectionSize`. keepalive (2s) / reack (500ms) intervals = timing, out of scope here (`?` — add later if wanted).

### master server list + server info (T46–T47)
fetch the DDNet master server list over HTTPS+JSON, and query a single server's info CONNLESS (no login). package `master` exposes a `Client` built with `New(...)` (⊥ package-global request funcs, V64); requests are methods on it, master selection driven by a pluggable `RequestPolicy` whose default replicates DDNet's CChooseMaster (fastest-validated, cached). net6/net7 own the connless request/framing + parse helpers, so `master` composes them and never hand-rolls wire bytes (V59/V60). stdlib only — `net/http` + `encoding/json` + `crypto/tls` default (C1: ⊥ new deps).
```
master (new pkg; imports net6/net7 + network + packet; ⊥ imported by them — no cycle):
  DDNet masters (HTTPS, failover): master1.ddnet.org … master4.ddnet.org, path /ddnet/15/servers.json (? "15" = master proto ver, confirm)
  type PlayerInfo  — Name, Clan string; Country, Score int; IsPlayer bool   // a server's CURRENT client (player or spectator)
  type ServerInfo  — Name, GameType, MapName string; Passworded bool; NumPlayers, MaxPlayers, NumClients, MaxClients int; Clients []PlayerInfo
  type Address     — Version packet.Version (06/07); Host string; Port int   // parsed from "tw-0.6+udp://host:port" / "tw-0.7+udp://…"
  type ServerEntry — Addresses []Address; Location string; Info ServerInfo
  json: tolerant decode — unknown fields ignored (forward-compat); an address with an unknown scheme is SKIPPED, ⊥ fails the whole list (V56).
CLIENT (constructor, ⊥ package-global request funcs — V64):
  type Client — holds masters []string, *http.Client, RequestPolicy, default query timeout; ALL request entry points are methods on it.
  func New(opts ...Option) *Client            // defaults: DefaultMasters, http timeout DefaultHTTPTimeout, ChooseFastest policy (DDNet default), DefaultQueryTimeout
  opts: WithMasters([]string), WithHTTPClient(*http.Client), WithRequestPolicy(RequestPolicy), WithQueryTimeout(time.Duration)
  func (c *Client) FetchServerList(ctx) ([]ServerEntry, error)            // delegate master selection to the policy; return first VALID (decodable, non-empty) list; all fail → err (V56)
  func (c *Client) FetchServerListFrom(ctx, url string) ([]ServerEntry, error)   // single explicit master, bypass policy
  func (c *Client) QueryServerInfo(ctx, version packet.Version, addr string) (ServerInfo, error)   // connless, no session (V57); uses Client default query timeout
  func ParseAddress(s string) (Address, bool)  // STAYS package-level — pure stateless parser, ⊥ Client state
REQUEST POLICY (how FetchServerList selects among masters, V64) — interface drives the strategy, ⊥ just an order:
  type RequestPolicy interface {
    // Fetch runs ONE FetchServerList against the masters using try (fetch+validate one master URL),
    // returns the first valid list or an error if all fail. Implementations may be stateful (cache best).
    Fetch(ctx, masters []string, try func(ctx context.Context, url string) ([]ServerEntry, error)) ([]ServerEntry, error)
  }
  func ChooseFastest() RequestPolicy  // DEFAULT — REPLICATES DDNet CChooseMaster (§R): probe masters in RANDOM order CONCURRENTLY, first VALIDATED response wins (fastest healthy), CACHE that master index, reuse it on later calls, RE-PROBE on failure of the cached one. validation = try returns nil err + a decodable non-empty list.
  func RoundRobin()    RequestPolicy  // each call starts at the next index (shared atomic cursor → spread load), then failover through the rest
  func Failover()      RequestPolicy  // always [0,1,…,n-1] (master1 first), sequential
  feasibility (§R VERIFIED): DDNet masters master1…4 are INTERCHANGEABLE replicas — CChooseMaster picks the fastest VALIDATED one, ⊥ merges; any one yields the full list. so ChooseFastest/RoundRobin over them return equivalent lists. NB: no cross-master sync in mastersrv source (sv_register_url defaults to master1) → "shared state" is a DDNet DEPLOYMENT property; for CUSTOM WithMasters the caller picks the policy. every policy still recovers when a master is down (ChooseFastest re-probes, RoundRobin/Failover failover to the rest).
connless server info (query ONE server directly, no session/handshake/login):
  flow: Client.QueryServerInfo opens UDP (network.Dial), builds the connless getinfo via net6/net7 HELPERS, recv, strips
    connless framing via helpers, then decodes the body via net6/net7 ParseInfoResponse → ServerInfo (incl. Clients). ctx-bounded; ⊥ Login/Handshake (V57).
  master ⊥ hand-roll any wire bytes — connless framing/handshake comes from net6/net7; body decode via net6/net7 (V59/V60).
shared connless magics (packet, used by both protocols):
  var packet.ServerBrowseGetInfo = {255,255,255,255,'g','i','e','3'} ; packet.ServerBrowseInfo = {…,'i','n','f','3'}
net6 helpers (in net6/builder.go beside BuildConnect/BuildChunkPacket — same home as every other Build*):
  func BuildInfoRequestConnless(reqToken byte) []byte           // 6×0xFF + GETINFO + token (real TW connless framing, ⊥ the header connless bit)
  func ConnlessInfoPayload(datagram []byte) ([]byte, bool)      // strip 6×0xFF + verify INFO magic → body after magic
net7 helpers (in net7/builder.go beside BuildTokenRequest/BuildConnect):
  func BuildInfoRequestConnless(serverToken, clientToken packet.Token, reqToken int) []byte   // Header{Connless}.Pack + GETINFO + PackInt(reqToken)
  func ParseTokenResponse(datagram []byte) (packet.Token, bool) // extract server token from the NET_CTRLMSG_TOKEN reply (shares Handshake's offsets)
  func ConnlessInfoPayload(datagram []byte) ([]byte, bool)      // strip 9-byte connless header + verify INFO magic → body
  (BuildTokenRequest already exists; QueryServerInfo 0.7 = BuildTokenRequest → ParseTokenResponse → BuildInfoRequestConnless)
  body decode stays in master (parseInfo6 decimal-string ints / parseInfo7 varint ints) — returning master.ServerInfo there would cycle, so ⊥ in net6/net7.
```
the master `info.clients` array IS the current player list per server (name/clan/country/score/is_player) — surfaced as `ServerInfo.Clients`; same struct returned by `QueryServerInfo` so callers get one shape from either source.

### input robustness — panic-free public API (T69–T70)
exported funcs guard against unexpected caller input (V70). known gap: `physics.NewCollision(nil)` / `client.NewMapView(nil)` deref `m.GameLayers()` → nil panic. policy: config → clamp (V62), parse/IO → error, structural nils → safe empty (nil map = empty all-solid world). hostile-input tests (nil, "", negative, oversized, truncated/garbage wire bytes) per package assert no panic + a sane error/clamp.

### documentation — godoc for the whole public surface (T60–T68)
every EXPORTED identifier in the shipped pkgs (`packet`,`packer`,`network`,`net6`,`net7`,`physics`,`client`,`master`) carries an idiomatic godoc comment; protocol/physics behavior cites the DDNet or teeworlds-0.7 source it mirrors; each package ships runnable `Example` functions. ~790 exported symbols, currently 0 examples.
```
godoc style: comment STARTS with the identifier name ("Connect …", "Client is …", "MaxSnaps bounds …") per Go convention; first sentence = one-line summary (shows in `go doc`); deprecations marked `Deprecated:`.
package overview: each pkg gets a doc comment (a `doc.go` or the top file) — what it is, where it sits in the dep chain (§A), when to use it.
source refs: cite the upstream we mirror as `repo path:symbol`, e.g. DDNet `src/engine/shared/snapshot.cpp CSnapshot::GetItemIndex`, teeworlds-0.7 `src/engine/shared/network.cpp CNetBase::SendPacketConnless`, `datasrc/network.py`. ⊥ invented refs — only files verified in §R / the cloned trees.
examples: Go `Example`/`ExampleType_method` funcs in `*_test.go`, COMPILE + run under `go test`; deterministic ones use `// Output:`. small + comprehensible: build a Client, register a callback, fetch the master list, query one server, run a physics tick, pack/unpack a value. network examples that need a server are `// Output:`-free (compile-only) or skip.
verify: `go doc ./<pkg>` shows docs; `go test ./...` runs examples; a coverage check asserts no exported symbol lacks a doc comment.
```

## §V — invariants

- V1: new event types ! implement `packet.Event` (`eventTag()`), emitted via `packet.SendEvent` on `EventCh()`. `packet/event.go`.
- V2: callbacks fire serial in `eventLoop` goroutine, receive `*Client`. ⊥ block long → stalls event drain. Doc: handler ! return fast or spawn own goroutine. handler may call `c.SendChat`/`c.SendInput` etc — ⊥ dispatch while holding `c.mu` (release before invoke).
- V3: register/unregister ! concurrency-safe (mutex) — caller registers from any goroutine while `eventLoop` reads.
- V4: ∀ requested event reachable in both 0.6 & 0.7, OR documented version-only. ⊥ silent 0.7 gap.
- V5: snap-derived events ! computed in `Client.handleEvent` `EventSnapshot` case by diff vs prev `CharacterState`; need stored prev snap + myClientID.
- V7: unregister closure idempotent — 2nd call no-op, ⊥ panic.
- V9: prediction seeds predicted world from acked snap @ ack tick (all chars from CharacterCore, projectiles/lasers from objs), re-sims forward to predTick. own char uses buffered local inputs[ack..predTick]. ⊥ seed from already-predicted state (no cross-snap drift).
- V9a: others (cid≠local) predicted by extrapolation — no input avail, reuse last-seen intent (dir/jump/hook/fire) run `Core.Tick`. accuracy < own; ⊥ claim authoritative. snap reconcile corrects each tick.
- V9b: predicted world uses `Tuning` from latest `E_tuneparams` (default/zone-0); on tune-zone maps, per-char tuning resolved by zone (V29). ⊥ stale tuning → physics mismatch vs server.
- V10: predicted world reconciles to authoritative snap on each `EventSnapshot` — all predicted states ! converge to server snap @ acked tick (own error ≤ rounding; others ≤ extrapolation err). ⊥ permanent divergence.
- V10a: reconcile jumps smoothed — rendered pos lerps prev-predicted → new-predicted over short window (DDNet antiping smooth, `gameclient.cpp:2271`). ⊥ visible teleport on correction.
- V10b: prediction physics config per game-type — `WorldConfig`{PredictWeapons,PredictFreeze,PredictTiles,PredictDDRace,IsVanilla,IsDDRace} from GameInfoEx. vanilla ≠ DDRace sim. ⊥ DDRace freeze/tele predicted on vanilla server (& vice-versa).
- V11: prediction+antiping opt-in via Option (`WithPrediction`/`WithAntiping`); disabled → `PredictedCharacter()`==`Character()`, `PredictedCharacters()`==raw snap. ⊥ silent behavior change for existing callers.
- V12: snap-derived events need prev + cur full-snap char map. `SnapStorage` ! hold `map[cid]CharacterState` (all players, not just localCID) + prev copy. diff computed in `EventSnapshot` handler under `c.mu`, dispatched after unlock (V2).
- V13: presence events edge-triggered: enter/leave fire once on set-membership change, ⊥ repeat each snap while present. throttle `E_player_move` (? min px delta) to avoid per-tick flood.
- V14: transient-obj events (explosion/death/spawn/hammerhit) fire once per snap they appear; ⊥ dedup across snaps (objs already one-tick). map snap obj → event in same `EventSnapshot` pass.
- V15: whisper unified — ∀ source → identical `packet.EventWhisper{FromID,ToID,Msg}`. sources: 0.6 DDNet `Sv_Chat m_Team`∈{TEAM_WHISPER_SEND,TEAM_WHISPER_RECV} (≥2); 0.7 `Sv_Chat m_Mode==CHAT_WHISPER`. (vanilla 0.6 teeworlds: none — DDNet adds via m_Team.) consumer ⊥ see protocol diff.
- V15a: 0.7 obj-as-message normalize — 0.7 `Sv_ClientInfo`/`Sv_ClientDrop`/`Sv_SkinChange`/`Sv_Team`/`Sv_GameInfo` carry data that in 0.6 lives in snap OBJECTS (ClientInfo/GameInfo). reader maps BOTH → same event (E_player_join/leave/skin_change/team_set/game_info). ref `sixup_translate_game.cpp`. test: join fires on 0.6 & 0.7.
- V16: full event scope — ∀ §I.catalog rows (vanilla + DDNet-ext + snap-derived A/B/C/D/E) implemented. ⊥ silent skip; unimpl → explicit `?`-flagged + §T row.
- V17: protocol-unified events (generalizes V15 to whole catalog). ONE event struct per logical event, defined once (`packet`). net6 & net7 readers both emit it. consumer/callback code ⊥ branch on `version`, ⊥ see net6/net7 types. event present in only 1 protocol → documented version-only in §I + `?`. snap-derived events identical (snap format shared post-decode). test: same handler fires on both 0.6 & 0.7 server for shared events.
- V18: consumer interface protocol-unified (extends V17 to actions). `Action` set & `TickState` identical regardless of 0.6/0.7. `c.Do(Action)` maps to the active session's send. consumer/Frontend ⊥ branch on version, ⊥ see net6/net7 types.
- V19: `TickState` self-contained & complete — ∀ data a consumer needs for one tick present: predicted local+all chars, projectiles, visible entities, MapView (collision env incl unhookable tile positions), tuning, game/race state, events-since-last-tick. ⊥ require consumer to call back for missing state. built from PREDICTED world (V9), not raw snap.
- V20: two consumer roles, ONE shared `TickState`/observation path. `Observer` = view-only (`Observe(c,st)`, no actions) — MANY may plug (renderers, ML-training data collectors). `Controller` = view + action (`OnTick(c,st)[]Action`) — exactly ONE (the actor / ML policy). both share the same per-tick state; ⊥ separate API per use case. controller action path == `c.Do(Action)`. ⊥ multiple controllers (avoids conflicting input); replacing the controller is allowed. registry concurrency-safe.
- V21: smoothing IN-SCOPE (supersedes B3 deferral — render consumer now exists). keep prev+cur `PredictedWorld`; `TickState.IntraTick` + `SmoothedCharacters(intra)` lerp prev→cur per cid. render ⊥ teleport between ticks. headless-only consumers may ignore (intra=0 == V10/predicted).
- V22: `Action` covers full ddnet + 0.7 client action set — movement/aim/jump/hook/fire/weapon, chat, team chat, whisper, emoticon, kill, vote, call-vote, set-team, spectate. each ! map to a net6 AND net7 send (or documented version-only + `?`). missing action → `?`-flag + §T row.
- V24: dual cadence, single builder. driver supports `TickModeFixed` (50Hz, IntraTick=0, ML) & `TickModeFrame` (render rate, IntraTick∈[0,1), smoothed). BOTH go through ONE `buildTickState(tick)`; frame mode only overlays `SmoothedCharacters(intra)` + IntraTick on top. ⊥ duplicate/divergent TickState assembly per mode. consumer `Mode()` selects cadence; everything else shared.
- V31: ONE driver loop dispatches each tick to ALL observers + the single controller, each per its `Mode()` (fixed consumers on new predicted tick, frame consumers per render frame). per-consumer observation scope (window size, planes) is CONSUMER-side: each crops its own view from `TickState.Map`/entities — ⊥ global obs config. only the controller's returned actions are applied via `Do`; observers ⊥ act. ⊥ build TickState more than once per (tick,intra) — share across consumers of the same cadence.
- V25: ONE canonical type per concept — ⊥ redundant parallel structs/consts. character = `CharacterState` (snapshot AND predicted; ⊥ separate `PredictedCharacter` type). sim char = `physics.Core` (convert only at seed/extract). position: `physics.Vec2` (sim float) ↔ int X/Y (wire/snap) at ONE conversion site. input: `packet.PlayerInput` (wire/Action) ↔ `physics.Input` (sim) via single `inputToPhysics`. weapon ids: `packet.Weapon` is source; `physics` mirror = SOLE documented exception (layer isolation, ⊥ packet import), ⊥ any further dup. tuning: `physics.Tuning` canonical, `EventTuneParams.Raw` = wire form decoded once. new code ! reuse canonical, ⊥ reinvent.
- V26: `MapView` spans the COMPLETE local map (downloaded/cached), ⊥ limited to snapshot-visible region. out-of-bounds tile query → `Solid` (world border, matches collision). `Window` crops a FIXED-size chunk at any center, OOB padded Solid.
- V27: ML observation window FIXED-size ∀ ticks (constant ML input shape) — config W×H tiles (default square), ego-centric on predicted self, OOB padded Solid, multi-channel (static map planes + dynamic entity planes). ⊥ variable-size or visible-only crop. ⊥ rebuild map collision per tick (map static — query the one MapView).
- V28: observation completeness — obs exposes EVERYTHING available for the tick: ALL static map layers (collision, freeze, death, tele, speedup, switch, tune-zone), ALL dynamic entities (self, players, projectiles, lasers, pickups, flags, doors/ext), AND agent scalars (current weapon, health/armor/ammo, velocity, hook, active tuning vector, tune-zone, race/game state). ⊥ silently omit an available entity/layer. unavailable item → documented `?`, not dropped.
- V29: position-dependent tuning — tuning may differ per DDNet tune-zone. `MapView.TuneZone(tx,ty)` from map Tune layer; `Client.TuningAt(tx,ty)`/`ActiveTuning` resolve it. default tuning ← `Sv_TuneParams` (zone 0). per-zone tuning VALUES ⊥ reliably on wire (server-side `tune_zone` config) — `?`; model still observes the zone INDEX + resulting trajectory, so it can learn zone behavior. prediction uses zone tuning when known, else default; ⊥ assume single global tuning on DDRace maps with tune zones.
- V30: per-tile tuning in observation — obs window includes ONE plane per tuning param, each cell = `TuningAt(tile)` (the tile's zone tuning), so the model sees the physics every tile imposes and can predict movement on tiles it will consume. piecewise-constant per zone. unknown per-zone values → default(zone-0) fallback; the tune-zone-index plane (V28) still separates zones. ⊥ expose only self-tile tuning when full-window per-tile tuning is the observation goal.

- V32: timeout code STABLE per `Client` — generated once (`WithTimeoutCode` or auto-gen) and reused on EVERY (re)connect. ⊥ regenerate per session (new code orphans the timed-out tee → no resume). DDNet-only; vanilla sends no timeout msg (V37).
- V33: reconnect preserves identity — name/clan/skin/country + timeout code carried across `Reconnect`/auto-reconnect. resumed tee continues server-side position + race time; local snap/prediction reset on `Connect` (V9) and race time re-syncs from the first post-reconnect snap. ⊥ lose identity on reconnect.
- V34: disconnect classified — CTRL_CLOSE reason (`packet.EventClose{Reason}`) → `DisconnectReason{Kind,Text,BanDuration}`. ban detected by reason-text match; duration parsed when present else 0 (unknown/permanent). raw text preserved verbatim. ⊥ silently drop reason (current `client/client.go:499` drops it — T23 fixes).
- V35: ban-aware reconnect — auto-reconnect on `Kind=Banned` keeps retrying on the `Backoff` schedule; each retry IS the unban poll (no separate poll knob — the backoff cap = poll interval). a reconnect that completes without CLOSE ends the wait and calls `Backoff.Reset()`. ⊥ retry faster than the backoff delay. honors ctx cancel (V39).
- V36: pluggable bounded backoff — `ReconnectPolicy.Backoff` is an interface (`Next()/Reset()`); user may supply any impl. default = `ExponentialBackoff{Base 1s, Factor 2, Max 1h}`: delays 1s,2s,4s,…,capped at 1h. auto-reconnect honors `MaxAttempts` (0=∞). ⊥ infinite tight loop; ⊥ hardcode the schedule (must go through `Backoff`). each attempt = one `Connect`; success → `Reset()`.
- V37: timeout RESUME = DDNet-only, documented version-only. on a vanilla server the feature degrades to plain reconnect (fresh tee, no resume) — ⊥ assume resume on non-DDNet. detection/wait/poll (V34,V35) still apply on all servers.
- V38: `OnDisconnect` fires from the event path (serial, V2) on CTRL_CLOSE, before any reconnect attempt; handler ⊥ block the reconnect loop (return fast / spawn own goroutine). registry concurrency-safe like other callbacks (V3,V7).
- V39: fully ctx-aware + abortable — EVERY blocking point in auto-reconnect (each `Backoff` wait, ban wait, and the `Connect` attempt itself) `select`s on `ctx.Done()`. cancel returns promptly with `ctx.Err()` (⊥ `time.Sleep` that ignores ctx, ⊥ unkillable wait). a sleeping/waiting reconnect must abort within ~one scheduler tick of cancel.
- V40: graceful shutdown = clean disconnect — on ctx cancel (or explicit `Close`), the client sends a CTRL_CLOSE disconnect to the server (`net6/session.go:96`, `net7/session.go:85`) before teardown, so a deliberate quit ⊥ rely on the timeout path and ⊥ leave a dangling server-side tee. timeout-resume (V32,V33) covers UNEXPECTED drops only. Close idempotent + safe under concurrent shutdown.
- V41: construct config types via CONSTRUCTORS, ⊥ raw struct literals. `Backoff`/`ReconnectPolicy`/`DisconnectReason` built through `New…`/`Default…` (+ functional `ReconnectOption`s) — same idiom as existing `Client` `Option`s. concrete impls keep fields unexported so a zero/partial literal can't bypass invariants (e.g. `ExponentialBackoff` with base 0 → busy-loop). ctor validates + applies defaults. applies to NEW reconnect types; ⊥ regress existing types.
- V47: server capabilities parsed — DDNet `capabilities@ddnet.tw` NETMSG_EX (Version+Flags) decoded in `net6/processEx`, stored on Session, exposed `Client.Capabilities()` + `EventServerCapabilities`. flags per `SERVERCAPFLAG_*`. not received (sent before MAP_CHANGE; vanilla/0.7 omit) → zero-value caps (all false). timeout-code send (V32/T24) gates on `ChatTimeoutCode`. ⊥ assume DDNet caps on a server that never sent them. caps arrive BEFORE MAP_CHANGE during the synchronous login handshake (reader not up) → MUST be captured in `recvUntilMapChange` via `ExtractAllSysMsgPayloads` (every EX, not first) + seeded into `Client.caps` from `sess.Capabilities()` in `Connect` (B5). ⊥ rely on the event-only path for the initial caps.
- V42: server password — `WithPassword(pw)` plumbs through `Connect`→`Login`→`SysInfo(version, pw)` (net6 `:223` & net7 equiv; both already param'd). protocol-unified (C2); empty = unprotected. wrong/missing pw on a protected server → CTRL_CLOSE classified `WrongPassword` (V34). password carried across reconnect like other identity (V33). ⊥ emit password in logs/errors (cleartext leak).
- V43: rcon protocol-unified — `SysRconAuth`/`SysRconCmd` sent on BOTH net6 & net7; inbound `EventRconLine`/`EventRconAuth`/`EventRconCmd` are shared event structs (V17, parsed T4c). consumer/callback ⊥ branch on version, ⊥ see net6/net7 rcon types.
- V44: rcon cmd requires auth — `Rcon(cmd)` errors (`ErrNotAuthed`) when `!RconAuthed()`. auth state derived from `EventRconAuth` (on/off+level), cleared on disconnect (CTRL_CLOSE / reader EOF). ⊥ send `SysRconCmd` before auth confirmed.
- V45: rcon re-auth on reconnect — `WithRconPassword` held on `Client`, re-sent after EACH (re)connect like identity (V33); ⊥ silently stay unauthed post-reconnect. rcon password ⊥ cleartext-logged (as V42).
- V46: rcon reactions serial — `OnRconLine`/`OnRconAuth`/`OnRconCmd` fire from the event path (serial, V2); handler ⊥ block; MAY call `c.Rcon(...)` (dispatch after mu release, V2). registry concurrency-safe (V3,V7).
- V48: perf work ⊥ change public API or OBSERVABLE behavior — optimization only. ∀ existing tests pass UNCHANGED (incl `-race`); no signature/type/event/wire change. a perf change that needs a behavior change is out of scope (escalate, ⊥ silently alter).
- V49: optimize only PROFILE-PROVEN hot paths — pprof/-benchmem ranks the target FIRST. each optimized path has a committed `Benchmark*` (with `-benchmem`); baseline (before) + result (after) recorded in §PERF/commit. ⊥ claim a speedup w/o a bench delta; ⊥ speculative micro-opt of cold code.
- V50: snap delta item lookup O(1) — `applyDelta` resolves updated-item → existing-item via an index map (`itemKey`→idx), ⊥ O(numUpdated × items) linear scan (current `snap.go:231`). mirrors DDNet item hashtable. result identical to linear version (test parity).
- V51: bounded alloc on steady-state hot paths — per-tick (snap decode, prediction tick, tickstate build, event diff) and per-message (unpack) paths reuse pooled/Reset buffers + preallocate slice cap; ⊥ unbounded per-call `make`. data RETAINED past the call (snapshot `Fields`, emitted events) is still freshly allocated/copied out — measured by `allocs/op` not zeroed blindly.
- V52: pooled scratch ⊥ alias retained state — anything stored beyond the call (in a `Snapshot`, `TickState`, event) is COPIED out of pooled/`Reset` buffers before the buffer is reused or returned to the pool. ⊥ use-after-free / cross-tick aliasing. (safety corollary of V51; `-race` + parity tests guard.)
- V53: snap storage size configurable — `packet.SnapStorage.MaxSnaps` (delta-base ring window) settable via `WithSnapStorageSize(n)` plumbed Client→net6/net7 Session→`NewSnapStorage(WithMaxSnaps(n))` (V41 ctor-validated, ⊥ raw literal mutation of MaxSnaps in the public path). default 16 UNCHANGED when unset (opt-in only, V48-style — existing behavior + tests identical with no opt). invalid `n ≤ 0` → default; `n` below the live delta-window min → clamp UP so purge (`snap.go:113-127`, keyed off MaxSnaps) ⊥ drop the base the server deltas against (too small → "snap: apply delta" decode failure). protocol-unified (net6+net7, C2). targets `packet.SnapStorage` (delta ring) ⊥ `client.SnapStorage` (per-player state).
- V54: source-derived buffer sizes configurable — every hardcoded buffer/queue capacity lifted from the original client (`predInputRingSize` 256, reader `eventCh` 128, UDP read buffer 2MB) is settable through a variadic option, same idiom as V53. each option is ctor-validated (V41): `n ≤ 0` → the original default (so unset == default, opt-in); `0 < n < min` → clamped UP to a safe floor. default UNCHANGED when unset → existing behavior + tests identical (V48-style). options plumb Client→Session→{reader | network.Dial} like V53. ⊥ raw-literal bypass; ⊥ a default that differs from the original value.
- V55: wire-format constants ⊥ tunable — protocol-fixed sizes stay constant: `MaxPacketSize` 1400, `MaxSequence` 1024, net7 `HeaderSize` 7 / `HeaderSizeConnless` 9, `TokenRequestDataSize` 512, `AntiReflectionSize`. these define wire layout / sequence space; a config option changing them would break interop. V54 options resize LOCAL memory/queues only — never a value the peer parses.
- V56: master list over HTTPS+JSON — `master.FetchServerList` GETs a DDNet master (`/ddnet/15/servers.json`), decodes `servers`, returns `[]ServerEntry`. stdlib only (`net/http`+`encoding/json`, C1). context-bounded (honors ctx cancel/deadline). failover: try masters in order, return the first success; all-fail → error. decode TOLERANT — unknown JSON fields ignored (forward-compat); an `addresses` entry with an unparseable/unknown scheme is skipped, ⊥ failing the whole list. each `Address` carries `packet.Version` so 0.6/0.7 are distinguished. `ServerInfo.Clients` = the server's current player/spectator list.
- V57: server info WITHOUT a play session — `master.QueryServerInfo` opens only a UDP socket and exchanges CONNLESS packets (uses the existing connless header flag), sends NO Handshake/Login/Ready, and returns parsed `ServerInfo` (incl. `Clients`). ctx-bounded with timeout. version-aware (net6 vs net7 connless info build/parse differ). result struct identical to the master-list `ServerInfo` so either source yields one shape.
- V58: browser is read-only + side-effect-free on the game client — `master` package neither creates a `client.Client`/session nor mutates connection state; it only reads (HTTP GET, connless query). callable standalone before/without ever connecting to play.
- V59: `master` composes net6/net7 + packer helpers — it ⊥ hand-roll protocol wire bytes (no literal header/connless/token byte construction, no raw BE token packing). connless getinfo packets come from `net6.BuildInfoRequestConnless`/`net7.BuildInfoRequestConnless`, the 0.7 token handshake from `net7.BuildTokenRequest`/`net7.ParseTokenResponse`, framing strip from `*.ConnlessInfoPayload`, and body decode from `packer` (GetString/GetInt). connless magics are shared in `packet` (one definition, ⊥ duplicated per package). protocol packet builders live with their peers in `net6/builder.go` + `net7/builder.go` (same home as `BuildConnect`/`BuildTokenRequest`/`BuildChunkPacket`) — ⊥ a separate ad-hoc builder location. consolidation invariant (cf. V19/V25): one tested framing path, reused, never re-implemented in a consumer.
- V60: packet build/parse OWNERSHIP follows the §A dep direction (`client`,`master` → `net6`,`net7` → `network`,`packer` → `packet`). low-level varint/string codec ⊂ `packer`. version-AGNOSTIC shared structs + their decoders (result types, cross-version msg parse) ⊂ `packet` (foundation, imports no internal pkg). version-SPECIFIC wire build (`Build*`) + parse (`process*`/`Parse*`) ⊂ `net6`/`net7`, beside their reader/builder. raw transport ⊂ `network` (⊥ protocol). a CONSUMER (`client`,`master`) ⊥ decode wire bytes/fields itself — ⊥ loop `packer.GetInt/GetString` over a server payload; it calls a protocol-pkg parser and gets back a typed struct. shared RESULT types live in `packet` so net6/net7 return them without importing a consumer (⊥ cycle). corollary + REFINES V59: connless server-info BODY decode is version-specific → belongs in `net6.ParseInfoResponse` / `net7.ParseInfoResponse` returning `packet.ServerInfo`, ⊥ `master` (the V59 "body decode in master via packer" clause is superseded — master orchestrates, ⊥ decodes).
- V61: bot/dummy is SERVER-SIDE only — NO client may self-advertise as a bot in any supported protocol (researched teeworlds 0.7 + DDNet 0.6). 0.7: `IsClientBot` = `CPlayer::IsDummy()`, `Dummy=true` set SOLELY by the server `dbg_dummies` debug spawner (`gamecontext.cpp:1675`, `CONF_DEBUG`); every network connect is `Dummy=false` (`server.cpp:888`); no CONNECT/`CL_STARTINFO` field carries a bot flag (the only client-set connect attribute is spectator, `ConnectAsSpec`). 0.6/DDNet: no bot concept at all. ⇒ a "advertise-as-bot" client option is a no-op and is ⊥ added (no wire field to carry it). connless serverinfo per-client flag carries PLAYER/SPECTATOR only — 0.7 `0=player,1=spectator` (`server.cpp:1121`), 0.6 DDNet `1=player,0=spectator`; bot is NEVER emitted (the `// bot=2` source comment is legacy/aspirational). `packet.PlayerInfo.IsPlayer` reflects exactly this wire bit; bot is unrepresented because the protocol omits it. revisit only if a server variant is found to actually emit flag `2`.
- V62: every configurable DEFAULT is an EXPORTED constant (or var/func) named `Default<Thing>`, in the package that owns the option, so a library user can read it and pass it back through the matching `With…` option. clamp floors are exported as `Min<Thing>`. ⊥ unexported `default<Thing>`/`min<Thing>` consts; ⊥ inline magic-number defaults in constructors. covered: `network.DefaultReadTimeout` (✓), `network.DefaultReadBufferSize`; `packet.DefaultMaxSnaps` + `packet.MinMaxSnaps`; `client.DefaultPredInputRingSize` + `client.MinPredInputRingSize`; `net6.DefaultEventChanSize` + `net7.DefaultEventChanSize`; `master.DefaultQueryTimeout` + `master.DefaultHTTPTimeout`. already-conforming: `packet.DefaultName/DefaultSkin/DefaultCountry`, `client.DefaultBackoff()`/`DefaultReconnectPolicy()`. companion to the no-env rule (V63): defaults are explicit, public, env-free.
- V68: LOGIN must survive packet loss — vital + control msgs are RETRANSMITTED during connect, ⊥ aborted on first read-timeout (B6). The background reader/resender starts only after Login, so Handshake + the recv-loops (`recvUntilMapChange`/`recvUntilConReady`/`recvUntilReadyToEnter`, net6 + net7) must themselves, on a recv timeout, RE-SEND the pending step (CONNECT in Handshake; the last vital INFO/READY in the loops) and retry — bounded by an overall connect deadline — rather than returning `i/o timeout`. Mirrors DDNet `CNetConnection::Update` (resend unacked vitals + CONNECT on a timer, §R). A single dropped handshake/login packet ⊥ fail the whole connect. (Independent of any client-side connect pacing, which only reduces self-induced loss and ⊥ cure real-network loss.)
- V69: every EXPORTED identifier in the shipped pkgs has an idiomatic godoc comment STARTING with its name (Go convention; first sentence = `go doc` summary); each package has an overview doc comment; protocol/physics behavior cites the upstream it mirrors (`repo path:symbol`, DDNet or teeworlds-0.7, only refs verifiable in §R / the cloned trees — ⊥ invented); each package ships runnable `Example` funcs that compile + pass under `go test` (deterministic → `// Output:`). a doc-coverage check asserts NO exported symbol lacks a comment. docs are TRUTH-tracking: a doc that names a symbol/flag must match current code (cf. recalled-memory caveat). ⊥ regress — documentation-only, no API/behavior change (V48-style).
- V70: exported entry points are PANIC-FREE for ANY caller input — every exported func/method/ctor/Option validates or safely handles unexpected input, ⊥ panics on hostile/garbage values. policy by input kind: (a) CONFIG sizes/durations → CLAMP to `Default*`/`Min*` (V62) — never panic, never a broken value; (b) PARSE / connect / I/O / address / version → return an ERROR (⊥ panic); (c) STRUCTURAL pointers/slices → GUARD: a nil `*twmap.Map` yields an empty all-solid world (`NewCollision`/`NewMapView`/`NewCore`), a nil/short byte slice yields a decode error (Unpacker/parse already), a nil option is ignored. each exported symbol DOCUMENTS its contract (what it does with bad input). reachable-from-API panic on any input = a bug (→ §B). cmd/ harness exempt. (extends V41/V62 clamp + the existing `PlayerInput.Set*` range checks + `ParseAddress` rejection to the WHOLE public surface.)
- V71: public API names are idiomatic Go. (a) plain accessor = `X()`, NOT `GetX()` — e.g. `Session.MapInfo()` (sibling of `Map()`/`MapName()`). (b) a stream/cursor reader that CONSUMES + advances uses `NextX()` (⊥ `GetX`) — `packer.Unpacker.NextInt/NextString/NextStringSanitized/NextRaw/NextByte/NextMsgAndSys` (it reads the next value and advances the cursor; mirrors a binary reader, ⊥ a field getter). (c) initialisms ALL-CAPS: `ID`/`UUID`/`HTTP`/`URL`/`CRC`/`UDP`/`TCP`/`IP` (`ClientID`, `CalculateUUID`, `WithHTTPClient`). (d) ⊥ package-name stutter (`packet.Snapshot`, ⊥ `packet.PacketSnapshot`). (e) constructors `New…`, options `With…`, defaults `Default…`/floors `Min…`. exceptions documented: `MapCache.GetOrWait` keeps `Get` (blocking fetch-or-wait, not a cursor read). naming-only — ⊥ behavior change (V48-style); a rename is a coordinated edit across decl + interface + all callers + tests + examples.
- V67: `RoundRobin` exhausts all masters within ONE FetchServerList before erroring — it starts at the rotating index (`cursor++`) and, on each failure, advances to the NEXT master in rotation `masters[(start+i)%n]` for i in [0,n), trying every master AT MOST once per call; returns the first success, or the wrapped last error once all n are exhausted (`errNoMasters` when n==0). across calls the start index advances (load spread). same exhaust-then-error contract holds for `Failover` (start fixed at 0) and `ChooseFastest` (cached → concurrent probe of all). (already implemented `master/policy.go`; this pins the guarantee + its test, V48.)
- V66: tests use `t.Context()` (or `b.Context()`) as the base context — NEVER `context.Background()` / `context.TODO()`. derive deadlines/cancels from it (`context.WithTimeout(t.Context(), d)`, `context.WithCancel(t.Context())`). `t.Context()` is cancelled during test cleanup (Go 1.24+, module is go1.26) → unblocks the code under test + ⊥ leaked contexts. applies to `*_test.go` only; the gitignored `cmd/` harness is exempt. a test deliberately exercising cancellation still derives its cancellable ctx from `t.Context()`.
- V65: shipped library code (`packer`/`packet`/`net6`/`net7`/`network`/`client`/`master`/`physics`) ⊥ `time.Sleep` — every wait is CONTEXT-AWARE + cancellable: `select { case <-ctx.Done(): … case <-timer.C: … case <-closeSignal: … }` (cf. V39 reconnect loop) or an equivalent that returns promptly on `ctx` cancel. ⊥ unconditional/busy-wait sleeps that ignore cancellation. `time.Sleep` is allowed ONLY in `*_test.go` (e.g. settling a concurrent probe) and in the gitignored `cmd/` harness (⊥ shipped). every blocking op takes a `context.Context` and honors it.
- V64: `master` exposes a `Client` via `New(...opts)` — request entry points (`FetchServerList`, `FetchServerListFrom`, `QueryServerInfo`) are METHODS, ⊥ package-global functions (`ParseAddress` stays a pure package-level parser). master selection is a pluggable `RequestPolicy` (interface, `WithRequestPolicy`); the DEFAULT replicates DDNet's `CChooseMaster` (§R): probe masters concurrently in random order, first VALIDATED (decodable, non-empty) response wins, cache the winning master, reuse it, re-probe on failure. `RoundRobin` + `Failover` are provided alternatives. masters are interchangeable replicas (§R) so any policy returns an equivalent list for the DDNet masters; for custom `WithMasters` the caller picks the policy. config via options + `Default*` consts only (V62/V63); ⊥ env. behavior identical to the prior global funcs for the default single-list result (V48-style: API moves to a Client, the returned data is unchanged).
- V63: the library reads NO environment variables — timeouts/sizes/addresses come ONLY from `With…` options + the exported `Default…` consts (V62). a caller wanting env-driven config reads the env itself and passes the option. ⊥ implicit `os.Getenv` in any shipped pkg (`packer`/`packet`/`net6`/`net7`/`network`/`client`/`master`/`physics`). (the old `TW_TIMEOUT` read in `network.Dial` is removed.)

## §T — tasks

```
id|status|task|cites
T2|x|research ddnet server events → §I event catalog finalized (this doc); whisper resolved V15|I.catalog
T3|x|define event structs (packet.EventChat…EventWeaponChange) impl packet.Event|V1,V4,I.catalog
T4|x|parse msg-derived events in net6/reader.go processPayload switch + net7 equiv → SendEvent|V1,V4,V15,C5
T4a|x|DDNet-ext msg (NETMSGTYPE_EX UUID) decode: teamsstate, killmsgteam, yourvote, racefinish, record, commandinfo(+group), votegroup, changeinfocooldown, myownmsg, mapsoundglobal → events|V4,V16,I.catalog
T4d|x|0.7 obj-as-msg unify: Sv_ClientInfo/ClientDrop/SkinChange/Team/GameInfo/GameMsg/ServerSettings → E_player_join/leave/skin_change/team_set/game_info/game_msg/server_settings; map to 0.6 snap-obj source|V15a,V17,I.catalog
T4e|x|DamageInd net-event (vanilla obj 20) → EventDamageInd in deriveTransient|V14,I.catalog
T4e2|x|UUID-ext snap-obj decode: DDNetCharacter(freeze/flags/jumps), DDNetPlayer(auth/afk), SpecChar, Finish via deriveExt (B1 resolved — no decoder change)|V14,I.catalog
T4b|x|chat/whisper unify: 0.6(team,cid,msg) & 0.7(mode,cid,targetID,msg) → E_chat/E_servermsg/E_whisper by mode|V15,V17,I.catalog
T4c|x|sys-msg events: rcon_line, rcon_auth, rcon_cmd_list, server_error (net6/reader.go sys switch)|V1,I.catalog
T5|x|SnapStorage: track map[cid]CharacterState all players + prev-snap copy (extend client/snap.go)|V12,C5
T5a|x|snap-derived core: hook-by, weapon-change(self), player enter/leave sight (edge-trig)|V5,V12,V13,I.catalog
T5b|x|snap-derived motion: player move(throttled)/jump/dir/attack/weapon/hook/emote for visible chars|V13,I.catalog
T5c|x|transient-obj events: explosion/spawn/death/hammerhit/sound + projectile/laser (new-obj detect)|V14,I.catalog
T5d|x|game/flag/round events: round-state, score, flag, spectarget|V16,I.catalog
T6|x|callback registry on Client: On[E] generic + OnX wrappers, unregister, mutex, dispatch in handleEvent|V2,V3,V7,I.api
T7|x|tests: registry concurrency, each event fires, unregister idempotent; cross-protocol — same event/handler fires on 0.6 & 0.7|V2,V3,V7,V17
T8|x|input ring buffer keyed by tick (extend inputRecord); capture local clientID from snap|V9,I.predict
T9|x|PredictedWorld: two-world (GameWorld snap-seed + PredictedWorld copy→Tick to predTick); own re-sim inputs; Tuning+WorldConfig from game-type|V9,V9b,V10b,V11,I.predict
T9a|x|antiping others: extrapolate non-local chars (reuse last intent, Core.Tick); PredictedCharacters() map|V9a,I.predict
T9b|x|projectile prediction via physics.Tuning.ProjectilePos + PredictedProjectiles() (laser is hitscan, no ballistic predict). B2 resolved|V9,I.predict
T10|x|reconcile whole world on EventSnapshot; expose PredictedCharacter()/PredictedCharacters(); converge|V10,I.predict
T10a|x|reconcile smoothing — RE-SCOPED (B3 reverted, V21): keep prev+cur PredictedWorld, SmoothedCharacters(intraTick) lerp for render|V10a,V11,V21,I.predict
T11|x|tests: own converges (≤rounding), others bounded-err, drift-free N ticks, smoothing no-teleport, disabled==raw|V9,V9a,V10,V10a,V11
T12|x|unified Action type (input/chat/whisper/emoticon/kill/vote/callvote/setteam/spectate) + c.Do(Action) → net6 & net7 send|V18,V22,I.consumer
T13|x|TickState observation struct: predicted local+all chars, projectiles, lasers/pickups/flags, tuning, game/race, events-since-tick|V19,I.consumer
T14|x|MapView: WHOLE-map queries — ALL layers (Solid/Unhook/HookThrough/Death/Freeze/Tele/Speedup/Switch) + TuneZone(tx,ty), OOB→Solid + Window crop, from twmap LayerKind{Game,Front,Tele,Speedup,Switch,Tune}|V19,V26,V28,I.mapview
T15|x|Observer (many, view-only) + Controller (one, view+action) interfaces; AddObserver→remove, SetController/WithController|V18,V20,V31,I.consumer
T16|x|tick driver: ONE buildTickState per (tick,intra) shared across consumers; dispatch to all observers + controller by Mode; apply controller []Action via Do|V19,V20,V21,V24,V31,I.consumer
T17|x|tests: Action↔send both protocols; TickState complete; both cadences share builder; one Frontend serves UI+ML plugs; MapView tiles + Window crop correct|V18,V19,V20,V22,V24
T19|x|consolidate redundant types: canonical CharacterState/Vec2/PlayerInput/Weapon/Tuning + single conversion sites; audit & remove dup impls; ⊥ phantom PredictedCharacter|V25
T20|x|ML observation: ego-centric FIXED multi-channel window — ALL static planes (collision+freeze+death+tele+speedup+switch+tune-zone) + per-tile tuning planes (TuningAt per cell) + ALL dynamic entity planes + agent scalars (weapon/hp/vel/hook/active-tuning/tune-zone); config size, square default, OOB=Solid|V26,V27,V28,V30,I.mapview,I.consumer
T21|x|position-dependent tuning: per-tune-zone tuning store; Client.TuningAt(tx,ty) over any tile/window; ActiveTuning; default←Sv_TuneParams; feed predicted world per char's zone; expose in TickState|V29,V30,V9b,I.consumer
T22|x|research DDNet timeout-code wire protocol: VERIFIED (§R) — `/timeout <code>` chat cmd post-entergame, cap-gated CHATTIMEOUTCODE, server SetTimedOut reclaim, 0.6-only|V32,V37,I.reconnect
T33|x|parse server capabilities: net6 processEx UUID capabilities@ddnet.tw → ServerCapabilities{DDNet,ChatTimeoutCode,…}; store on Session; Client.Capabilities() + EventServerCapabilities + OnServerCapabilities; net7 zero|V47,I.caps
T23|x|DisconnectReason via NewDisconnectReason(raw) ctor: classify CTRL_CLOSE reason (Closed/Kicked/Banned/TimedOut/ShuttingDown/Full/WrongPassword); parse ban duration from text; surface via handleEvent (fix EventClose drop at client/client.go:499)|V34,V41,I.reconnect
T24|x|timeout code: WithTimeoutCode option + auto-gen stable code + TimeoutCode(); send DDNet timeout msg after Login/join; reuse SAME code across reconnect|V32,V33,V37,I.reconnect
T25|x|timeout-aware Reconnect: existing Reconnect reuses identity+stable code → resumes tee (DDNet 0.6); ResetTimeoutCode() forces fresh; vanilla/0.7 degrade. (dropped redundant ReconnectWithTimeout wrapper)|V32,V33,V37,I.reconnect
T26|x|AUTOMATIC reconnect (default-on Option, NOT a method): WithReconnectPolicy/WithoutAutoReconnect; pluggable Backoff (Next/Reset) + ExponentialBackoff + ctors; NewReconnectPolicy(opts)/DefaultReconnectPolicy; on server drop client loops bound to the Connect ctx; MaxAttempts; Banned retries=poll; ctx-cancel/Close abort promptly; Close sends clean CTRL_CLOSE; ResetTimeoutCode for fresh|V35,V36,V39,V40,V41,I.reconnect
T27|x|OnDisconnect callback + LastDisconnect(); fire serial in event path before reconnect|V38,V2,V3,V7,I.reconnect
T28|x|tests: code stable across reconnect; reason classification + ban-duration parse; default backoff sequence (1s,2s,…,cap 1h) + custom Backoff injected + Reset on success; MaxAttempts bound; ctx-cancel aborts mid-backoff/mid-wait promptly; graceful shutdown sends clean CTRL_CLOSE; resume identity; vanilla degrade; ctors validate (base 0 rejected, defaults applied)|V32,V33,V34,V35,V36,V37,V38,V39,V40,V41
T29|x|server password: WithPassword option + plumb Connect→Login→SysInfo(version,pw) (net6 session.go:223 + net7 equiv); carry across reconnect; wrong-pw → WrongPassword reason; ⊥ log cleartext; test connect to pw server + wrong-pw classify|V42,V33,V34,I.password
T30|x|rcon client API: session SendRconAuth/SendRconCmd (net6+net7); RconLogin(ctx,pw) (await EventRconAuth on); Rcon(cmd) require authed (ErrNotAuthed); RconAuthed(); WithRconPassword auto-login|V43,V44,I.rcon
T31|x|rcon state + reactions: OnRconLine/OnRconAuth/OnRconCmd callbacks; track auth from EventRconAuth, clear on disconnect; re-auth after reconnect; ⊥ log pw cleartext|V44,V45,V46,V33,I.rcon
T32|x|tests: auth ok/reject, cmd-before-auth → ErrNotAuthed, log line → OnRconLine fires, re-auth after reconnect, both protocols|V43,V44,V45,V46
T34|x|bench harness (-benchmem, no new deps): BenchmarkApplyDelta/ProcessSnap (packet), UnpackInt/GetString/Pack* (packer), ProcessMessage (net6/7), PredictTick/BuildTickState/DeriveEvents (client); record baseline ns/op + allocs/op|V49,I.perf
T35|x|profile: cpuprofile+memprofile per pkg → rank top CPU fns + alloc sites; record measured top-N in §PERF; pick optimization targets (⊥ unmeasured)|V48,V49,I.perf
T36|x|applyDelta O(1) item index: replace linear updated-item scan (snap.go:231) with itemKey→idx map; parity test vs old result; bench delta|V50,V48,V49
T37|x|Unpacker reuse: pool/Reset across the 73 NewUnpacker sites (net6/net7 readers) — one buffer per session reader, ⊥ alloc+copy per inbound msg; verify no cross-msg aliasing|V51,V52,V48
T38|x|packer pack path: AppendInt/AppendStr/AppendMsgID into a reused builder buffer (keep PackInt etc as thin wrappers); GetStringSanitized preallocate buf by RemainingSize|V51,V48
T39|x|client per-tick alloc cut: snap.go derive* append into one evs (cap=prev len), swap prev/cur maps instead of realloc, trim charactersCopy churn|V51,V52,V48
T40|x|re-bench all (T34 harness); assert no regression + behavior unchanged (full suite + -race green); record after-numbers vs baseline|V48,V49
T41|x|snap storage size option: packet `NewSnapStorage(fn, ...SnapStorageOption)` variadic + `WithMaxSnaps(n)` ctor-validated clamp (default 16, min=delta-window); net6/net7 Session `WithSnapStorageSize` opt → StartReader; Client `WithSnapStorageSize` Option plumb via `newSession`; ⊥ change default behavior|V53,V41,I.snapsize
T42|x|tests: option sets MaxSnaps on both protocols; unset = 16 default; invalid (≤0)/too-small clamped; delta still decodes at configured size (parity vs default); ⊥ regress default|V53,V41,I.snapsize
T43|x|predInputRingSize configurable: WithPredInputRingSize(n) Client option; predInputBuffer.ring [256]array → []slice sized at New() (init so nil-slice ⊥ mod-by-zero); index tick%len(ring); default 256 clamp (≤0→256, <min→min); + tests (default/set/clamp, record→get round-trip at small size)|V54,V41,I.bufsize
T44|x|reader eventCh buffer configurable: WithEventChanSize(n) net6+net7 Session option + Client option plumbed via newSession; StartReader make(chan,n); default 128 clamp (≤0→128, <min→min); + tests both protocols (option→cap(reader.eventCh), default, clamp)|V54,V41,I.bufsize
T45|x|UDP read buffer configurable: network.WithReadBufferSize(n) DialOption (network/conn.go, like WithReadTimeout) + net6/net7 Session option + Client option plumbed via newSession → network.Dial; udp.SetReadBuffer(n); default 2MB clamp (≤0→2MB); + tests (DialOption sets field, default, plumb)|V54,V55,V41,I.bufsize
T46|x|master pkg: types (PlayerInfo/ServerInfo/Address/ServerEntry); FetchServerList(ctx)/FetchServerListFrom over DDNet masters /ddnet/15/servers.json; tolerant JSON decode; address "tw-0.6+udp://host:port" parse → {Version,Host,Port} (unknown scheme skipped); failover; WithHTTPClient/WithMasters opts; + tests (decode fixture incl clients player list, address parse, unknown-scheme skip, failover, ctx cancel)|V56,V58,I.master,C1
T47|x|connless server info (no session): net6/net7 BuildInfoRequestConnless + ParseInfoResponseConnless (version-aware, §R refs); master.QueryServerInfo(ctx,version,addr,opts) opens UDP only, connless request→response→ServerInfo incl Clients, ctx timeout, ⊥ Handshake/Login; + tests (build/parse round-trip from captured response bytes, no-session assertion)|V57,V58,I.master,C2
T48|x|shared connless magics + net6 helpers: packet.ServerBrowseGetInfo/ServerBrowseInfo (one def); net6/builder.go BuildInfoRequestConnless(reqToken byte) (6×0xFF framing) + net6 ConnlessInfoPayload(datagram)→(body,ok) strip+verify INFO; + tests (build bytes, strip round-trip, bad-magic rejected)|V59,I.master
T49|x|net7 connless helpers: net7/builder.go BuildInfoRequestConnless(serverToken,clientToken packet.Token,reqToken int) (Header{Connless}.Pack+magic+PackInt) + ParseTokenResponse(datagram)→(packet.Token,ok) (reuse Handshake offsets) + ConnlessInfoPayload(datagram)→(body,ok); + tests|V59,I.master,C2
T50|x|refactor master.QueryServerInfo onto helpers: 0.6 = net6.BuildInfoRequestConnless+ConnlessInfoPayload; 0.7 = net7.BuildTokenRequest+ParseTokenResponse+BuildInfoRequestConnless+ConnlessInfoPayload; body decode via packer stays in master (parseInfo6/7). ⊥ any literal wire bytes in master. behavior unchanged: 0.6 live-verified, unit+fake-server green (V48-style)|V59,V57,V58,I.master,C2
T51|x|relocate server-info structs + parse to follow V60: move ServerInfo/PlayerInfo to packet (packet.ServerInfo/PlayerInfo, version-agnostic result); add net6.ParseInfoResponse(body)→(packet.ServerInfo,error) (decimal-string ints, moved from master.parseInfo6) + net7.ParseInfoResponse(body)→(packet.ServerInfo,error) (varint ints, moved from master.parseInfo7); + tests in net6/net7 (parse synthesized body); ⊥ behavior change|V60,V48,I.master
T52|x|master consumes parsers, ⊥ decodes: master.ServerInfo/PlayerInfo = aliases of packet types (or ServerEntry.Info packet.ServerInfo); FetchServerList JSON→packet.ServerInfo; QueryServerInfo 0.6/0.7 call net6/net7.ParseInfoResponse (delete master.parseInfo6/7/decInt6); master ⊥ packer.GetInt/GetString; live 0.6 re-verify + unit/fake-server green|V60,V58,V48,I.master
T53|x|export all default consts as Default*/Min* (V62): packet defaultMaxSnaps→DefaultMaxSnaps, minMaxSnaps→MinMaxSnaps; client defaultPredInputRingSize→DefaultPredInputRingSize, minPredInputRingSize→MinPredInputRingSize; net6+net7 defaultEventChanSize→DefaultEventChanSize; network defaultReadBufferSize→DefaultReadBufferSize; master inline 5s/10s → DefaultQueryTimeout/DefaultHTTPTimeout consts; update all refs + tests; ⊥ behavior change|V62,V48
T54|x|enforce no-env (V63): confirm ⊥ os.Getenv in shipped pkgs (TW_TIMEOUT already removed from network.Dial); DefaultReadTimeout = 100s (DDNet conn_timeout) used as the env-free fallback; doc that env-driven config is the caller's job via With* options|V63,V48
T55|x|master.Client constructor: type Client + New(...Option) holding masters/http/policy/queryTimeout; convert FetchServerList/FetchServerListFrom/QueryServerInfo to METHODS (remove package-global funcs); keep ParseAddress package-level; Option set WithMasters/WithHTTPClient/WithRequestPolicy/WithQueryTimeout; update all call sites + tests (incl live_test); ⊥ change returned data (V48)|V64,V62,V63,V48,I.master
T56|x|RequestPolicy: interface Fetch(ctx,masters,try); ChooseFastest() DEFAULT replicating DDNet CChooseMaster (concurrent random-order probe, first valid wins, cache best index, re-probe on fail) + RoundRobin() (shared atomic cursor) + Failover(); wire into Client.FetchServerList; + tests (fake masters: fastest-valid wins, cache reused, re-probe on cached fail, all-down→err, RoundRobin rotates, Failover order)|V64,I.master
T57|x|migrate all *_test.go to t.Context(): replace context.Background()/context.TODO() with t.Context() (base) or context.WithTimeout/WithCancel(t.Context(),…); ~31 sites across net6/net7/client/master tests; cancellation tests derive cancellable ctx from t.Context(); cmd/ harness exempt; full suite green incl -race|V66
T58|x|test RoundRobin within-call exhaustion (V67): with n all-down masters, ONE Fetch attempts every master exactly once (hit-count == n) then errors; with only the rotation-start master down, recovers via the next; start index rotates across calls. (behavior already in master/policy.go — test-only, ⊥ code change unless a gap is found)|V67,V48
T59|x|login retransmission (B6/V68): net6 + net7 Handshake resends CONNECT on recv-timeout (bounded retries); recvUntilMapChange/recvUntilConReady/recvUntilReadyToEnter resend the last pending vital (INFO/READY) on recv-timeout + continue, bounded by overall connect deadline, instead of aborting on first `i/o timeout`. mirror DDNet CNetConnection::Update. test: mock conn dropping the first N packets of each step → connect still succeeds.|V68|net6/session.go,net7/session.go|
T60|x|godoc packet: all exported (Event types, Snapshot/SnapItem/SnapStorage, Token, MapInfo/MapCache, ServerInfo/PlayerInfo, ServerBrowse* magics, Pack*/Append*/Unpack helpers, ParseMapChangePayload, consts) + doc.go overview + Example (pack/unpack a snap field; build/parse MapInfo); refs DDNet datasrc/network.py + snapshot.cpp|V69,I.docs
T61|x|godoc packer: Unpacker, Pack*/Append*/GetString*/GetInt, CalculateUUID + doc.go + Example (PackInt→Unpacker.GetInt round-trip; GetStringSanitized); ref teeworlds varint CVariableInt + str_sanitize|V69,I.docs
T62|x|godoc network: Conn, Dial, DialOption (WithReadTimeout/WithWriteTimeout/WithReadBufferSize/WithLogger), DefaultReadTimeout/DefaultReadBufferSize, SendRaw/RecvContext + doc.go (raw UDP transport, ⊥ protocol) + compile-only Example (Dial+SendRaw); ref teeworlds CNetBase|V69,I.docs
T63|x|godoc net6: Session + all builders (BuildConnect/BuildCtrlPacket/BuildChunkPacket/BuildInfoRequestConnless/…), Sys*/Game* msg builders, ConnlessInfoPayload/ParseInfoResponse, Options, consts (Split,NetVersion) + doc.go (0.6/DDNet) + Example; refs DDNet protocol.h/protocol_ex.h + network.cpp|V69,I.docs
T64|x|godoc net7: Session + builders (BuildTokenRequest/BuildConnect/Ctrl*/BuildInfoRequestConnless), ParseTokenResponse/ConnlessInfoPayload/ParseInfoResponse, Header(7B/connless), consts + doc.go (0.7 sixup) + Example; refs teeworlds-0.7 network.cpp/protocol.h + datasrc/network.py|V69,I.docs
T65|x|godoc physics: Core, Collision, Tuning, WorldConfig + ctors/presets + doc.go (DDNet-faithful tick) + Example (seed Core, Tick, read pos); refs DDNet src/game/{gamecore.cpp,collision.cpp,game/character.cpp} + tuning.h|V69,I.docs
T66|x|godoc client: Client + every Option, Action types, Observer/Controller/Frontend, TickState, MapView, PredictedTime/PredictedWorld, SnapStorage accessors, ReconnectPolicy/Backoff, DisconnectReason, callbacks (On*/OnDisconnect), Default*/Min* + doc.go (lifecycle: New→Connect→observe/act) + runnable Examples (connect+OnChat; controller emits Action; WithPrediction); refs DDNet gameclient.cpp/prediction|V69,I.docs
T67|x|godoc master: Client, New, Options (WithMasters/WithHTTPClient/WithRequestPolicy/WithQueryTimeout), FetchServerList*/QueryServerInfo/ParseAddress, RequestPolicy + ChooseFastest/RoundRobin/Failover, ServerEntry/Address, Default*Masters/Timeout + doc.go + Examples (FetchServerList; QueryServerInfo); ref DDNet serverbrowser_http.cpp CChooseMaster + mastersrv|V69,I.docs
T68|x|doc-coverage gate: script/test asserting every exported symbol in shipped pkgs has a doc comment (parse via go/doc or `go doc` audit); all Example funcs compile + pass `go test ./...`; `go vet` clean; ⊥ behavior change (V48)|V69,V48,I.docs
T69|x|nil/empty structural-input safety (V70): physics.NewCollision(nil) + physics.NewCore(nil,…) + client.NewMapView(nil)/MapView() handle a nil/empty *twmap.Map as an empty all-solid world (⊥ deref m.GameLayers()); + tests passing nil map → no panic, OOB = ClassSolid/TileSolid|V70,I.robust
T70|x|input-validation sweep (V70): audit every exported func/method/ctor/Option across packet/packer/network/net6/net7/physics/client/master for unguarded caller input; clamp config (V62) / return error / guard nils per policy; document each contract in godoc; + per-package hostile-input tests (nil, "", negative, oversized, truncated/garbage wire bytes, bad version/address) asserting NO panic + sane error/clamp|V70,V62,I.robust
T71|x|client micro-benchmarks (measure-first, V49): add -benchmem micro-benches for per-tick hot paths lacking coverage — buildTickState, reconcilePrediction, SnapStorage.processSnapshot (full + empty delta, 64-char), MapView.Window crop / MapObservation, PredictedTime.OnSnapshot+NextInput; record baseline ns/op + allocs/op in §I.perf. ⊥ optimize yet (gate before T73). extends existing DeriveEvents(130 allocs)/CharactersCopy(67 allocs)|V49,I.perf
T72|x|profile the client per-tick loop: cpuprofile+memprofile over a simulated 50Hz build→dispatch→reconcile loop; rank top alloc-sites + CPU fns from pprof; record measured top-N candidates in §I.perf (⊥ optimize unmeasured, V49)|V48,V49,I.perf
T73|x|optimize PROVEN client hot paths: cut bounded alloc on the T72-ranked candidates (reuse per-tick scratch maps/slices, trim charactersCopy churn, prealloc TickState sub-slices) — V51 bounded-alloc, V52 pooled-scratch ⊥ alias retained state; re-bench (T71 harness) shows allocs/op drop; public API + behavior + prediction parity UNCHANGED (V48), full suite incl -race green|V48,V49,V51,V52,I.perf
T74|.|align public names to idiomatic Go (V71): rename Session.GetMapInfo→MapInfo (client.Session interface + net6/net7 Session + stubSession + internal callers); rename packer.Unpacker.GetInt/GetString/GetStringSanitized/GetRaw/GetByte/GetMsgAndSys → NextInt/NextString/NextStringSanitized/NextRaw/NextByte/NextMsgAndSys + ALL call sites across packer/packet/net6/net7 + tests + examples + godoc; keep MapCache.GetOrWait; naming-only, ⊥ behavior change; build + full suite incl -race + examples + doc-coverage gate green|V71,V48
order: T2–T21 = x (done). password + rcon + reconnect features ACTIVE: T22–T32 = `.` (pending).
perf effort (library client, ⊥ racebot): T34–T40 = `.` (pending). build order: T34 (bench baseline) → T35 (profile/rank) → T36 (snap O(1)) → T37 (unpacker reuse) → T38 (packer pack) → T39 (client per-tick) → T40 (re-bench/verify). T34+T35 are measure-FIRST gates — ⊥ optimize (T36–T39) before profile confirms targets (V49).
client perf round 2 (micro-bench-driven): T71–T73 = `.` (pending). build order: T71 (client micro-benches + baseline) → T72 (profile/rank per-tick loop) → T73 (optimize proven). same measure-FIRST gate — T71/T72 before T73; behavior + parity unchanged (V48).
snap storage size config: T41–T42 = `x` (done). build order: T41 (plumb option packet→session→client + clamp) → T42 (tests). additive opt-in, default unchanged (V53).
configurable buffer sizes: T43–T45 = `.` (pending). build order: T43 (predInputRingSize) → T44 (eventCh buffer) → T45 (UDP read buffer). each = option + clamp + tests; additive opt-in, defaults unchanged (V54); wire-format constants excluded (V55).
master server list + server info: T46–T47 = `x` (done). build order: T46 (HTTP/JSON master list) → T47 (connless server-info query). new `master` pkg, stdlib only (V56), read-only/no-session (V58); connless info reuses header connless flag (V57).
helper alignment: T48–T50 = `x` (done). build order: T48 (packet magics + net6 connless helpers) → T49 (net7 connless helpers) → T50 (refactor master onto helpers, ⊥ raw bytes). consolidation per V59 — net6/net7 own the framing, master only composes. behavior unchanged (V48-style).
master Client + request policy (V64): T55–T56 = `.` (pending). build order: T55 (Client + New + methods, drop globals) → T56 (RequestPolicy: ChooseFastest default = DDNet CChooseMaster replicate, + RoundRobin/Failover). returned data unchanged (V48); replicates DDNet master selection (§R).
public-defaults + no-env (V62/V63): T53–T54 = `x` (done). build order: T53 (export Default*/Min* consts) → T54 (assert no os.Getenv in shipped pkgs). behavior unchanged (V48); TW_TIMEOUT already removed.
parse-ownership alignment (V60): T51–T52 = `x` (done). build order: T51 (ServerInfo/PlayerInfo → packet; net6/net7 ParseInfoResponse) → T52 (master consumes parsers, delete master-side decode). answers "does parsing belong in master?" → NO; version-specific parse ⊂ net6/net7, result struct ⊂ packet. behavior unchanged (V48).
build order: T29 (password) → T30 (rcon API) → T31 (rcon state+reactions) → T32 (rcon tests) → T22 (research wire) → T23 (disconnect classify) → T24 (timeout code send) → T25 (reconnect-with-timeout) → T26 (auto-reconnect loop) → T27 (OnDisconnect callback) → T28 (reconnect tests).
prior build order (completed): T19 → T14 → T21 → T13 → T12 → T10a → T15 → T16 → T20 → T17.
input robustness (V70): T69–T70 = `.` (pending). build order: T69 (nil-map ctors → empty world) → T70 (full public-surface validation sweep + hostile-input tests). panic-free API; clamp/error/guard per input kind.
godoc the public surface (V69): T60–T68 = `x` (done). build order foundation-up: T60 packet → T61 packer → T62 network → T63 net6 → T64 net7 → T65 physics → T66 client → T67 master → T68 doc-coverage gate. each = godoc all exported + doc.go overview + runnable Example(s) + upstream refs (§R); documentation-only, ⊥ behavior change (V48).

## §R — research refs (verified sources)

catalog + prediction verified against pulled sources:
- DDNet `github.com/ddnet/ddnet@b10c6e4ea` (master, pulled 2026-06-12). msg/obj truth `datasrc/network.py`; 0.7↔0.6 map `src/game/client/sixup_translate_game.cpp`; whisper `src/game/client/components/chat.cpp:731`; prediction `src/game/client/prediction/{gameworld,entities/character,entities/projectile}.cpp` + `src/game/client/gameclient.cpp` (`OnNewSnapshot`, two-world `:2161/2219`, smooth `:2271/2285`, WorldConfig `:2828`).
- Teeworlds 0.7 `github.com/teeworlds/teeworlds@5d68273` (master=0.7, cloned 2026-06-12). 0.7 msg truth `datasrc/network.py`: `Sv_Chat{m_Mode,m_ClientID,m_TargetID,m_pMessage}`, `Sv_Team`, `Sv_ClientInfo/ClientDrop/SkinChange/GameInfo/GameMsg/ServerSettings/RaceFinish/Checkpoint`.
- local: `net6/constants.go`, `net6/reader.go`, `client/snap.go`, `packet/event.go`.
- DDNet timeout-code (T22, VERIFIED `~/Desktop/Development/ddnet`): NOT a dedicated netmsg — code is sent as a CHAT COMMAND `/timeout <code>` from `CClient::OnPostConnect` (`src/engine/client/client.cpp:527-536`) AFTER entergame, only when the server advertises `SERVERCAPFLAG_CHATTIMEOUTCODE` (`src/engine/shared/protocol_ex.h:34`). server handler `ConTimeout` (`src/game/server/ddracechat.cpp:565-600`): matches `/timeout` arg against every player's stored `m_aTimeoutCode`; on match `Server()->SetTimedOut(i, newClientId)` REClaims the timed-out tee + re-sends tuning. drop side: `SetTimeoutProtected` keeps the tee. SIXUP/0.7 CANNOT reclaim (server logs "0.7 clients can not reclaim … 0.6 client can") → resume = DDNet 0.6 ONLY (V37). code: DDNet derives MD5(seed+"normal"/"dummy"+server-addrs) via `generate_password` (`client.cpp:583`), or fixed `cl_timeout_code`; ANY stable string works — server only compares equality, so a per-client stable random satisfies V32. NOTE: our client does not yet parse server caps (NETMSG_EX) → cap-gating unavailable; T24 sends `/timeout` best-effort on 0.6 when resume enabled, cap-parse = `?` future refinement.
- ban/kick CTRL_CLOSE reason strings (T23, ← `~/Desktop/Development/ddnet` + teeworlds): verify exact text in `src/engine/server/server.cpp` (`Kick`/ban) + `src/engine/shared/network*.cpp` — "Kicked (...)", ban "Banned (...)"/"You have been banned" (+duration text), "Server shutdown". confirm on T23.
- perf (T34–T40, ← DDNet `src/engine/shared/snapshot.cpp` + Go runtime/profiling): DDNet `CSnapshot::GetItemIndex` uses an item index/hashtable for O(1) lookup (⊥ linear) → V50; `CSnapshotDelta::UnpackDelta` works over fixed preallocated `MAX_SNAPSHOT_SIZE` buffers, item field counts from `CSnapshotItem` type tables (no per-item size read) → V51. teeworlds `datasrc/network.py` item field counts ≅ our `ItemSizeFn`. Go: profile via `go test -bench . -benchmem -cpuprofile cpu.out -memprofile mem.out` + `go tool pprof`; escape analysis `go build -gcflags=-m`; `sync.Pool` for per-call scratch (already `deltaScratch`); prealloc slice cap; avoid `[]byte`↔`string` copies (`unsafe`-free). VERIFY actual hot paths on real snap traffic in T35 before optimizing (V49).
- master list + connless info (T46–T47, ← DDNet mastersrv + teeworlds/DDNet `serverbrowser`): DDNet mastersrv serves `servers.json` at `/ddnet/15/servers.json` (path + `/ddnet/15/register` confirmed via mastersrv README; `addresses` = `["tw-0.6+udp://host:port", …]`, `location`, `info{name,map,game_type,passworded,max_clients,max_players,clients[{name,clan,country,score,is_player}]}`). masters master1…master4.ddnet.org over HTTPS (served by reverse proxy). VERIFY exact `info` field names + the "15" version segment on T46 against a live fetch / mastersrv source. master client policy (T55/T56, ← DDNet `src/engine/client/serverbrowser_http.cpp` `CChooseMaster`, VERIFIED): default master URLs `DEFAULT_SERVERLIST_URLS` = master1…master4.ddnet.org `/ddnet/15/servers.json` (`:528`). selection: probe all masters CONCURRENTLY in RANDOM order (Fisher-Yates, `CJob::Run`), `CTimeout{10000ms, …, 8000B/s, 10s}`, first VALIDATED response wins (fastest healthy), best index CACHED (`m_BestIndex`/`m_PreviousBestIndex`, `GetBestUrl`), reused, re-probed on failure. masters are interchangeable replicas — ⊥ merge. mastersrv has NO cross-master sync (README; server `sv_register_url` defaults to master1 only, `config_variables.h:470`) → shared state is a deployment property of ddnet.org, not a protocol guarantee. ⇒ ChooseFastest default replicates this; RoundRobin/Failover are simpler opt-ins for custom masters.
connless server info (T47): DDNet/TW connless `getinfo`/`info` exchange — 0.6 EXTENDED info (`src/engine/server/server.cpp` `SendServerInfo`, request token in `CServer::ProcessConnlessPacket`), 0.7 token-gated connless; exact request magic + response layout = `?`, pin from `~/Desktop/Development/ddnet`/teeworlds `serverbrowser`/`server.cpp` before T47. uses the connless header flag already in `net6/header.go`/`net7/header.go`.

## §A — architecture (ref, ex-docs/ARCHITECTURE.md)

twclient = headless Teeworlds/DDNet client lib, Go, module `github.com/jxsl13/twclient`. impl 0.6 (DDNet variant) + 0.7 from scratch: packet headers, chunk frames, varint msgs, delta snaps, handshake incl TKEN token. consumers under `cmd/` (gitignored test/training harness, ⊥ shipped) — own docs in `cmd/*/docs/`.

dep direction (strict downward):
```
client/ → net6/,net7/ → network/,packer/ → packet/
```
- `packet/` — foundation, imports NOTHING internal. types: `Token`, `ChunkHeader`, `Snapshot`/`SnapItem`/`SnapStorage`, `PlayerInput`, `Direction`/`JumpState`/`HookState`/`Weapon`, `Event` iface, `MapInfo`/`MapCache`. fns: `UnpackChunks`, `CountVitalChunks`/`ContainsSysMsg`/`ContainsGameMsg`, `PackMsgID`/`PackInt`/`PackStr`, physics consts, coord/tile conv. INVARIANT: ⊥ import other internal pkgs; version-specific logic ⊂ net6/net7.
- `packer/` — varint+string wrap over `github.com/teeworlds-go/varint`. `Unpacker`, `PackInt`/`PackStr`/`PackMsgID`. `CalculateUUID` (DDNet ext-msg UUID v3).
- `network/` — UDP transport. `Conn` wraps `net.UDPConn`: `Dial`/`SendRaw`/`RecvContext`. INVARIANT: ⊥ know protocol versions, moves raw bytes only.
- `net6/` — 0.6.4 + DDNet TKEN. consts `Split=4`, `NetVersion="0.6 626fce9a778df4d4"`, `DDNetVersion=19070`. types `Header`(3B/7B), `Flags`, `Session`. builders `BuildConnect`/`BuildInfoPacket`/`BuildReadyPacket`/`BuildEnterGamePacket`/`BuildStartInfoPacket`. snap sizes `snap.go`. INVARIANT: ⊥ depend on `client/` (flows other way).
- `net7/` — 0.7. like net6 but `Split=6`, 7B header (always token), diff msg ids, native race msgs (`MsgGameSvRaceFinish`/`MsgGameSvCheckpoint`).
- `client/` — protocol-agnostic API wrapping net6/net7 Session. types `Client`, `Session` iface (`Login`/`Close`/`StartReader`/`EventCh`/`Poll`/`SendInput`/`SendChat`/`SendKill`/`DownloadMap`/`Map`/`SetMap`), `SnapStorage`(`CharacterState`,`GameInfoState`), `PredictedTime`, `RaceTime`. INVARIANT: API boundary — consumers talk ONLY to `client.Client`, never net6/net7 direct.

data flow: `network.Conn.RecvContext` → `net6.Session` bg reader (unpack header,ack,decompress,chunks) → `processMessage` → `packet.Event` on eventCh → `client.Client` event loop (extract CharacterState/GameInfoState, update PredictedTime) → consumer reads `Character()`/`RaceTime()`/`LastSnapTick()` → `client.SendInput()`.

concurrency: each Session = bg reader goroutine (ack/seq mutex, mapInfo/state RWMutex). `client.Client` = bg event-loop goroutine (snap RWMutex, accessors thread-safe). `MapCache` mutex-safe (dedup downloads).
tick rate: 50/s (20ms). PredictedTime advances from last acked tick.
deps: `github.com/jxsl13/twmap` (map parse), `github.com/teeworlds-go/huffman/v2` (compress), `github.com/teeworlds-go/varint` (varint).
test: `go build ./...`; `go test ./... -v`; `TW_TARGET=localhost:8303 go test ./client -run TestLogin06 -v`; `go test ./client -fuzz FuzzPostHandshakeChunks -fuzztime 30s`.

## §P — wire protocol (ref, ex-docs/PROTOCOL.md)

src: chillerdragon 0.6 docs, DDNet `network.{h,cpp}`/`network_conn.cpp`, teeworlds-go/protocol. input semantics → §X.

packet: UDP ≤1400B. header 3B (DDNet/0.6.4) | 7B (vanilla 0.6.5 token flag). control pkt = header+1 ctrl msg, NumChunks=0, NEVER compressed. game/sys = header+N chunks, may huffman. DDNet token = 4B appended AFTER all chunk data (⊥ in header), before compression.
header wire (DDNet `SendPacket`):
```c
aBuffer[0] = ((m_Flags << 2) & 0xfc) | ((m_Ack >> 8) & 0x3);
aBuffer[1] = m_Ack & 0xff;
aBuffer[2] = m_NumChunks;
```
header layout: bits — flags(5..1), ack 10bit (b0 bits1:0 + byte1), NumChunks 8bit (byte2). 0.6.5 adds 4B token after.
flag bits (byte0): 5=Compression, 4=Resend, 3=Connless, 2=Control, 1=Token(0.6.5 only, ⊥ DDNet), 0=Unused(DDNet→0.7/Sixup detect).
```c
NET_PACKETFLAG_UNUSED=1<<0; TOKEN=1<<1; CONTROL=1<<2; CONNLESS=1<<3; RESEND=1<<4; COMPRESSION=1<<5;
```
DDNet TKEN: 0.6.4-based, ⊥ 0.6.5 header flag. appends 4B sec-token to END of payload (`WriteSecurityToken(chunkData+DataSize)`), stripped on recv (`DataSize -= sizeof(token)`), verify at `chunkData[DataSize]`.

chunk header (Split=4 for 0.6, Split=6 for 0.7): non-vital 2B, vital 3B(+seq). flags: bit0=Vital, bit1=Resend. size 10bit → max payload 1023B. seq 10bit → wrap 1024 (`NET_MAX_SEQUENCE`). pack:
```c
pData[0] = ((m_Flags & 3) << 6) | ((m_Size >> Split) & 0x3f);
pData[1] = (m_Size & ((1 << Split) - 1));
if(VITAL){ pData[1] |= (m_Sequence >> 2) & (~((1<<Split)-1)); pData[2] = m_Sequence & 0xff; }
```
msg id varint: `packed_id = (msg_id << 1) | system_flag` (sys=1 system, 0 game).
varint: `ESDDDDDD EDDDDDDD…` — E(b7)=more, S(b6 first byte)=sign, D=data. byte0=6 data bits, rest 7. little-endian. neg = one's complement (XOR -1).

handshake DDNet (0.6.4+TKEN): C→CONNECT(ctrl 0x01, payload `"TKEN"(4)+ClientToken(4)+pad(504)`=512 anti-reflection) → S→CONNECTACCEPT(0x02, `"TKEN"(4)+SecurityToken(4)`) → C extract token payload[5:9] → C→ACCEPT(0x03 empty). then login (all pkts append token): C→INFO(sys1, version+pw) → S→MAP_CHANGE(sys2) → C→READY(sys14) → S→(MOTD+ServerSettings+CON_READY 3 chunks) → C→CL_STARTINFO(game20) → C→ENTERGAME(sys15) → S→SV_VOTE* → S→SV_READYTOENTER(game8) → snaps begin.
vanilla 0.6.5: token in 7B header (CONNECT token=0xFFFFFFFF, CONNECTACCEPT ServerToken in payload), NO ACCEPT step, state→ONLINE direct. KEY diff: DDNet 3B header + appended TKEN + extra ACCEPT; vanilla 7B header token.

control msgs (0.6): NumChunks=0, never compressed, payload=`[ctrl_id(1B)]+[extra]`.
```
0x00 KEEPALIVE both (none) | 0x01 CONNECT C `"TKEN"(4)+ClientToken(4)+pad(504)`=512 | 0x02 CONNECTACCEPT S `"TKEN"(4)+SecurityToken(4)`=8 | 0x03 ACCEPT C(DDNet,removed vanilla) (none) | 0x04 CLOSE both opt null-term reason
```
system msgs (0.6) — id|name|dir|payload:
```
1 INFO C→S String(version)+String(password)               | 2 MAP_CHANGE S→C String(map)+Int(crc)+Int(size)
3 MAP_DATA S→C Int(last)+Int(crc)+Int(chunk)+Int(chunkSize)+Raw(data)
4 CON_READY S→C (none)                                     | 5 SNAP S→C Int(tick)+Int(deltaTick)+Int(numParts)+Int(part)+Int(crc)+Int(partSize)+Raw
6 SNAPEMPTY S→C Int(tick)+Int(deltaTick)                   | 7 SNAPSINGLE S→C Int(tick)+Int(deltaTick)+Int(crc)+Int(partSize)+Raw
8 SNAPSMALL S→C (undocumented)                             | 9 INPUTTIMING S→C Int(intendedTick)+Int(timeLeft)
10 RCON_AUTH_STATUS S→C Int(authed)+Int(cmdList)           | 11 RCON_LINE S→C String(line)
12 AUTH_CHALLENGE / 13 AUTH_RESULT (unused)                | 14 READY C→S (none)
15 ENTERGAME C→S (none)                                    | 16 INPUT C→S Int(ackTick)+Int(predTick)+Int(size)+[PlayerInput]
17 RCON_CMD C→S String(command)                            | 18 RCON_AUTH C→S String(name)+String(password)+Int(sendRconCmds)
19 REQUEST_MAP_DATA C→S Int(chunk)                         | 20 AUTH_START / 21 AUTH_RESPONSE (unused)
22 PING / 23 PING_REPLY both (none)                        | 24 ERROR (unused)
25 RCON_CMD_ADD S→C String(name)+String(help)+String(params) | 26 RCON_CMD_REM S→C String(name)
```
game msgs (0.6) — id|name|dir|payload:
```
1 SV_MOTD S→C String(msg)                | 2 SV_BROADCAST S→C String(msg)
3 SV_CHAT S→C Int(team)+Int(clientID)+String(msg) | 4 SV_KILLMSG S→C Int(killer)+Int(victim)+Int(weapon)+Int(modeSpecial)
5 SV_SOUNDGLOBAL S→C Int(soundID)        | 6 SV_TUNEPARAMS S→C Int×32
7 SV_EXTRAPROJECTILE S→C (removed 2015)  | 8 SV_READYTOENTER S→C (none)
9 SV_WEAPONPICKUP S→C Int(weapon)        | 10 SV_EMOTICON S→C Int(clientID)+Int(emoticon)
11 SV_VOTECLEAROPTIONS S→C (none)        | 12 SV_VOTEOPTIONLISTADD S→C Int(numOptions)+String×15
13 SV_VOTEOPTIONADD S→C String(desc)     | 14 SV_VOTEOPTIONREMOVE S→C String(desc)
15 SV_VOTESET S→C Int(timeout)+String(desc)+String(reason) | 16 SV_VOTESTATUS S→C Int(yes)+Int(no)+Int(pass)+Int(total)
17 CL_SAY C→S Int(team)+String(msg)      | 18 CL_SETTEAM C→S Int(team)
19 CL_SETSPECTATORMODE C→S Int(spectatorID) | 20 CL_STARTINFO C→S String(name)+String(clan)+Int(country)+String(skin)+Int(useCustomColor)+Int(colorBody)+Int(colorFeet)
21 CL_CHANGEINFO C→S (=CL_STARTINFO)     | 22 CL_KILL C→S (none)
23 CL_EMOTICON C→S Int(emoticon)         | 24 CL_VOTE C→S Int(vote)
25 CL_CALLVOTE C→S String(type)+String(value)+String(reason)
```
snap obj types (0.6) — id|name|fields:
```
1 PlayerInput Direction,TargetX,TargetY,Jump,Fire,Hook,PlayerFlags,WantedWeapon,NextWeapon,PrevWeapon
2 Projectile X,Y,VelX,VelY,Type,StartTick     | 3 Laser X,Y,FromX,FromY,StartTick
4 Pickup X,Y,Type,Subtype                      | 5 Flag X,Y,Team
6 GameInfo GameFlags,GameStateFlags,RoundStartTick,WarmupTimer,ScoreLimit,TimeLimit,RoundNum,RoundCurrent
7 GameData TeamscoreRed,TeamscoreBlue,FlagCarrierRed,FlagCarrierBlue
8 CharacterCore Tick,X,Y,VelX,VelY,Angle,Direction,Jumped,HookedPlayer,HookState,HookTick,HookX,HookY,HookDx,HookDy
9 Character CharacterCore+Health,Armor,AmmoCount,Weapon,Emote,AttackTick
10 PlayerInfo Local,ClientID,Team,Score,Latency
11 ClientInfo Name(4),Clan(3),Country,Skin(6),UseCustomColor,ColorBody,ColorFeet
12 SpectatorInfo SpectatorID,X,Y
```
conn states (DDNet `EState`): `OFFLINE, WANT_TOKEN, CONNECT, PENDING, ONLINE, ERROR`. OFFLINE→CONNECT(Connect, sends CONNECT every 500ms)→PENDING(recv CONNECTACCEPT)→ONLINE(recv non-ctrl OR send ACCEPT)→ERROR(timeout/close)→OFFLINE(Reset).

DDNet ext msgs (UUID): `NETMSG_EX` id=0 → wire `varint(1)=(0<<1)|1` + 16B UUID + payload. UUID = v3 MD5: `MD5(TEEWORLDS_NAMESPACE || name_without_NUL)`, namespace `e05ddaaa-c4e6-4cfb-b642-5d48e80c0029`, then version=3(byte6) variant=1(byte8). known:
```
WHATIS what-is@ddnet.tw both UUID(16) | ITIS it-is@ddnet.tw both UUID(16)+String(name) | IDONTKNOW i-dont-know@ddnet.tw both UUID(16)
RCONTYPE rcon-type@ddnet.tw S→C Int(usernameRequired) | MAP_DETAILS map-details@ddnet.tw S→C String(map)+Raw(sha256,32)+Int(crc)+Int(size)+String(url)
CAPABILITIES capabilities@ddnet.tw S→C Int(version)+Int(flags) | CLIENTVER clientver@ddnet.tw C→S UUID(connUUID,16)+Int(ddnetVersion)+String(versionStr)
PINGEX ping@ddnet.tw both UUID(16) | PONGEX pong@ddnet.tw both UUID(16) | REDIRECT redirect@ddnet.org S→C Int(port) | RECONNECT reconnect@ddnet.org S→C (none)
```
CLIENTVER: sent BEFORE INFO at login. without it server treats client as vanilla 0.6 (no DDNet features/caps). wire: `varint(1)` + UUID of clientver (`8c001304-8461-3e47-8787-f672b3835bd4`) + 16B random conn UUID(v4) + `varint(DDNetVersion)` (e.g. 19070) + null-term version str.
CAPABILITIES flags: 0=DDNET, 1=CHATTIMEOUTCODE, 2=ANYPLAYERFLAG, 3=PINGEX, 4=ALLOWDUMMY, 5=SYNCWEAPONINPUT.

huffman: only non-control payloads compressible. flag in header. official TW freq table. sec-token appended BEFORE compression (token gets compressed too).

constants: `NET_MAX_PACKETSIZE`=1400, `NET_MAX_PAYLOAD`=1394, `NET_PACKETHEADERSIZE`=3, `NET_MAX_SEQUENCE`=1024(10bit), `NET_MAX_CHUNK_SIZE`=1023(10bit), `NET_MAX_PACKET_CHUNKS`=255(8bit), `NET_TOKENREQUEST_DATASIZE`=512, `NET_SECURITY_TOKEN_UNKNOWN`=-1, `NET_SECURITY_TOKEN_UNSUPPORTED`=0, `SECURITY_TOKEN_MAGIC`=`{'T','K','E','N'}`, `NET_VERSION`(0.6)=`"0.6 626fce9a778df4d4"`, `NET_VERSION`(0.7)=`"0.7 802f1be60a05665f"`.
snap delta note: updated items use SEPARATE type+id varints (⊥ packed key); size field only for unknown/extended item types (DDNet `snapshot.cpp`).

## §X — input & physics (ref, ex-docs/INPUT.md)

src: DDNet `gamecore.cpp`, `prediction/entities/character.cpp`, `gameclient.cpp`; chillerdragon; teeworlds-go/protocol.

`CNetObj_PlayerInput` = 10 int fields, varint-sent:
```c
m_Direction;  // -1 left,0 stop,1 right
m_TargetX; m_TargetY;  // cursor REL to tee (⊥ world coords). (0,0)→(0,-1). angle = atan2(TargetY,TargetX)*256
m_Jump;       // 1 jump (ground/air),0 no
m_Fire;       // bit0=state, bits1+=counter (parity flip = new shot)
m_Hook;       // 1 active,0 release. dir from TargetX/Y
m_PlayerFlags;// Playing,InMenu,Chatting,Scoreboard,AimOnMousepos
m_WantedWeapon; // 1-6 (Hammer,Gun,Shotgun,Grenade,Laser,Ninja)
m_NextWeapon; m_PrevWeapon; // scroll counters
```
direction: applies in `CCharacterCore::Tick` via SaturatedAdd. tuning defaults: GroundControlSpeed=10, GroundControlAccel=2, GroundFriction=0.5, AirControlSpeed=5, AirControlAccel=1.5, AirFriction=0.95.
jump: bitfield `m_Jumped` (bit0=executed this frame, bit1=air jumps spent). MUST send 0→1 transition — holding 1 ⊥ retrigger (1→0→1 for air jump). impulses: GroundJumpImpulse=13.2, AirJumpImpulse=12.0. DDRace `m_Jumps`: -1 ground-only, 0 none, 1 one, 2 default(1+1); `m_EndlessJump` unlimited.
hook FSM: `IDLE→(Hook=1)→FLYING→GRABBED→(Hook=0/Timeout)→RETRACTED→IDLE`. HookFireSpeed=80, HookLength=380, HookDragAccel=3.0 (upward stronger y*=0.3). timeout = `SERVER_TICK_SPEED*1.25` ≈62 ticks (1.25s). DDRace `m_EndlessHook` (HookTick=0 every tick); `TILE_NOHOOK` → RETRACT_START not GRABBED.
weapon switch: `HandleWeaponSwitch` — Next/Prev counters skip unowned; direct `m_WantedWeapon` overrides (1-based→0-based); applied ONLY when `m_ReloadTimer==0` & ⊥ Ninja.
fire: `CountInput(prev,cur).m_Presses>0`. FullAuto (Shotgun/Grenade/Laser) fires while `Fire&1`. counter: bit0=state, upper bits=changes; new shot when counter increased. trigger programmatically: `Fire=(Fire+1)|1` press (odd), `Fire=(Fire+1)&~1` release (even).
fire delays (default tuning, ms/ticks@50): Hammer 125/~6, Gun 125/~6, Shotgun 500/25, Grenade 500/25, Laser 800/40, Ninja 800/40.

NETMSG_INPUT (sys16): `AckGameTick + PredictionTick(=PredTick) + Size(40=10×4) + InputData[10]`. PredTick = GameTick + prediction latency (future). INPUTTIMING (sys9): `IntendedTick + TimeLeft(ms to server exec)` — TimeLeft>0 too early (slow down), <0 too late (speed up), ≈0 perfect. aim send ~`PREDICTION_MARGIN` ms before exec.

physics tick (50/s):
1. `CCharacterCore::Tick(UseInput)` — gravity `m_Vel.y += Gravity(0.5)`; ground check `IsOnGround`; read dir; angle; jump(§); hook(§); movement SaturatedAdd; TickDeferred (player collisions, vel clamp max 6000).
2. `Move()` — VelocityRamp; MoveBox (world collide); ground reset (Jumped&=~2, JumpedTotal=0); player collision.
3. `Quantize()` — round floats→net ints; pos snap (exact reproducibility).
velocity ramp: `if(Value<Start) 1.0; else 1.0/pow(Curvature,(Value-Start)/Range)`. defaults VelrampStart=550, Range=2000, Curvature=1.4.

client prediction: `PredictedTime` — `PredTick = baseTick + elapsedTicks + 1` (+1 always 1 tick ahead) from last acked snap. loop: copy GameWorld → for tick GameTick+1..PredGameTick: fetch input, `OnDirectInput`(weapons/fire, edge via CountInput) + `OnPredictedInput`(move/jump/hook) + `GameWorld.Tick()` → store Predicted+PrevPredicted. input ring buffer 200 slots keyed by tick. smooth render: local `mix(PrevPredicted.Pos, Predicted.Pos, PredIntraGameTick)`; others `mix(Prev,Cur, IntraGameTick)`.

DDRace physics:
- freeze (`m_FreezeTime>0`): Direction=0, Jump=0, Hook=0 (except live freeze).
- tile mods: Freeze/Unfreeze, EndlessHook, UnlimitedJumps, Solo(no player collide), Jetpack(gun=recoil), Speedup, TuneZones(per-area physics).
- velocity units: pos 1unit=1px@zoom1; vel = units/tick, wire `VelX=vel.x×256` fixed-pt; tiles 32×32px; PhysicalSize=28px; TeeRadius=14px.
- tee box 28×28 (`PhysicalSize()=28.0f`). `TestBox(Pos,Size)` corners `Pos±14`; `IsOnGround` checks `(Pos.x±14, Pos.y+14+5)` =19 below; player collision `dist<28`. tile triggers at tee CENTER `GetMapIndex(Pos)=Pos/32` (center overlap, ⊥ box edge).

## §B — bugs

```
id|date|cause|fix
B1|2026-06-13|T4e assumed ext snap-objects need new decoder infra; feared blocked. premise WRONG: applyDelta already passes ext items through raw.|RESOLVED in T4e2: marker (type-0, id≥0x4000) carries UUID; ext obj uses type≥0x4000. deriveExt in client/snap.go maps marker UUID→type & decodes DDNetCharacter/Player/SpecChar/Finish. NO decoder change. T4e=DamageInd (vanilla) still valid split.
B2|2026-06-13|T9b needs per-weapon curvature+speed (gun/shotgun); physics.Tuning only had grenade.|RESOLVED: added GunSpeed/Curvature(2200/1.25), ShotgunSpeed/Curvature(2750/1.25) to physics.Tuning (DDNet tuning.h) + Tuning.ProjectilePos (CalcPos formula). PredictedProjectiles() advances snapshot projectiles to predTick. laser = hitscan, no ballistic predict needed.
B3|2026-06-13|T10a reconcile smoothing was render-only; headless client had no renderer → deferred as dead code.|REVERTED 2026-06-13: render/UI consumer + ML consumer now in scope (V21, T15 Frontend). smoothing needed for sub-tick render interpolation. T10a back to `.` (active). "revisit if render consumer added" condition now met.
B5|2026-06-13|T33 caps parsing never fired on real DDNet servers (live test localhost:8303 + 45.141.36.31). 3 causes: (1) caps NETMSG_EX sent BEFORE MAP_CHANGE → consumed by synchronous recvUntilMapChange (reader/eventCh not up yet); (2) ExtractSysMsgPayload returned only the FIRST EX, caps was 2nd/3rd chunk; (3) Client.Capabilities() read client field set only via EventServerCapabilities, never emitted during login.|RESOLVED: added packet.ExtractAllSysMsgPayloads (all EX, not first); recvUntilMapChange now scans every EX → maybeParseCapabilities stores on Session; Connect seeds c.caps = sess.Capabilities() after login. Verified live: DDNet=true ChatTimeoutCode=true ver=5. Test: TestExtractAllSysMsgPayloads. Strengthened V47.
B4|2026-06-13|/check found V10b VIOLATE: T9 marked x but no WorldConfig anywhere — prediction was single global physics model, no vanilla≠DDRace split. DDRace freeze would (not) be predicted regardless of server type.|RESOLVED: added physics.WorldConfig{IsVanilla,IsDDRace,PredictWeapons,PredictFreeze,PredictTiles,PredictDDRace} + Default/DDRace presets. Core.SetWorldConfig gates freeze (Collision.Freeze predicate, freeze tile suppresses control) + weapons. Game-type from map: MapView.IsDDRace() = has tele/speedup/switch/tune layer OR freeze tile. predCfg derived once at map load, fed per-core in seedCore. Vanilla servers never predict freeze (V10b satisfied). Tests: freeze-only-on-ddrace, hook-release-on-freeze, weapons-gated, IsDDRace detection.
B6|2026-06-13|net6/net7 Login had NO vital/ctrl RETRANSMISSION. The background reader/resender starts only AFTER Login (client/client.go), so during handshake+login nothing resent a lost packet. A single dropped CONNECT, INFO, READY, or the server's MAP_CHANGE/CON_READY was never resent → the recv-loop (Handshake/recvUntilMapChange/recvUntilConReady in net6/session.go + net7/session.go) hit the read deadline and aborted: `session06: recv waiting for map_change: read udp …: i/o timeout`. Surfaced under packet loss — many concurrent clients from one host (UDP buffer overflow) or any lossy real network. DDNet `CNetConnection::Update` resends unacked vitals + re-sends CONNECT on a timer; we didn't.|RESOLVED (T59, V68): `resendRecv` helper (net6+net7) waits one `loginResendInterval` (500ms) for the expected reply, else retransmits the pending step — CONNECT / token-request in Handshake, the SAME INFO/READY vital bytes in the recv-loops — bounded by the connect ctx, instead of aborting on first i/o timeout. Mirrors `CNetConnection::Update`. Verified: lossy mock server (0.6 + 0.7) dropping the first N datagrams of EVERY step → Login still completes (`TestLoginSurvivesPacketLoss`, net6+net7, -race). Paced connects (cmd/racebot) remain a complementary mitigation, ⊥ the cure.
```
