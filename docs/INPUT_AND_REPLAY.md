---
doc_title: Client Input And Replay Semantics
summary: Canonical reference for DDNet player input semantics, physics tick order, and replay timing details.
canonical_for: player input fields, jump and hook semantics, fire parity, physics tick order, replay timing
keywords:
  - input
  - replay
  - jump
  - hook
  - fire parity
  - physics
  - timing
---

# DDNet/Teeworlds Client Input & Replay File Documentation

Use this document for player input behavior and replay timing semantics, not for the full wire protocol.

## When To Read

Read this document when you need:

1. player input field meaning,
2. jump, hook, fire, or weapon-switch semantics,
3. physics tick order,
4. replay timing rules.

## Not For

Do not use this document for:

1. packet wire format,
2. package boundaries,
3. replay experiment history or failure logs.

This document describes in detail how client input works in the DDNet/Teeworlds protocol, how replay files (Ghost, Demo, Teehistorian) are structured, and how we can achieve correct timing during playback.

**Sources:**
- DDNet Source Code: `src/game/gamecore.cpp`, `src/game/client/prediction/entities/character.cpp`, `src/game/client/gameclient.cpp`, `src/game/client/components/ghost.cpp`
- chillerdragon Docs: https://chillerdragon.github.io/teeworlds-protocol/06/system_messages.html
- DDNet libtw2 Docs: https://ddnet.org/docs/libtw2/demo/, https://ddnet.org/docs/libtw2/teehistorian/
- teeworlds-go/protocol: https://github.com/teeworlds-go/protocol

---

## 1. The CNetObj_PlayerInput Struct

The central data type for all player inputs. Each input consists of **10 integer fields**, sent as varints over the network:

```c
struct CNetObj_PlayerInput {
    int m_Direction;      // -1 = left, 0 = stop, 1 = right
    int m_TargetX;        // Cursor X relative to tee (left = negative, right = positive)
    int m_TargetY;        // Cursor Y relative to tee (up = negative, down = positive)
    int m_Jump;           // 1 = jump (ground jump or air jump), 0 = don't jump
    int m_Fire;           // Bit 0 = fire state, Bit 1+ = fire counter (parity flip = new shot)
    int m_Hook;           // 1 = hook active, 0 = release hook
    int m_PlayerFlags;    // Bitmask: Playing, InMenu, Chatting, Scoreboard, AimOnMousepos
    int m_WantedWeapon;   // 1-6 (1=Hammer, 2=Gun, 3=Shotgun, 4=Grenade, 5=Laser, 6=Ninja)
    int m_NextWeapon;     // Counter for next weapon (scroll wheel)
    int m_PrevWeapon;     // Counter for previous weapon (scroll wheel)
};
```

### 1.1 Direction (Movement)

- `-1`: Player presses left arrow (or A key)
- `0`: No horizontal movement
- `1`: Player presses right arrow (or D key)

The physics engine then applies the direction as follows:
```c
// In CCharacterCore::Tick():
float MaxSpeed = Grounded ? m_Tuning.m_GroundControlSpeed : m_Tuning.m_AirControlSpeed;
float Accel = Grounded ? m_Tuning.m_GroundControlAccel : m_Tuning.m_AirControlAccel;
float Friction = Grounded ? m_Tuning.m_GroundFriction : m_Tuning.m_AirFriction;

if(m_Direction < 0)
    m_Vel.x = SaturatedAdd(-MaxSpeed, MaxSpeed, m_Vel.x, -Accel);
if(m_Direction > 0)
    m_Vel.x = SaturatedAdd(-MaxSpeed, MaxSpeed, m_Vel.x, Accel);
if(m_Direction == 0)
    m_Vel.x *= Friction;
```

**Default tuning values:**
- `GroundControlSpeed` = 10.0
- `GroundControlAccel` = 2.0
- `GroundFriction` = 0.5
- `AirControlSpeed` = 5.0
- `AirControlAccel` = 1.5
- `AirFriction` = 0.95

### 1.2 Jumping

The jump mechanism uses a bitfield `m_Jumped`:
- **Bit 0**: Whether a jump has been executed on this input frame (player is holding space bar)
- **Bit 1**: All air jumps used up (dark feet)

```c
// In CCharacterCore::Tick():
if(m_Input.m_Jump)
{
    if(!(m_Jumped & 1))  // Jump not yet registered on this input
    {
        if(Grounded && (!(m_Jumped & 2) || m_Jumps != 0))
        {
            // Ground jump
            m_TriggeredEvents |= COREEVENT_GROUND_JUMP;
            m_Vel.y = -m_Tuning.m_GroundJumpImpulse;  // Default: 13.2
            m_JumpedTotal = 0;
        }
        else if(!(m_Jumped & 2))
        {
            // Air jump (double jump)
            m_TriggeredEvents |= COREEVENT_AIR_JUMP;
            m_Vel.y = -m_Tuning.m_AirJumpImpulse;  // Default: 12.0
            m_Jumped |= 3;  // Set both bits
            m_JumpedTotal++;
        }
    }
}
else
{
    m_Jumped &= ~1;  // Reset jump bit when key released
}
```

**Important for replay:** The jump must be sent as a 0→1 transition. Holding `1` continuously does not trigger a new jump. You must:
1. Send `Jump=1` (execute jump)
2. Send `Jump=0` (release key)
3. Send `Jump=1` (jump again — air jump)

DDRace extensions:
- `m_Jumps = -1`: Only ground jumps allowed
- `m_Jumps = 0`: No jumps at all
- `m_Jumps = 1`: One jump (ground OR air)
- `m_Jumps = 2`: Default (1 ground + 1 air)
- `m_EndlessJump`: Unlimited air jumps (feet stay light)

### 1.3 Grappling Hook

The hook is controlled by `m_Hook` in the input:
- `1`: Fire / hold hook
- `0`: Release hook

The hook direction is determined by `m_TargetX`/`m_TargetY` (cursor position):
```c
vec2 TargetDirection = normalize(vec2(m_Input.m_TargetX, m_Input.m_TargetY));
```

**Hook state machine:**
```
HOOK_IDLE → (Input.Hook=1) → HOOK_FLYING → HOOK_GRABBED → (Input.Hook=0 or Timeout) → HOOK_RETRACTED → HOOK_IDLE
```

Detailed states:
1. **HOOK_IDLE**: No hook active. Hook position = player position.
2. **HOOK_FLYING**: Hook is flying towards `TargetDirection`.
   - Speed: `m_Tuning.m_HookFireSpeed` (Default: 80.0)
   - Maximum range: `m_Tuning.m_HookLength` (Default: 380.0)
   - Checks collision with walls and players.
3. **HOOK_GRABBED**: Hook has attached to a wall or player.
   - On wall: Pulls the player toward the hook position.
   - On player: `m_HookedPlayer` is set, both players influence each other.
   - Hook drag: `m_Tuning.m_HookDragAccel` (Default: 3.0), asymmetric (upward stronger: `y *= 0.3f`)
   - Directional boost: Hook power is amplified when `Direction` and `HookVel.x` point the same way.
4. **HOOK_RETRACT_START → HOOK_RETRACT_END**: Hook is retracting.
5. **HOOK_RETRACTED**: Hook has been retracted.

**Timeout:** Hook releases after `SERVER_TICK_SPEED * 1.25` ticks (≈ 62 ticks at 50 TPS = 1.25 seconds).

**DDRace extensions:**
- `m_EndlessHook`: Hook has no timeout (`m_HookTick = 0` every tick)
- Nohook tiles: `TILE_NOHOOK` causes `HOOK_RETRACT_START` instead of `HOOK_GRABBED`

### 1.4 Weapon Switching and Firing

#### Weapon Switching

Three mechanisms for weapon switching:

1. **Direct selection** (`m_WantedWeapon`): Value 1-6 selects a weapon directly.
2. **Next weapon** (`m_NextWeapon`): Counter increments are counted, skips weapons not owned.
3. **Previous weapon** (`m_PrevWeapon`): Like NextWeapon, but backwards.

```c
void CCharacter::HandleWeaponSwitch()
{
    int WantedWeapon = m_Core.m_ActiveWeapon;

    // Scroll wheel
    int Next = CountInput(m_LatestPrevInput.m_NextWeapon, m_LatestInput.m_NextWeapon).m_Presses;
    int Prev = CountInput(m_LatestPrevInput.m_PrevWeapon, m_LatestInput.m_PrevWeapon).m_Presses;

    while(Next) { WantedWeapon = (WantedWeapon + 1) % NUM_WEAPONS; if(m_Core.m_aWeapons[WantedWeapon].m_Got) Next--; }
    while(Prev) { WantedWeapon = (WantedWeapon - 1 + NUM_WEAPONS) % NUM_WEAPONS; if(m_Core.m_aWeapons[WantedWeapon].m_Got) Prev--; }

    // Direct selection overrides
    if(m_LatestInput.m_WantedWeapon)
        WantedWeapon = m_Input.m_WantedWeapon - 1;  // 1-based → 0-based

    if(WantedWeapon != m_Core.m_ActiveWeapon && m_Core.m_aWeapons[WantedWeapon].m_Got)
        m_QueuedWeapon = WantedWeapon;

    DoWeaponSwitch();  // Switch only when ReloadTimer == 0
}
```

**Important:** The weapon switch is only executed when `m_ReloadTimer == 0` (weapon is not reloading) and Ninja is not active.

#### Firing Weapons

The fire mechanism uses a special parity system:
```c
// Detecting fire:
bool WillFire = CountInput(m_LatestPrevInput.m_Fire, m_LatestInput.m_Fire).m_Presses > 0;

// For auto-fire weapons (Shotgun, Grenade, Laser):
if(FullAuto && (m_LatestInput.m_Fire & 1))
    WillFire = true;
```

**CountInput mechanism:** The fire value is a counter. Bit 0 indicates the current state (1=fire pressed). The upper bits count state changes. A new shot is detected when the counter has increased.

**For replay:** To trigger a shot, the fire counter must be manipulated as follows:
```
Fire = (Fire + 1) | 1    // Press (odd number = fire active)
Fire = (Fire + 1) & ~1   // Release (even number = fire inactive)
```

**Weapon fire delays (default tuning):**
| Weapon    | Fire Delay (ms) | Ticks at 50 TPS |
|-----------|-----------------|------------------|
| Hammer    | 125             | ~6               |
| Gun       | 125             | ~6               |
| Shotgun   | 500             | 25               |
| Grenade   | 500             | 25               |
| Laser     | 800             | 40               |
| Ninja     | 800             | 40               |

### 1.5 Aim / Cursor Position

`m_TargetX` and `m_TargetY` are **relative coordinates** to the tee (not absolute world coordinates).
- `(0, 0)` is not allowed (gets corrected to `(0, -1)`)
- The distance determines cursor distance from the tee
- The angle is computed as: `m_Angle = atan2(m_TargetY, m_TargetX) * 256`

---

## 2. NETMSG_INPUT — Network Input Message

The client regularly sends (~50x/second) its inputs via `NETMSG_INPUT` (System Message ID 16):

```
NETMSG_INPUT:
    AckGameTick    (int)    — Acknowledges the last received snapshot tick
    PredictionTick (int)    — The tick this input is intended for (= PredTick)
    Size           (int)    — Size of the input data (40 = 10 × 4 bytes)
    InputData      (int[10])— CNetObj_PlayerInput fields
```

The relationship between AckGameTick, PredictionTick and the server tick is critical:

```
Client timeline:
  t=0                t=50ms           t=100ms
  |---- Tick N -------|---- Tick N+1 ----|---- Tick N+2 ----|
       ^                    ^
       GameTick        PredTick = GameTick + PredictionLatency
       (last snapshot) (in the future!)
```

### 2.1 INPUTTIMING — Server Feedback (System Message 9)

After receiving an input, the server sends back an `INPUTTIMING` message:

```
INPUTTIMING:
    IntendedTick  (int)  — The PredictionTick of the received input
    TimeLeft      (int)  — Milliseconds until the server actually executes this tick
```

- **TimeLeft > 0**: Input arrived too early → client should decrease PredTick (send slower)
- **TimeLeft < 0**: Input arrived too late → client should increase PredTick (send faster)
- **TimeLeft ≈ 0**: Perfect timing

The client aims to send input approximately `PREDICTION_MARGIN` milliseconds before server execution.

---

## 3. CCharacterCore::Tick — Physics Simulation

Each game tick (50x/second) runs through the following physics pipeline:

```
1. CCharacterCore::Tick(UseInput=true)
   ├── Gravity:         m_Vel.y += m_Tuning.m_Gravity  (Default: 0.5)
   ├── Ground check:    IsOnGround(m_Pos, PhysicalSize)
   ├── Read input:      m_Direction = m_Input.m_Direction
   ├── Set angle:       m_Angle = atan2(TargetY, TargetX) * 256
   ├── Jump handling:   (see section 1.2)
   ├── Hook handling:   (see section 1.3)
   ├── Movement:        SaturatedAdd based on Direction
   └── TickDeferred:    Player collisions, velocity clamp (max 6000)

2. CCharacterCore::Move()
   ├── VelocityRamp:    Speed curve for high velocities
   ├── MoveBox:         Collision with world (walls, floor, ceiling)
   ├── Ground reset:    On ground → Jumped &= ~2, JumpedTotal = 0
   └── Player collision: Physical blocking by other players

3. CCharacterCore::Quantize()
   ├── Write → Read:    Round float values to network integers
   └── Position snap:   Ensure exact reproducibility
```

### 3.1 Velocity Ramp

At high speeds (e.g. from speedup tiles), a damping factor is applied:
```c
float VelocityRamp(float Value, float Start, float Range, float Curvature) {
    if(Value < Start) return 1.0f;
    return 1.0f / pow(Curvature, (Value - Start) / Range);
}
```
Default: `VelrampStart=550, VelrampRange=2000, VelrampCurvature=1.4`

---

## 4. Input Prediction (Client-Side Prediction)

Since the network has latency, the player would experience a delay between key press and reaction without prediction. DDNet solves this with client-side prediction:

### 4.1 PredictedTime System

```go
// Simplified from client/predicted_time.go:
type PredictedTime struct {
    baseTick    int       // Last confirmed server tick (from snapshot)
    baseTime    time.Time // Timestamp of the snapshot
}

// Current prediction tick:
func (pt *PredictedTime) PredTick() int {
    elapsed := time.Since(pt.baseTime)
    elapsedTicks := int(elapsed / (time.Second / SERVER_TICK_SPEED))
    return pt.baseTick + elapsedTicks + 1  // +1: Always one tick ahead
}
```

### 4.2 Prediction Loop

The client simulates the game world locally ahead of time:
```
UpdatePrediction():
  1. Copy GameWorld (snapshot-based)
  2. For each tick from GameTick+1 to PredGameTick:
     a. Fetch stored input for this tick
     b. pLocalChar->OnDirectInput(pInput)    // Weapons, fire events
     c. pLocalChar->OnPredictedInput(pInput)  // Movement, hook
     d. m_GameWorld.Tick()                    // Simulate all entities
  3. Store PredictedChar + PrevPredictedChar for rendering
```

**OnDirectInput vs OnPredictedInput:**
- `OnDirectInput`: Processes "immediate" events (weapon switch, fire). Uses `CountInput` with previous input for edge detection.
- `OnPredictedInput`: Copies the input into `m_Input` for the tick simulation (direction, jump, hook).

### 4.3 Input Buffer

The client stores all sent inputs in a ring buffer (200 slots):
```c
struct {
    int m_aData[MAX_INPUT_SIZE];      // The 10 input fields
    int m_Tick;                       // Which tick this is for
    int64_t m_PredictedTime;          // Predicted time when sent
    int64_t m_PredictionMargin;       // Margin when sent
    int64_t m_Time;                   // Wall clock time when sent
} m_aInputs[NUM_DUMMIES][200];
```

The prediction loop can then fetch the matching input from the buffer for each simulated tick:
```c
CNetObj_PlayerInput *pInput = Client()->GetInput(Tick);
```

### 4.4 Smooth Rendering

The rendered position interpolates between predicted positions:
```c
// For local player:
m_LocalCharacterPos = mix(m_PredictedPrevChar.m_Pos, m_PredictedChar.m_Pos,
                          Client()->PredIntraGameTick());

// For other players (snapshot-based):
vec2 Pos = mix(
    vec2(Prev.m_X, Prev.m_Y),
    vec2(Cur.m_X, Cur.m_Y),
    Client()->IntraGameTick());
```

---

## 5. Replay File Formats

### 5.1 Ghost Files (.gho)

Ghost files record the **visible position and state** of a player per tick. They contain NO raw input, only derived state data.

#### Format structure:
```
Ghost File:
  Header:
    [8]  Magic: "TWGHOST\0"
    [1]  Version: 3-6
    [16] Owner Name (null-terminated)
    [64] Map Name (null-terminated)
    [4]  Map CRC (or 0)
    [4]  NumTicks
    [4]  Time (milliseconds)
    [32] SHA256 (from version 6)

  Data Chunks (max 6400 bytes per chunk):
    [2]  NumItems (uint16)
    [2]  DataSize (uint16)
    [ ]  Items (compressed or uncompressed)

  Data Types:
    0 = SKIN:              [36 bytes] (9 × int32: name as IntPack + colors)
    1 = CHARACTER_NO_TICK: [44 bytes] (11 × int32, without tick field)
    2 = CHARACTER:         [48 bytes] (12 × int32, with tick field)
    3 = START_TICK:        [4 bytes]  (int32)
```

#### CharacterFrame fields:
```
CGhostCharacter {
    m_X          int32  // Absolute X position (world coordinates)
    m_Y          int32  // Absolute Y position
    m_VelX       int32  // X velocity × 256
    m_VelY       int32  // Always 0 in ghost! (design decision)
    m_Angle      int32  // View angle × 256
    m_Direction  int32  // -1, 0, 1
    m_Weapon     int32  // 0-5 (active weapon)
    m_HookState  int32  // HOOK_IDLE through HOOK_RETRACTED
    m_HookX      int32  // Hook position X
    m_HookY      int32  // Hook position Y
    m_AttackTick int32  // Tick of last attack
    m_Tick       int32  // Game tick (only for TYPE_CHARACTER)
}
```

#### Recording:
- Recorded in `CGhost::OnNewSnapshot()` — called on each new server snapshot.
- Per snapshot, a `CGhostCharacter` is extracted from the current `CNetObj_Character` and optionally `CNetObj_DDNetCharacter`.
- Recording starts when crossing the start line and stops at finish.

#### Playback:
```c
void CGhost::OnRender()
{
    int PlaybackTick = Client()->PredGameTick() - m_StartRenderTick;

    for(auto &Ghost : m_aActiveGhosts)
    {
        int GhostTick = Ghost.m_StartTick + PlaybackTick;

        // Find the matching frame
        while(Ghost.m_PlaybackPos >= 0 &&
              Ghost.m_Path.Get(Ghost.m_PlaybackPos)->m_Tick < GhostTick)
            Ghost.m_PlaybackPos++;

        int CurPos = Ghost.m_PlaybackPos;
        int PrevPos = maximum(0, CurPos - 1);

        // Interpolation between two frames
        int TickDiff = Player.m_Tick - Prev.m_Tick;
        float IntraTick = (GhostTick - Prev.m_Tick - 1 + PredIntraGameTick) / TickDiff;

        // Render with interpolated position
        RenderPlayer(&Prev, &Player, pRenderInfo, -2, IntraTick);
    }
}
```

**Important timing details:**
- Ghost playback is based on `PredGameTick` (client prediction tick), not the raw server tick.
- Synchronization happens via the ghost's `m_StartTick` and the local race start's `m_StartRenderTick`.
- Intra-tick interpolation ensures smooth display between the discrete 50 Hz frames.

### 5.2 Demo Files (.demo)

Demo files record the **complete network traffic** between server and client — snapshots, messages, and timing.

#### Format structure:
```
Demo File:
  [  8] VersionHeader: "TWDEMO\0" + Version (3-5)
  [168] Header:
        [64] net_version
        [64] map_name
        [ 4] map_size (BE int32)
        [ 4] map_crc (BE int32)
        [ 8] type ("client" or "server")
        [ 4] length (game duration in ticks)
        [20] timestamp
  [260] TimelineMarkers (version 4-5 only)
  [   ] Map (embedded Teeworlds map)
  [   ] Data Chunks
```

#### Chunk structure:
```
Chunk Types:
  Tick marker:
    Bit 7:   is_tick = 1
    Bit 6:   keyframe (contains full snapshot)
    Bit 0-5: tick_delta (0 = absolute tick follows as BE int32)

  Data chunk:
    Bit 7:   is_tick = 0
    Bit 5-6: type (1=Snapshot, 2=Message, 3=SnapshotDelta)
    Bit 0-4: size (30=+1 byte, 31=+2 bytes)
```

#### Timing:
- Each data chunk is associated with a tick.
- The demo stores **tick deltas** (difference to previous tick), not absolute timestamps.
- Playback speed: 1 tick = 20ms at default tick rate (50 TPS).
- The demo contains snapshot deltas; the player reconstructs the full game state from them.
- Timeline markers allow fast seeking to keyframes.

### 5.3 Teehistorian Files

Teehistorian is a **server-side format** that records ALL inputs from all players. It is the most accurate format for reproducing game runs.

Unlike demo and ghost files which are client-side (single player perspective), teehistorian records every player's raw inputs simultaneously. Each `INPUT_NEW`/`INPUT_DIFF` carries a `cid` (client ID), so a single file contains interleaved input streams for up to 64 players. The `JOIN`/`DROP` messages track when players connect and disconnect.

When reading a teehistorian file for training, a specific CID must be selected (or -1 to auto-select the first player seen). The `Loader` internally maintains input state for all 64 CIDs but only yields frames for the selected one.

When **writing** teehistorian from an `InputProvider` (which is a single-stream interface), the converter assigns all inputs to the specified CID. Multi-CID writing would require a different interface.

#### Format structure:
```
Teehistorian File:
  [16] UUID: 699db17b-8efb-34ff-b1d8-da6f60c15dd1
  [  ] JSON Header (null-terminated):
       {"version":"2", "game_type":"DDraceNetwork", "map":{"name":"...","sha256":"...","size":N}, ...}
  [  ] Message Stream
```

#### Message types:
```
ID  Name          Payload
──────────────────────────────────────────────────────────
0-63 PLAYER_DIFF   dx(int) dy(int)           — Position delta for CID=MsgID
-1   FINISH        (none)                     — End of file
-2   TICK_SKIP     dt(int)                    — dt+1 ticks skipped
-3   PLAYER_NEW    cid(int) x(int) y(int)     — New player character
-4   PLAYER_OLD    cid(int)                   — Player character removed
-5   INPUT_DIFF    cid(int) dinput(int[10])    — Input delta from previous input
-6   INPUT_NEW     cid(int) input(int[10])     — First/full input
-7   MESSAGE       cid(int) msgsize(int) msg   — Game message
-8   JOIN          cid(int)                    — Player joined
-9   DROP          cid(int) reason(str)        — Player dropped
-10  CONSOLE_CMD   cid(int) flags(int) cmd(str) num_args(int) args — Rcon command
-11  EX            uuid(16) size(int) data     — Extension message

Extended EX messages:
  - DDNETVER: Client version
  - AUTH_INIT/LOGIN/LOGOUT: Rcon auth
  - TEAM_SAVE/LOAD: Team saves
  - PLAYER_TEAM: Team change
  - PLAYER_READY: Spawn ready
  - PLAYER_SWITCH: Tee swap
```

#### Tick logic:
Ticks are counted implicitly. A new tick is assumed when a PLAYER_DIFF/NEW/OLD message appears for a **lower CID** than the previous one:
```
PLAYER_DIFF cid=0 ...  → Tick N
PLAYER_DIFF cid=5 ...  → Still Tick N
PLAYER_NEW  cid=3 ...  → Tick N+1 (CID 3 < CID 5 → implicit tick!)
```

TICK_SKIP(dt) skips dt+1 ticks (no inputs during those ticks).

---

## 6. Replay Timing: Correct Playback

### 6.1 The Timing Problem

Each replay format has different timing characteristics:

| Format        | Time basis       | Precision    | What is stored                |
|--------------|-----------------|--------------|-------------------------------|
| Ghost (.gho) | Per-tick state   | 1 tick (20ms)| Position, angle, state        |
| Demo (.demo) | Tick deltas      | 1 tick (20ms)| Snapshots + messages          |
| Teehistorian | Server ticks     | 1 tick (20ms)| Raw inputs + positions        |

**The core problem:** The server runs at exactly 50 ticks/second. Each tick lasts exactly 20ms. Inputs are processed BEFORE the tick they are intended for. Replay playback must reproduce this timing exactly.

### 6.2 Ghost → Input Derivation

Since ghost files contain no raw input, it must be derived from the state data (already implemented in `CharacterToInputAdapter`):

```
Direction:  From X position delta between frames
            ΔX > 0 → Direction = 1
            ΔX < 0 → Direction = -1
            Otherwise → Direction = 0

Jump:       From Y position delta (NOT VelY, which is always 0 in ghost)
            ΔY < -5 (moving upward) AND lastJump == false → Jump = 1, lastJump = true
            ΔY < -5 AND lastJump == true → Jump = 1 (hold through arc)
            Otherwise → Jump = 0, lastJump = false

Hook:       HookState >= HOOK_RETRACT_START → Hook = 1, Target = (HookX-X, HookY-Y)
            HookState < HOOK_RETRACT_START → Hook = 0

Fire:       AttackTick changes from prev frame:
            → fireCounter = (fireCounter + 1) | 1   // press (odd)
            AttackTick same as prev AND fireCounter is odd:
            → fireCounter = (fireCounter + 1) & ~1  // release (even)

Weapon:     Weapon field from ghost → WantedWeapon + 1

Aim:        Angle field → TargetX = cos(Angle/256) × 256
                        → TargetY = sin(Angle/256) × 256
```

**Limitations of ghost derivation:**
- **VelY is always 0** in ghost → jump detection uses Y position deltas (dy < -5) instead of velocity
- **Jump edge detection** → the server requires 0→1 transitions; adapter tracks lastJump state
- **Fire parity counter** → AttackTick change triggers a monotonic counter flip, not raw AttackTick
- **No sub-tick information** → if multiple actions happen in the same tick, information is lost
- **No aim interpolation** → cursor moves in steps instead of smoothly
- **Fire timing imprecise** → AttackTick only shows the last shot, not multiple rapid shots

### 6.3 Tick-Accurate Replay Sending

For correct timing, our replay client must send inputs at the **correct PredictionTick**:

```
Replay strategy:
  1. Connect to server (handshake, load map)
  2. Start PredictedTime system
  3. Start race (cross start line)
  4. Per tick:
     a. Wait until PredTick reaches the next replay tick
     b. Fetch the input for this tick from the replay file
     c. Send the input via NETMSG_INPUT with:
        - AckGameTick = last received snapshot tick
        - PredictionTick = current PredTick
        - InputData = replay input (10 fields)
     d. Process INPUTTIMING feedback from server
        → Adjust PredictedTime accordingly
```

### 6.4 Timing Synchronization

The most critical problem: **When exactly do we send which input?**

```
Timeline alignment:

Ghost file:    Tick 0   Tick 1   Tick 2   Tick 3   ...
               |--------|--------|--------|--------|
               Input[0] Input[1] Input[2] Input[3]

Server:        ... Tick 5000  Tick 5001  Tick 5002  ...
               |------------|----------|----------|
               (Race Start)

Mapping:       Ghost Tick N → Server Tick (RaceStartTick + N)
```

**PredTick-based mapping:**
```go
// For each frame:
predTick := predictedTime.PredTick()
replayTick := predTick - raceStartTick

if replayTick >= 0 && replayTick < len(replayInputs) {
    input := replayInputs[replayTick]
    sendInput(ackGameTick, predTick, input)
}
```

### 6.5 Timing Correctness Verification

The DDNet ghost system uses the following synchronization mechanisms:

1. **StartTick alignment**: Ghost `m_StartTick` is mapped to the server RaceStartTick.
2. **Tick-by-tick playback**: Exactly one ghost frame is played per server tick.
3. **Intra-tick interpolation**: Rendering interpolates between frames for 60+ FPS.
4. **Server feedback**: INPUTTIMING messages correct the prediction timing.

### 6.6 Teehistorian Replay (Ideal Case)

Teehistorian contains the **exact server-side inputs** per tick. This means:

```
For each tick in teehistorian:
  1. INPUT_NEW/INPUT_DIFF gives the exact player input
  2. PLAYER_DIFF/PLAYER_NEW gives the resulting position
  3. → Send input 1:1 over the network

Timing guarantee:
  - Teehistorian input for tick N → send as PredictionTick = RaceStartTick + N
  - Server executes the same physical tick
  - Identical result (deterministic!)
```

**Advantages over ghost:**
- Exact input, no derivation needed
- Fire parity correct
- Sub-tick events preserved (multiple shots per tick)
- Aim data exact
- Jump hold vs jump tap correctly distinguishable

### 6.7 Server Tick Rate Assumption

**IMPORTANT:** All formats assume a fixed tick rate of **50 TPS** (20ms per tick). The server value `SERVER_TICK_SPEED = 50` is hardcoded. There is no variable tick rate in the DDNet protocol.

```
Timing constants:
  Tick duration:    20 ms
  Ticks/second:     50
  Snap rate:        ~20-25 Hz (≈ every other tick)
  Input rate:       ~50 Hz (every tick)
```

### 6.8 Recommended Implementation Strategy

```
1. Ghost replay (already implemented):
   ├── Load ghost → extract CharacterFrames
   ├── CharacterToInputAdapter: Frames → derive PlayerInputs
   ├── Connect to server, start race
   ├── Manage PredictedTime (with INPUTTIMING feedback)
   └── Per tick: send derived input

2. Teehistorian replay (more accurate):
   ├── Load teehistorian → extract InputFrames (INPUT_NEW/INPUT_DIFF)
   ├── Use inputs per tick directly (no derivation needed!)
   ├── Connect to server, start race
   ├── Manage PredictedTime
   └── Per tick: send exact input

3. Demo replay (possible but complex):
   ├── Load demo → decode snapshots + messages
   ├── Extract snap CNetObj_Character → treat like ghost
   ├── OR: Extract CNetMsg_Cl_Input messages → treat like teehistorian
   └── Timing via tick markers
```

---

## 7. DDRace-Specific Physics Details

### 7.1 Freeze Mechanic

In the freeze state, certain inputs are blocked:
```c
if(m_FreezeTime > 0) {
    m_Input.m_Direction = 0;  // No movement
    m_Input.m_Jump = 0;       // No jumping
    m_Input.m_Hook = 0;       // No hooking (except in live freeze)
}
```

### 7.2 Tile-Based Modifications

DDRace maps can modify physics per player via tiles:
- **Freeze/Unfreeze**: Freeze/release movement
- **Endless Hook**: Hook has no timeout
- **Unlimited Jumps**: Unlimited air jumps
- **Solo**: No collision with other players
- **Jetpack**: Gun fires as recoil instead of projectile
- **Speedup Tiles**: Additional velocity in a direction
- **Tune Zones**: Modified physics parameters in specific areas

### 7.3 Velocity Units

```
Position:     1 unit = 1 pixel (at zoom 1.0)
Velocity:     Units per tick
              Network: m_VelX = vel.x × 256 (fixed point)
Tiles:        32 × 32 pixels per tile
PhysicalSize: 28 pixels (bounding box side length of tee)
TeeRadius:    14 pixels (half bounding box, center→edge)
```

### 7.4 Tee Physical Size and Collision

The tee has a **28×28 world unit bounding box** (`CCharacterCore::PhysicalSize() = 28.0f`).

```c
// From DDNet gamecore.h:
static constexpr float PhysicalSize() { return 28.0f; }
static constexpr vec2 PhysicalSizeVec2() { return vec2(28.0f, 28.0f); }
```

**Collision checks:**
- `TestBox(Pos, Size)`: checks corners at `Pos ± Size/2` = `Pos ± 14`
- `IsOnGround(Pos, Size)`: checks `(Pos.x ± 14, Pos.y + 14 + 5)`, i.e. 19 units below center
- `MoveBox`: resolves solid collisions using the 28×28 box
- Player collision: triggers at `distance < PhysicalSize()` = 28 units

**Tile triggers:** checked at the tee's **center position** via `GetMapIndex(Pos)`, which converts to `Pos / 32`. This means the tee triggers a start tile when its center overlaps the tile, not when the bounding box edge touches it.

---

## 8. Navigation and Pathfinding

The replay bot must navigate from its spawn position to the start line before playback begins. This is handled by the `WalkToStart` function and the A* pathfinder.

### 8.1 A* Pathfinding

The pathfinder operates on the game layer tile grid. It models Teeworlds physics for neighbor discovery:

**Movement types in pathNeighbors:**

| Movement | Description | Conditions |
|----------|-------------|------------|
| Walk left/right | Move to adjacent passable tile | Tile is not solid and not dangerous |
| Fall | Gravity drop through air tiles | Not on ground; scan down up to 30 tiles for landing |
| Jump straight up | Jump 1–5 tiles upward | On ground; clear path above |
| Jump diagonal | Jump up 1–5 tiles + 1 tile left/right | On ground; clear path |
| Jump across gap | Jump 2–4 tiles horizontal, 0–3 up | On ground; clear horizontal path |
| Hook swing | Hook to solid surface, swing to landing | Hookable solid within 10 tiles with line-of-sight |

**Hook pathfinding (`hookNeighbors`):**
- Scans for hookable solid tiles within a 10-tile radius
- Filters out `TileUnhookable` tiles
- Checks line-of-sight via Bresenham ray march (`hasLineOfSight`)
- For each hookable anchor, considers landing tiles within ±3 tiles that have solid ground below

### 8.2 Navigation Loop (`walkToTile`)

The navigation loop runs at 50 Hz (20ms tick) and drives the bot along the A* path:

```
walkToTile(goal, goalWX, goalWY):
  loop:
    1. Check context/deadline/connection errors
    2. Check if race already started → return immediately
    3. Check arrival: |actual - target| ≤ TeeRadius (14 units)
    4. Recompute A* path if tile position changed
    5. Find next waypoint in path
    6. Generate input:
       a. Direction: steer toward next waypoint X
       b. If waypoint requires hook (>5 tiles up, or far diagonal):
          - Find hookable tile via findHookTarget
          - Set Hook=1, aim at hook target
       c. Else if waypoint is above: Jump=1
    7. SendInput
```

**Key details:**
- Arrival threshold uses `TeeRadius` (14 world units), not tile size
- The goal position uses the exact world coordinates from the ghost file, not tile center
- Navigation exits early when `nav.RaceStarted()` returns true (race timer activated)
- Hook target selection (`findHookTarget`) prefers solid tiles above and toward the goal, scored by proximity to goal

### 8.3 WalkToStart

The `WalkToStart` function accepts the exact world coordinates from the ghost file's first frame as the target. This ensures the bot arrives at the precise position where the recording began.

```go
func WalkToStart(ctx, nav, targetWX, targetWY, logger) error
```

The function returns when any of these conditions is met:
1. Bot's center is within TeeRadius (14 units) of the target
2. The race timer starts (bot crossed the start line)
3. Context cancelled or 30s timeout

---

## 9. Drift Correction During Replay

During replay playback, the bot's actual position may drift from the recording's expected position due to:
- Network latency variations
- Server-side physics differences (other players, tune zones)
- Rounding differences in input derivation (ghost files)
- Timing jitter in tick delivery

### 9.1 Position Tracking in InputFrame

Each `InputFrame` carries the expected world position alongside the input:

```go
type InputFrame struct {
    Tick  int
    Input packet.PlayerInput

    // Expected world position at this tick (from ghost CharacterFrame).
    ExpectedX, ExpectedY int
    HasPos               bool  // false for pure-input formats
}
```

The `CharacterToInputAdapter` populates these fields from the ghost's `CharacterFrame.X/Y` during conversion.

### 9.2 Correction Logic (`ReplayFrameCorrected`)

The `Replayer.ReplayFrameCorrected(idx, actualX, actualY)` method compares the bot's actual position against the expected position and applies corrections:

```
dist = distance(actual, expected)

if dist ≤ TeeRadius (14 units):
  → Return original replay input unchanged
  → Bot is within its own collision radius of the target

if dist > TeeRadius:
  → Override movement inputs to close the gap:
    - Direction: steer toward expected X
    - Jump: activate if expected Y is significantly above (dy < -5)
    - Aim: point toward expected position
  → Preserve action inputs from original recording:
    - Fire (weapon shots)
    - Hook state
    - Weapon selection
    - Player flags
  → Look-ahead: if next frame moves in same direction as correction,
    keep original fire/hook (bot is on correct trajectory, just displaced)
```

The TeeRadius threshold means the bot tolerates positional drift up to the tee's own bounding box radius — the bounding box still overlaps the expected position, so tile triggers (start, finish, checkpoints) fire correctly.

### 9.3 Integration in Replay Loop

The replay command uses `ReplayFrameCorrected` instead of `ReplayFrame`:

```go
// In cmd/replay/main.go replay loop:
curX, curY := nav.CharacterPos()
input, ok := rpl.ReplayFrameCorrected(targetIdx, curX, curY)
cl.SendInput(input)
```

This runs every tick, so corrections are applied continuously and reactively.

---

## 10. Summary for Our Project

### What we have:
- `replay/ghost/ghost.go`: Ghost parser → `CharacterFrame`
- `replay/teehistorian/teehistorian.go`: Teehistorian parser → `InputFrame`
- `replay/replay.go`: `CharacterToInputAdapter` (Ghost→Input derivation) with position tracking
- `replay/pathfind.go`: A* pathfinder with walk, jump, fall, hook-swing neighbor discovery
- `replay/navigate.go`: Navigation loop (`WalkToStart`, `walkToTile`) with tee physics constants
- `replay/replayer.go`: `ReplayFrameCorrected` for drift correction during playback
- `client/predicted_time.go`: PredictedTime management with INPUTTIMING feedback
- `client/input.go`: Input packing (10 varints)
- `testdata/Tutorial.gho`: Reference ghost recording (Tutorial map, 287.86s)
- `testdata/Tutorial.demo`: Reference demo recording (Tutorial map, completes)

### What we need for perfect timing:
1. ~~**Tick-accurate input delivery**~~: ✅ Input for replay tick N is sent as PredictionTick = RaceStartTick + N (implemented in `cmd/replay/main.go`).
2. ~~**PredictedTime calibration**~~: ✅ INPUTTIMING feedback adjusts prediction offset (implemented in `client/predicted_time.go`).
3. ~~**Race start detection**~~: ✅ Detected via `GAMESTATEFLAG_RACETIME` in `SnapStorage.updateRaceTime()`, exposed via `Client.RaceTime()`.
4. ~~**Tick synchronization**~~: ✅ Replay maps `predTick - raceStartTick` to frame index (implemented in `cmd/replay/main.go`).
5. ~~**Teehistorian exact inputs**~~: ✅ Used 1:1 via `teehistorian.Loader.NextInput()` → `InputFrame`.
6. ~~**Ghost derivation improvements**~~: ✅ Fixed VelY=0 jump detection (Y position deltas), fire parity counter, jump release tracking.
7. **Remaining**: Anti-prediction (cl_antiping style) is not implemented — not needed for headless replay.
8. ~~**Navigation to start**~~: ✅ A* pathfinding on game layer tiles with walk, jump, fall, and hook-swing movement. Bot navigates from spawn to ghost start position using exact world coordinates. Exits early on race start detection.
9. ~~**Tee physics constants**~~: ✅ `TeePhysicalSize = 28`, `TeeRadius = 14` from DDNet `CCharacterCore::PhysicalSize()`. Used for arrival threshold and drift correction.
10. ~~**Drift correction**~~: ✅ `ReplayFrameCorrected` compares actual vs expected position each tick. Within TeeRadius: use replay input as-is. Beyond TeeRadius: override direction/jump/aim to close gap while preserving fire/hook/weapon from recording.

### Known limitations:
- Ghost VelY is always 0 → jump derivation is heuristic
- Ghost has no sub-tick information
- Other players on the server affect physics (hook collisions, collisions)
- Tune zones can be configured differently on different servers
- The server may have slightly different tuning parameters than during the original run

---

## 10.5 Format Conversion (`replay/convert`)

All three replay formats (ghost, demo, teehistorian) implement `InputProvider`, which yields `(tick, PlayerInput)` pairs. The `convert` package uses this interface as a bridge for bidirectional conversion.

### Conversion matrix:

| From → To       | Teehistorian | Demo  | Ghost |
|-----------------|-------------|-------|-------|
| **Ghost**       | ✅ lossy    | ✅ lossy | —    |
| **Demo**        | ✅ lossy    | —     | ✗     |
| **Teehistorian**| —           | ✅ lossy | ✗   |

### What is lost:

| Conversion              | Lost data |
|------------------------|-----------|
| Ghost → Teehistorian   | Exact positions/velocities (replaced by zero); multi-player context |
| Ghost → Demo           | Same as above + snapshot fidelity |
| Demo → Teehistorian    | Full snapshot state; game messages; delta compression |
| Teehistorian → Demo    | Multi-player data (only selected CID); game messages; console commands |

### Multi-CID behavior:

Teehistorian is the only format that natively records multiple players. When writing:
- `ToTeehistorian(src, cid)` assigns all inputs from `src` to the given `cid`.
  The output file contains just one player (single JOIN/PLAYER_NEW/inputs/PLAYER_OLD/FINISH).
- To create multi-CID teehistorian, multiple InputProviders would need to be interleaved (not yet implemented).

When reading:
- `teehistorian.Open(file, cid)` selects one CID. Use `cid=-1` to auto-select the first player.
- `teehistorian.Loader.CIDs()` lists all CIDs that have inputs in the file.

---

## 11. Implementation Revision Log

| Date       | Change | Source |
|------------|--------|--------|
| 2026-04-08 | **CharacterToInputAdapter: Fixed jump detection** — old code used `VelY < -128` which never triggers because ghost VelY is always 0. New code uses Y position delta (`dy < -5`) to detect jumps from frame-to-frame position changes. Added jump-release tracking: the server requires a `0→1` transition on `m_Jump` to trigger a new jump (`CCharacterCore::Tick` checks `m_Jumped & 1`), so the adapter must send `Jump=0` between consecutive jump frames. | DDNet `src/game/gamecore.cpp`, ghost testing |
| 2026-04-08 | **CharacterToInputAdapter: Fixed fire parity counter** — old code set `Fire = FireCount(cur.AttackTick)` which is meaningless (AttackTick is an absolute tick number, not a fire counter). New code maintains a monotonically increasing parity counter: on each AttackTick change, `fireCounter = (fireCounter + 1) | 1` (odd = pressed); on the next frame without a new shot, `fireCounter = (fireCounter + 1) & ^1` (even = released). This matches `CountInput()` semantics in DDNet. | DDNet `src/game/server/entities/character.cpp`, `protocol.h` CountInput |
| 2026-04-08 | **Replay command: PredTick-based frame scheduling** — old code used a `time.NewTicker(20ms)` which drifts vs server tick timing and ignores INPUTTIMING feedback. New code polls at 1ms resolution and only sends input when `PredTick()` crosses a new boundary, indexed to `raceStartTick`. This matches DDNet's `CClient::Update()` which calls `SendInput()` inside the `if(NewPredTick > m_aPredTick)` block. | DDNet `src/engine/client/client.cpp` CClient::Update() |
| 2026-04-08 | **Replay command: Pre-buffers all frames** — instead of streaming frames one-by-one from the file while also time-synchronizing, the replay command now loads all frames into memory upfront and indexes into them by `predTick - raceStartTick`. This avoids the streaming/timing coupling problem and enables frame skipping on lag spikes. | n/a |
| 2026-04-08 | **Client: Added PredTick() and AckTick() accessors** — `client.Client` now exposes `PredTick()` and `AckTick()` for external callers (replay command, ML bots) to read the current prediction state without accessing internals. | n/a |
| 2026-04-08 | **Confirmed: DDNet SendInput is PredTick-gated** — verified in DDNet source that `CClient::Update()` only calls `SendInput()` when `NewPredTick > m_aPredTick[ClDummy]`, meaning input is sent exactly once per prediction tick boundary. Our `Client.SendInput()` has the same gate pattern: `predTick != c.lastInputTick`. | DDNet `src/engine/client/client.cpp` lines ~3200-3230 |
| 2026-04-08 | **Confirmed: PredictionMargin is 10ms** — DDNet uses `m_ServerCapabilities.m_SyncWeaponInput ? g_Config.m_ClPredictionMargin : 10`. Since we don't negotiate SyncWeaponInput, the default 10ms in `predicted_time.go` is correct. | DDNet `CClient::PredictionMargin()` |
| 2026-04-08 | **Tee physical constants added** — `TeePhysicalSize = 28`, `TeeRadius = 14` (in world units). From DDNet `CCharacterCore::PhysicalSize() = 28.0f`. The 28×28 bounding box is used in `TestBox`, `MoveBox`, `IsOnGround`. Tile triggers fire at the tee's center position. | DDNet `src/game/gamecore.h` |
| 2026-04-08 | **A* pathfinder: added hook-swing neighbors** — `pathNeighbors` now includes `hookNeighbors` which scans for hookable solid tiles within 10 tiles, checks line-of-sight via Bresenham ray, and yields landable positions near the hookable anchor. | DDNet hookLength=380 ≈ 12 tiles |
| 2026-04-08 | **Navigator: hook input generation** — `walkToTile` detects when the next waypoint is beyond jump range (>5 tiles up, or far diagonal) and activates hook input aimed at the nearest hookable solid. Uses `findHookTarget` to select the best solid tile above/toward the goal. | n/a |
| 2026-04-08 | **WalkToStart: exact world-coordinate target** — `WalkToStart` now accepts `targetWX, targetWY` (from ghost first frame) instead of searching for the nearest start tile. Arrival uses exact world coordinates with TeeRadius threshold. | n/a |
| 2026-04-08 | **Navigator: race-start early exit** — `walkToTile` checks `nav.RaceStarted()` each tick and returns immediately when the race timer starts, preventing the bot from getting stuck jumping at the start line. | n/a |
| 2026-04-08 | **InputFrame: position tracking** — `InputFrame` now carries `ExpectedX`, `ExpectedY`, `HasPos` fields. The `CharacterToInputAdapter` populates these from the ghost's `CharacterFrame.X/Y` during conversion. | n/a |
| 2026-04-08 | **Drift correction: ReplayFrameCorrected** — New `Replayer.ReplayFrameCorrected(idx, actualX, actualY)` method. Compares actual bot position vs expected. Within TeeRadius (14 units): no correction. Beyond: overrides direction/jump/aim to close gap while preserving fire/hook/weapon. Look-ahead blending keeps action inputs when trajectory aligns. | n/a |
| 2026-04-08 | **Test data: Tutorial recordings** — Added `testdata/Tutorial.gho` (ghost, 287.86s) and `testdata/Tutorial.demo` (demo, completes map) as reference recordings for the Tutorial map. | n/a |
| 2026-04-08 | **Format conversion docs + teehistorian multi-CID** — Documented teehistorian's server-side multi-player nature: all CIDs recorded simultaneously, `Loader` selects one CID for reading. Added `CIDs()` method to enumerate all client IDs with input data. Added conversion matrix (Ghost↔Demo↔Teehistorian) and loss documentation. Verified: Ghost→Teehistorian (7197 frames, 101KB), Ghost→Demo (7197 frames, 105KB), Demo→Teehistorian (2782 frames, 39KB). | https://ddnet.org/libtw2-doc/teehistorian/ |
