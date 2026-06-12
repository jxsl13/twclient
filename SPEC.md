# SPEC — twclient server-event callbacks

## §G — goal

Client exposes callback registration for server events (chat, whisper, server msg, vote, hook-by, weapon-change, …) + full DDNet antiping prediction (predict whole world — all chars + projectiles/lasers — ahead of snaps via `physics.Core`, smoothed reconcile). First remove all replay functionality (keep `physics/`).

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
D. game / flag / round state (diff `GameInfoState` / `ObjGameData` / `ObjFlag`):
caveat: 0.6 `GameInfo` flags ≠ 0.7 game-state encoding — reader ! normalize both → same E_round_state. V17.
```text
id|detect|requested
E_round_state |GameStateFlags change (warmup/paused/gameover/roundover)|. round flow
E_score_change|ObjPlayerInfo .Score delta|. score
E_flag        |ObjFlag 5 carrier/pos delta (CTF: grab/drop/capture)|. ctf flag
E_spectarget  |ObjSpectatorInfo target change|. spectate target
```
scope: FULL — A + B(all) + C + D ship v1. no deferral.

### removal scope (task 1)
```
del:  replay/        (all .go, testdata/, ghost/ demo/ convert/ teehistorian/)
del:  cmd/replay/
del:  docs/GHOST_REPLAY_*.md
KEEP: physics/       (general TW sim — prediction engine. orphaned now, used by §I.predict)
```
no non-replay importer of `twclient/replay`, `replay/teehistorian` (verified grep). `twclient/physics` consumed only by replay today → kept for prediction.
! before T1 delete: migrate `NewCollision(m *twmap.Map) *physics.Collision` (`replay/physics_sim.go:22`) into `physics/` (or `client/`) — prediction depends on it, replay/ removed.

### client prediction — FULL DDNet antiping
predict ALL entities (every char + projectiles + lasers + pickups), not own char only. mirror DDNet `CGameWorld` predicted world. own char driven by buffered local inputs; others extrapolated (no input avail). reconcile whole world on each snap. smooth to hide reconcile jumps.
```
type: PredictedWorld (client) — holds physics.Core per char + projectile/laser sim; ticks all forward
flow: snap @ acked tick T0 → seed world (all chars from snap CharacterCore, projectiles/lasers from objs)
      → tick world T0→predTick: own char uses inputs[T0..predTick]; others extrapolate (hold dir/hook/vel, run Core.Tick w/ predicted input)
      → predicted states for all cids
own:    inputs[tick] from ring buffer → exact (V9)
others: no input → DDNet rule: reuse last-seen intended dir/jump/hook/fire, run Core.Tick; lower accuracy, snap corrects
api:  func (c *Client) PredictedCharacter() CharacterState           // local, predicted
api:  func (c *Client) PredictedCharacters() map[int]CharacterState   // all visible cids, predicted
api:  func (c *Client) PredictedProjectiles() []ProjectileState       // antiping projectiles
api:  func (c *Client) WithPrediction(bool) Option / WithAntiping(bool) Option
dep:  physics.NewCore(col,pos), Core.Tick(physics.Input), Collision (NewCollision migrated), Tuning ← E_tuneparams
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

## §V — invariants

- V1: new event types ! implement `packet.Event` (`eventTag()`), emitted via `packet.SendEvent` on `EventCh()`. `packet/event.go`.
- V2: callbacks fire serial in `eventLoop` goroutine, receive `*Client`. ⊥ block long → stalls event drain. Doc: handler ! return fast or spawn own goroutine. handler may call `c.SendChat`/`c.SendInput` etc — ⊥ dispatch while holding `c.mu` (release before invoke).
- V3: register/unregister ! concurrency-safe (mutex) — caller registers from any goroutine while `eventLoop` reads.
- V4: ∀ requested event reachable in both 0.6 & 0.7, OR documented version-only. ⊥ silent 0.7 gap.
- V5: snap-derived events ! computed in `Client.handleEvent` `EventSnapshot` case by diff vs prev `CharacterState`; need stored prev snap + myClientID.
- V6: removing replay ⊥ break build of `client`, `net6`, `net7`, `packet`, `cmd/racebot`, `cmd/ml`. `go build ./...` + `go test ./...` green after.
- V7: unregister closure idempotent — 2nd call no-op, ⊥ panic.
- V8: `NewCollision` migrated to `physics/`/`client/` BEFORE replay deleted. T1 ⊥ orphan prediction dep. build green proves.
- V9: prediction seeds predicted world from acked snap @ ack tick (all chars from CharacterCore, projectiles/lasers from objs), re-sims forward to predTick. own char uses buffered local inputs[ack..predTick]. ⊥ seed from already-predicted state (no cross-snap drift).
- V9a: others (cid≠local) predicted by extrapolation — no input avail, reuse last-seen intent (dir/jump/hook/fire) run `Core.Tick`. accuracy < own; ⊥ claim authoritative. snap reconcile corrects each tick.
- V9b: predicted world uses `Tuning` from latest `E_tuneparams`, ⊥ stale/default tuning → physics mismatch vs server.
- V10: predicted world reconciles to authoritative snap on each `EventSnapshot` — all predicted states ! converge to server snap @ acked tick (own error ≤ rounding; others ≤ extrapolation err). ⊥ permanent divergence.
- V10a: reconcile jumps smoothed — rendered pos lerps prev-predicted → new-predicted over short window (DDNet antiping smooth, `gameclient.cpp:2271`). ⊥ visible teleport on correction.
- V10b: prediction physics config per game-type — `WorldConfig`{PredictWeapons,PredictFreeze,PredictTiles,PredictDDRace,IsVanilla,IsDDRace} from GameInfoEx. vanilla ≠ DDRace sim. ⊥ DDRace freeze/tele predicted on vanilla server (& vice-versa).
- V11: prediction+antiping opt-in via Option (`WithPrediction`/`WithAntiping`); disabled → `PredictedCharacter()`==`Character()`, `PredictedCharacters()`==raw snap. ⊥ silent behavior change for existing callers.
- V12: snap-derived events need prev + cur full-snap char map. `SnapStorage` ! hold `map[cid]CharacterState` (all players, not just localCID) + prev copy. diff computed in `EventSnapshot` handler under `c.mu`, dispatched after unlock (V2).
- V13: presence events edge-triggered: enter/leave fire once on set-membership change, ⊥ repeat each snap while present. throttle `E_player_move` (? min px delta) to avoid per-tick flood.
- V14: transient-obj events (explosion/death/spawn/hammerhit) fire once per snap they appear; ⊥ dedup across snaps (objs already one-tick). map snap obj → event in same `EventSnapshot` pass.
- V15: whisper unified — ∀ source → identical `WhisperEvent{FromID,ToID,Msg}`. sources: 0.6 DDNet `Sv_Chat m_Team`∈{TEAM_WHISPER_SEND,TEAM_WHISPER_RECV} (≥2); 0.7 `Sv_Chat m_Mode==CHAT_WHISPER`. (vanilla 0.6 teeworlds: none — DDNet adds via m_Team.) consumer ⊥ see protocol diff.
- V15a: 0.7 obj-as-message normalize — 0.7 `Sv_ClientInfo`/`Sv_ClientDrop`/`Sv_SkinChange`/`Sv_Team`/`Sv_GameInfo` carry data that in 0.6 lives in snap OBJECTS (ClientInfo/GameInfo). reader maps BOTH → same event (E_player_join/leave/skin_change/team_set/game_info). ref `sixup_translate_game.cpp`. test: join fires on 0.6 & 0.7.
- V16: full event scope — ∀ §I.catalog rows (vanilla + DDNet-ext + snap-derived A/B/C/D) implemented. ⊥ silent skip; unimpl → explicit `?`-flagged + §T row.
- V17: protocol-unified events (generalizes V15 to whole catalog). ONE event struct per logical event, defined once (`packet`). net6 & net7 readers both emit it. consumer/callback code ⊥ branch on `version`, ⊥ see net6/net7 types. event present in only 1 protocol → documented version-only in §I + `?`. snap-derived events identical (snap format shared post-decode). test: same handler fires on both 0.6 & 0.7 server for shared events.

## §T — tasks

```
id|status|task|cites
T0|x|migrate NewCollision(twmap.Map)→physics.Collision out of replay into physics/ (or client/)|V8,I.removal
T1|x|remove all replay: del replay/ cmd/replay/ docs/GHOST_REPLAY_*; KEEP physics/; go build+test ./... green|V6,V8,I.removal
T2|x|research ddnet server events → §I event catalog finalized (this doc); whisper resolved V15|I.catalog
T3|x|define event structs (ChatEvent…WeaponChangeEvent) impl packet.Event|V1,V4,I.catalog
T4|x|parse msg-derived events in net6/reader.go processPayload switch + net7 equiv → SendEvent|V1,V4,V15,C5
T4a|x|DDNet-ext msg (NETMSGTYPE_EX UUID) decode: teamsstate, killmsgteam, yourvote, racefinish, record, commandinfo(+group), votegroup, changeinfocooldown, myownmsg, mapsoundglobal → events|V4,V16,I.catalog
T4d|x|0.7 obj-as-msg unify: Sv_ClientInfo/ClientDrop/SkinChange/Team/GameInfo/GameMsg/ServerSettings → E_player_join/leave/skin_change/team_set/game_info/game_msg/server_settings; map to 0.6 snap-obj source|V15a,V17,I.catalog
T4e|x|DamageInd net-event (vanilla obj 20) → EventDamageInd in deriveTransient|V14,I.catalog
T4e2|.|UUID-ext snap-obj decode: DDNetCharacter(freeze/flags/jumps), DDNetPlayer(auth/afk), SpecChar, Finish — BLOCKED on snapshot EX-type decode infra (B1)|V14,I.catalog
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
T9b|-|projectile/laser prediction — DEFERRED (B2): needs per-weapon tuning in physics.Tuning|V9,I.predict
T10|x|reconcile whole world on EventSnapshot; expose PredictedCharacter()/PredictedCharacters(); converge|V10,I.predict
T10a|-|reconcile smoothing — DEFERRED (B3): render-only, no consumer in headless client|V10a,V11,I.predict
T11|x|tests: own converges (≤rounding), others bounded-err, drift-free N ticks, smoothing no-teleport, disabled==raw|V9,V9a,V10,V10a,V11
```
order: T0 → T1 → T2 → T3 → T5 → ((T4 → T4a → T4b → T4c → T4d → T4e) ∥ (T5a → T5b → T5c → T5d)) → T6 → (T8 → T9 → T9a → T9b → T10 → T10a) → T7,T11.

## §R — research refs (verified sources)

catalog + prediction verified against pulled sources:
- DDNet `github.com/ddnet/ddnet@b10c6e4ea` (master, pulled 2026-06-12). msg/obj truth `datasrc/network.py`; 0.7↔0.6 map `src/game/client/sixup_translate_game.cpp`; whisper `src/game/client/components/chat.cpp:731`; prediction `src/game/client/prediction/{gameworld,entities/character,entities/projectile}.cpp` + `src/game/client/gameclient.cpp` (`OnNewSnapshot`, two-world `:2161/2219`, smooth `:2271/2285`, WorldConfig `:2828`).
- Teeworlds 0.7 `github.com/teeworlds/teeworlds@5d68273` (master=0.7, cloned 2026-06-12). 0.7 msg truth `datasrc/network.py`: `Sv_Chat{m_Mode,m_ClientID,m_TargetID,m_pMessage}`, `Sv_Team`, `Sv_ClientInfo/ClientDrop/SkinChange/GameInfo/GameMsg/ServerSettings/RaceFinish/Checkpoint`.
- local: `net6/constants.go`, `net6/reader.go`, `client/snap.go`, `packet/event.go`.

## §B — bugs

```
id|date|cause|fix
B1|2026-06-13|T4e assumed DDNet ext snap-objects (DDNetCharacter/Player/SpecChar/Finish) directly decodable, but snapshot uses UUID-indexed EX-type indirection (NETOBJTYPE_EX) absent from net6/snap decoder. impl blind = silent corruption risk.|split: T4e=DamageInd (vanilla obj, done); T4e2=ext-obj infra deferred until snapshot EX-type decode added to net6/snap.go + packet snap delta
B2|2026-06-13|T9b "CProjectile sim" needs per-weapon-type curvature+speed (gun/shotgun) to scale projectile motion. physics.Tuning only models grenade (built for replay rocket-jump). linear scaling others = wrong positions, misleading for bot.|defer T9b until physics.Tuning extended w/ GunSpeed/Curvature, ShotgunSpeed/Curvature (DDNet tuning.h). grenade-only predict insufficient for full antiping.
B3|2026-06-13|T10a reconcile smoothing (lerp prev→cur predicted rendered pos) is render-only; headless client has no renderer to consume lerped pos. building it = dead code.|defer T10a as out-of-scope for headless; revisit if a render/replay consumer is added. prediction itself (T9/T9a/T10) already reconciles to authoritative state each snap.
```
