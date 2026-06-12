# SPEC — twclient: server-event callbacks, antiping prediction, consumer/Frontend interface

## §G — goal

Client exposes callback registration for server events (chat, whisper, server msg, vote, hook-by, weapon-change, …) + full DDNet antiping prediction (predict whole world — all chars + projectiles/lasers — ahead of snaps via `physics.Core`, smoothed reconcile) + ONE pluggable tick-driven `Frontend` interface serving UI render+input, ML training, and ML execution identically (protocol-unified) — incl. ego-centric fixed-window map observation over the complete local map. consolidate redundant types (one canonical per concept).

## §C — constraints

- C1: Go 1.26.1, module `github.com/jxsl13/twclient`. No new deps.
- C2: support both `packet.Version06` (net6) & `packet.Version07` (net7). ! single shared event-type set — both protocols map to EXACT same event structs wherever feature exists in both. version diff hidden in reader, ⊥ leak to consumer.
- C3: callbacks fire from `eventLoop` goroutine (`client/client.go:363`). 1 goroutine → callbacks serialized.
- C4: existing event flow unchanged: session reader → `packet.Event` on `EventCh()` → `Client.handleEvent`. New events extend `packet.Event` interface (`eventTag()`).
- C5: 2 event classes. msg-derived = parse game msg in `net6/reader.go` `processPayload` switch (`:180`) & net7 equiv. snap-derived (hook-by, weapon-change) = diff consecutive `CharacterState` in `client/snap.go`.

## §I — interfaces

### callback API (Client)
per-event `OnX`. handler ! receive `*Client` first param → response logic inline. Returns unregister closure.
```
register: func (c *Client) OnChat(fn func(*Client, ChatEvent))       → func() // unregister
register: func (c *Client) OnWhisper(fn func(*Client, WhisperEvent)) → func()
register: func (c *Client) OnBroadcast(fn func(*Client, BroadcastEvent)) → func()
register: func (c *Client) OnServerMsg(fn func(*Client, ServerMsgEvent)) → func()
register: func (c *Client) OnVoteSet(fn func(*Client, VoteSetEvent)) → func()
register: func (c *Client) OnVoteStatus(fn func(*Client, VoteStatusEvent)) → func()
register: func (c *Client) OnKill(fn func(*Client, KillEvent)) → func()
register: func (c *Client) OnEmoticon(fn func(*Client, EmoticonEvent)) → func()
register: func (c *Client) OnHookedBy(fn func(*Client, HookedByEvent)) → func()
register: func (c *Client) OnWeaponChange(fn func(*Client, WeaponChangeEvent)) → func()
```
ex: `c.OnChat(func(c *Client, e ChatEvent){ c.SendChat("re: "+e.Msg) })`

`OnX` registrar per event in §I.catalog (presence/motion/transient/game). same shape: `func(*Client, XEvent) → func()`.

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
ONE pluggable interface serves ALL consumers identically: visual UI (render + user input), ML training (ingest per-tick state), ML execution (state→action). Same plug into headless client. Consumer ⊥ see protocol version (V18). Drives off PREDICTED state each tick.
```
type: Frontend (consumer) interface — plugged via WithFrontend(f) / SetFrontend(f)
  OnTick(c *Client, st TickState) []Action   // observe predicted tick → emit actions
  (optional OnEvent(c, packet.Event) for async server events not tied to a tick)

obs: TickState — COMPLETE observable+predicted state for one tick (V19), self-contained:
  Tick, IntraTick float (sub-tick for smooth render, V21)
  LocalID
  Players   map[int]CharacterState       // predicted pos/vel/hook/weapon/freeze/flags, all visible cids (ONE char type, V25)
  Projectiles []ProjectileState          // predicted (T9b)
  Lasers, Pickups, Flags                  // visible entities
  Map       MapView                       // full static map: all layers + tune-zone (T14, V28)
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

dual cadence (V24) — Frontend declares mode, driver picks loop:
  TickModeFixed : OnTick once per predicted tick (50Hz, IntraTick=0). ML/training. deterministic.
  TickModeFrame : OnTick per render frame; IntraTick∈[0,1) from wall-clock between ticks; positions
                  smoothed. UI/render.
clean impl: ONE canonical builder `buildTickState(tick) TickState` (IntraTick=0). fixed mode calls it
  per new tick. frame mode calls it for the latest tick, then overlays SmoothedCharacters(intra) +
  sets IntraTick — NO duplicate state-assembly. ⊥ two divergent TickState code paths (V24).
plug: `type Frontend interface { Mode() TickMode; OnTick(*Client, TickState) []Action }`. one builder,
  two thin loops. headless/ML & UI share everything except the cadence wrapper.
```

### MapView — environment / collision observation (T14)
spans the COMPLETE local map (downloaded on connect / cached), NOT the snapshot-visible region (V26). map is fully available offline → window can sit anywhere.
```
type: MapView (client) — queryable static map over the WHOLE map. ALL DDNet special-tile layers, not just collision (V28).
api:  Width, Height int                       // full-map tile bounds
api:  Tile(tx,ty int) TileClass               // Air|Solid|Unhook|HookThrough|Death|Freeze|Tele|Speedup|Switch|Tune|...
api:  Solid/Unhook/HookThrough/Death/Freeze/Tele/Speedup/Switch(tx,ty) bool   // OOB → Solid
api:  TuneZone(tx,ty int) int                 // tune-zone index from map Tune layer (0=default); drives position tuning (V29)
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
- V15: whisper unified — ∀ source → identical `WhisperEvent{FromID,ToID,Msg}`. sources: 0.6 DDNet `Sv_Chat m_Team`∈{TEAM_WHISPER_SEND,TEAM_WHISPER_RECV} (≥2); 0.7 `Sv_Chat m_Mode==CHAT_WHISPER`. (vanilla 0.6 teeworlds: none — DDNet adds via m_Team.) consumer ⊥ see protocol diff.
- V15a: 0.7 obj-as-message normalize — 0.7 `Sv_ClientInfo`/`Sv_ClientDrop`/`Sv_SkinChange`/`Sv_Team`/`Sv_GameInfo` carry data that in 0.6 lives in snap OBJECTS (ClientInfo/GameInfo). reader maps BOTH → same event (E_player_join/leave/skin_change/team_set/game_info). ref `sixup_translate_game.cpp`. test: join fires on 0.6 & 0.7.
- V16: full event scope — ∀ §I.catalog rows (vanilla + DDNet-ext + snap-derived A/B/C/D/E) implemented. ⊥ silent skip; unimpl → explicit `?`-flagged + §T row.
- V17: protocol-unified events (generalizes V15 to whole catalog). ONE event struct per logical event, defined once (`packet`). net6 & net7 readers both emit it. consumer/callback code ⊥ branch on `version`, ⊥ see net6/net7 types. event present in only 1 protocol → documented version-only in §I + `?`. snap-derived events identical (snap format shared post-decode). test: same handler fires on both 0.6 & 0.7 server for shared events.
- V18: consumer interface protocol-unified (extends V17 to actions). `Action` set & `TickState` identical regardless of 0.6/0.7. `c.Do(Action)` maps to the active session's send. consumer/Frontend ⊥ branch on version, ⊥ see net6/net7 types.
- V19: `TickState` self-contained & complete — ∀ data a consumer needs for one tick present: predicted local+all chars, projectiles, visible entities, MapView (collision env incl unhookable tile positions), tuning, game/race state, events-since-last-tick. ⊥ require consumer to call back for missing state. built from PREDICTED world (V9), not raw snap.
- V20: ONE plug ∀ consumers — UI render, UI input, ML train-ingest, ML exec — implement same `Frontend` interface, plug identically (`WithFrontend`/`SetFrontend`) into headless `Client`. ⊥ separate API per use case. UI-input path == ML-action path == `c.Do(Action)`.
- V21: smoothing IN-SCOPE (supersedes B3 deferral — render consumer now exists). keep prev+cur `PredictedWorld`; `TickState.IntraTick` + `SmoothedCharacters(intra)` lerp prev→cur per cid. render ⊥ teleport between ticks. headless-only consumers may ignore (intra=0 == V10/predicted).
- V22: `Action` covers full ddnet + 0.7 client action set — movement/aim/jump/hook/fire/weapon, chat, team chat, whisper, emoticon, kill, vote, call-vote, set-team, spectate. each ! map to a net6 AND net7 send (or documented version-only + `?`). missing action → `?`-flag + §T row.
- V24: dual cadence, single builder. driver supports `TickModeFixed` (50Hz, IntraTick=0, ML) & `TickModeFrame` (render rate, IntraTick∈[0,1), smoothed). BOTH go through ONE `buildTickState(tick)`; frame mode only overlays `SmoothedCharacters(intra)` + IntraTick on top. ⊥ duplicate/divergent TickState assembly per mode. Frontend.Mode() selects loop; everything else shared.
- V25: ONE canonical type per concept — ⊥ redundant parallel structs/consts. character = `CharacterState` (snapshot AND predicted; ⊥ separate `PredictedCharacter` type). sim char = `physics.Core` (convert only at seed/extract). position: `physics.Vec2` (sim float) ↔ int X/Y (wire/snap) at ONE conversion site. input: `packet.PlayerInput` (wire/Action) ↔ `physics.Input` (sim) via single `inputToPhysics`. weapon ids: `packet.Weapon` is source; `physics` mirror = SOLE documented exception (layer isolation, ⊥ packet import), ⊥ any further dup. tuning: `physics.Tuning` canonical, `EventTuneParams.Raw` = wire form decoded once. new code ! reuse canonical, ⊥ reinvent.
- V26: `MapView` spans the COMPLETE local map (downloaded/cached), ⊥ limited to snapshot-visible region. out-of-bounds tile query → `Solid` (world border, matches collision). `Window` crops a FIXED-size chunk at any center, OOB padded Solid.
- V27: ML observation window FIXED-size ∀ ticks (constant ML input shape) — config W×H tiles (default square), ego-centric on predicted self, OOB padded Solid, multi-channel (static map planes + dynamic entity planes). ⊥ variable-size or visible-only crop. ⊥ rebuild map collision per tick (map static — query the one MapView).
- V28: observation completeness — obs exposes EVERYTHING available for the tick: ALL static map layers (collision, freeze, death, tele, speedup, switch, tune-zone), ALL dynamic entities (self, players, projectiles, lasers, pickups, flags, doors/ext), AND agent scalars (current weapon, health/armor/ammo, velocity, hook, active tuning vector, tune-zone, race/game state). ⊥ silently omit an available entity/layer. unavailable item → documented `?`, not dropped.
- V29: position-dependent tuning — tuning may differ per DDNet tune-zone. `MapView.TuneZone(tx,ty)` from map Tune layer; `Client.TuningAt(tx,ty)`/`ActiveTuning` resolve it. default tuning ← `Sv_TuneParams` (zone 0). per-zone tuning VALUES ⊥ reliably on wire (server-side `tune_zone` config) — `?`; model still observes the zone INDEX + resulting trajectory, so it can learn zone behavior. prediction uses zone tuning when known, else default; ⊥ assume single global tuning on DDRace maps with tune zones.
- V30: per-tile tuning in observation — obs window includes ONE plane per tuning param, each cell = `TuningAt(tile)` (the tile's zone tuning), so the model sees the physics every tile imposes and can predict movement on tiles it will consume. piecewise-constant per zone. unknown per-zone values → default(zone-0) fallback; the tune-zone-index plane (V28) still separates zones. ⊥ expose only self-tile tuning when full-window per-tile tuning is the observation goal.

## §T — tasks

```
id|status|task|cites
T2|x|research ddnet server events → §I event catalog finalized (this doc); whisper resolved V15|I.catalog
T3|x|define event structs (ChatEvent…WeaponChangeEvent) impl packet.Event|V1,V4,I.catalog
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
T10a|.|reconcile smoothing — RE-SCOPED (B3 reverted, V21): keep prev+cur PredictedWorld, SmoothedCharacters(intraTick) lerp for render|V10a,V11,V21,I.predict
T11|x|tests: own converges (≤rounding), others bounded-err, drift-free N ticks, smoothing no-teleport, disabled==raw|V9,V9a,V10,V10a,V11
T12|.|unified Action type (input/chat/whisper/emoticon/kill/vote/callvote/setteam/spectate) + c.Do(Action) → net6 & net7 send|V18,V22,I.consumer
T13|.|TickState observation struct: predicted local+all chars, projectiles, lasers/pickups/flags, tuning, game/race, events-since-tick|V19,I.consumer
T14|x|MapView: WHOLE-map queries — ALL layers (Solid/Unhook/HookThrough/Death/Freeze/Tele/Speedup/Switch) + TuneZone(tx,ty), OOB→Solid + Window crop, from twmap LayerKind{Game,Front,Tele,Speedup,Switch,Tune}|V19,V26,V28,I.mapview
T15|.|Frontend interface {Mode()TickMode; OnTick(*Client,TickState)[]Action} + WithFrontend/SetFrontend; same plug UI/ML-train/ML-exec|V18,V20,V24,I.consumer
T16|.|tick driver: ONE buildTickState(tick); TickModeFixed loop per predicted tick + TickModeFrame loop (IntraTick+SmoothedCharacters overlay); call OnTick, apply []Action via c.Do|V19,V20,V21,V24,I.consumer
T17|.|tests: Action↔send both protocols; TickState complete; both cadences share builder; one Frontend serves UI+ML plugs; MapView tiles + Window crop correct|V18,V19,V20,V22,V24
T19|x|consolidate redundant types: canonical CharacterState/Vec2/PlayerInput/Weapon/Tuning + single conversion sites; audit & remove dup impls; ⊥ phantom PredictedCharacter|V25
T20|.|ML observation: ego-centric FIXED multi-channel window — ALL static planes (collision+freeze+death+tele+speedup+switch+tune-zone) + per-tile tuning planes (TuningAt per cell) + ALL dynamic entity planes + agent scalars (weapon/hp/vel/hook/active-tuning/tune-zone); config size, square default, OOB=Solid|V26,V27,V28,V30,I.mapview,I.consumer
T21|x|position-dependent tuning: per-tune-zone tuning store; Client.TuningAt(tx,ty) over any tile/window; ActiveTuning; default←Sv_TuneParams; feed predicted world per char's zone; expose in TickState|V29,V30,V9b,I.consumer
```
order: done = T2–T10, T11. active: T19 (consolidate) + T10a (smoothing) + T12–T17, T20, T21.
build order: T19 (consolidate first) → T14 (mapview+layers+window) → T21 (position tuning) → T13 (tickstate) → T12 (actions) → T10a (smoothing) → T15 (frontend) → T16 (driver) → T20 (ML obs) → T17 (tests).

## §R — research refs (verified sources)

catalog + prediction verified against pulled sources:
- DDNet `github.com/ddnet/ddnet@b10c6e4ea` (master, pulled 2026-06-12). msg/obj truth `datasrc/network.py`; 0.7↔0.6 map `src/game/client/sixup_translate_game.cpp`; whisper `src/game/client/components/chat.cpp:731`; prediction `src/game/client/prediction/{gameworld,entities/character,entities/projectile}.cpp` + `src/game/client/gameclient.cpp` (`OnNewSnapshot`, two-world `:2161/2219`, smooth `:2271/2285`, WorldConfig `:2828`).
- Teeworlds 0.7 `github.com/teeworlds/teeworlds@5d68273` (master=0.7, cloned 2026-06-12). 0.7 msg truth `datasrc/network.py`: `Sv_Chat{m_Mode,m_ClientID,m_TargetID,m_pMessage}`, `Sv_Team`, `Sv_ClientInfo/ClientDrop/SkinChange/GameInfo/GameMsg/ServerSettings/RaceFinish/Checkpoint`.
- local: `net6/constants.go`, `net6/reader.go`, `client/snap.go`, `packet/event.go`.

## §B — bugs

```
id|date|cause|fix
B1|2026-06-13|T4e assumed ext snap-objects need new decoder infra; feared blocked. premise WRONG: applyDelta already passes ext items through raw.|RESOLVED in T4e2: marker (type-0, id≥0x4000) carries UUID; ext obj uses type≥0x4000. deriveExt in client/snap.go maps marker UUID→type & decodes DDNetCharacter/Player/SpecChar/Finish. NO decoder change. T4e=DamageInd (vanilla) still valid split.
B2|2026-06-13|T9b needs per-weapon curvature+speed (gun/shotgun); physics.Tuning only had grenade.|RESOLVED: added GunSpeed/Curvature(2200/1.25), ShotgunSpeed/Curvature(2750/1.25) to physics.Tuning (DDNet tuning.h) + Tuning.ProjectilePos (CalcPos formula). PredictedProjectiles() advances snapshot projectiles to predTick. laser = hitscan, no ballistic predict needed.
B3|2026-06-13|T10a reconcile smoothing was render-only; headless client had no renderer → deferred as dead code.|REVERTED 2026-06-13: render/UI consumer + ML consumer now in scope (V21, T15 Frontend). smoothing needed for sub-tick render interpolation. T10a back to `.` (active). "revisit if render consumer added" condition now met.
```
