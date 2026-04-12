# Ghost Replay: Input Derivation & Trajectory Following — Implementation Plan

## Problem Statement

DDNet ghost files store **character state** (position, velocity, direction, hook state, etc.), NOT player inputs. Our replay system must reverse-engineer inputs from these state snapshots and send them to a live server, which runs its own physics simulation. Due to network latency and quantization, the server's simulated position will inevitably drift from the ghost's recorded path.

**Core challenge**: This is a **trajectory-following control problem** where:
- The **setpoint trajectory** is the ghost's recorded position sequence
- The **plant** is the DDNet physics engine running on a remote server
- The **actuators** are discrete player inputs (Direction ∈ {-1,0,1}, Jump ∈ {0,1}, Hook ∈ {0,1})
- The **sensor** is the server-reported character position (from snapshots)
- There is a **transport delay** of 1-3 ticks between sending input and observing the result

## Source Analysis

### Ghost Data Per Frame (`CGhostCharacter`)
| Field | Type | Notes |
|-------|------|-------|
| X, Y | int32 | World position |
| VelX | int32 | Horizontal velocity from snapshot (`m_VelX`) |
| VelY | int32 | **Always 0** in DDNet recordings |
| Angle | int32 | `atan2(TargetY, TargetX) * 256` |
| Direction | int32 | -1/0/+1 — **this IS the original `m_Input.m_Direction`** |
| Weapon | int32 | 0-indexed weapon ID |
| HookState | int32 | -1=retracted, 0=idle, 1-3=retracting, 4=flying, 5=grabbed |
| HookX, HookY | int32 | Hook target world position |
| AttackTick | int32 | Server tick when last attack started |
| Tick | int32 | Server tick of this frame |

### Server Input (`CNetObj_PlayerInput`)
| Field | What we need to derive |
|-------|----------------------|
| Direction | Direct from ghost `Direction` field |
| TargetX, TargetY | From `Angle` or `HookX-X, HookY-Y` when hooking |
| Jump | **Must be inferred** from position transitions (VelY is always 0) |
| Hook | From HookState transitions |
| Fire | From AttackTick changes |
| WantedWeapon | From Weapon + 1 |
| PlayerFlags | Always `PLAYERFLAG_PLAYING` |

### DDNet Physics Constants (from `tuning.h`)
```
Gravity             = 0.5       (per tick, applied to VelY)
GroundControlSpeed  = 10.0      (max horizontal speed on ground)
GroundControlAccel  = 2.0       (= 100/50, per tick)
GroundFriction      = 0.5       (multiplier when Direction=0)
AirControlSpeed     = 5.0       (= 250/50, max horizontal speed in air)
AirControlAccel     = 1.5       (per tick)
AirFriction         = 0.95      (multiplier when Direction=0)
GroundJumpImpulse   = 13.2      (VelY = -13.2)
AirJumpImpulse      = 12.0      (VelY = -12.0)
HookLength          = 380.0
HookFireSpeed       = 80.0
HookDragAccel       = 3.0
HookDragSpeed       = 15.0
TeeRadius           = 14.0      (PhysicalSize/2 = 28/2)
SERVER_TICK_SPEED   = 50        (50 ticks per second = 20ms per tick)
```

### Physics Tick Order (from `CCharacterCore::Tick`)
```
1. Apply gravity:      VelY += 0.5
2. Determine grounded: collision check below feet
3. Select accel/friction/maxspeed based on grounded
4. Process Direction:   m_Direction = m_Input.m_Direction
5. Process Jump:        edge-detect on m_Input.m_Jump, apply impulse
6. Process Hook:        start/retract/drag based on m_Input.m_Hook
7. Apply direction:     VelX += Accel * Direction (SaturatedAdd)
8. Apply friction:      VelX *= Friction (when Direction=0)
9. Hook drag:           modify Vel based on hook position
10. TickDeferred:       player collision
11. Move:               MoveBox with velocity ramp, wall collision
12. Quantize:           round to int, write back
```

## Architecture: Feed-Forward + PD Feedback Controller

This is the standard approach in robotics for trajectory following (see: Craig, "Introduction to Robotics", Ch. 12; Siciliano et al., "Robotics: Modelling, Planning and Control", Ch. 11).

```
                    ┌──────────────────────────────────────────────┐
                    │          Ghost Recording                      │
                    │   frame[0], frame[1], ..., frame[N]          │
                    └──────────────┬───────────────────────────────┘
                                   │
                    ┌──────────────▼───────────────────────────────┐
                    │      Feed-Forward Input Derivation            │
                    │  (open-loop: derive "ideal" inputs)          │
                    │                                               │
                    │  Direction = ghost.Direction                  │
                    │  Jump      = detect from Y transitions       │
                    │  Hook      = detect from HookState           │
                    │  Fire      = detect from AttackTick          │
                    │  Aim       = from Angle or Hook target       │
                    └──────────────┬───────────────────────────────┘
                                   │ u_ff (feed-forward input)
                                   │
                    ┌──────────────▼───────────────────────────────┐
                    │         PD Feedback Corrector                 │
                    │  (closed-loop: correct for drift)            │
                    │                                               │
                    │  e(t) = ghost_pos[t] - actual_pos[t]         │
                    │  Only modifies Direction (never Jump/Hook)   │
                    │  Deadband: |e| < TeeRadius → no correction  │
                    │  Correction: override Direction toward ghost │
                    └──────────────┬───────────────────────────────┘
                                   │ u_final (corrected input)
                                   │
                    ┌──────────────▼───────────────────────────────┐
                    │           DDNet Server                        │
                    │  (physics simulation, 50 Hz)                 │
                    └──────────────┬───────────────────────────────┘
                                   │ y(t) = actual position
                                   │ (from server snapshot)
                                   └──── feedback to PD corrector
```

### Why Feed-Forward + PD (not PID)?

1. **No integral term**: The discrete nature of inputs (Direction is -1/0/1, not continuous) means integral windup would cause oscillation. A deadband handles steady-state error.
2. **Feed-forward dominates**: Unlike a blind PID, we KNOW the desired trajectory and can derive most inputs directly. The PD corrector only handles residual drift.
3. **Only X-axis correction**: Direction only affects horizontal movement. Vertical movement is controlled by Jump (binary, timing-critical) and gravity (fixed). Correcting Y by adding/removing jumps would be catastrophic.

## Implementation Plan

### Phase 1: Precise Input Derivation (Feed-Forward)

Replace the current heuristic approach with physics-model-based input inference.

#### 1.1 Direction (trivial — already correct)
```go
input.Direction = packet.Direction(ghost[t].Direction)
```
The ghost's Direction field IS the original input direction. No derivation needed.

#### 1.2 Jump Detection (from position deltas)

**Problem**: VelY is always 0 in ghost files. We must infer jumps from Y-position transitions.

**Physics model for jump detection**:
- At tick t, the server applies: `VelY[t] = VelY[t-1] + Gravity` (before jump check)
- If jump: `VelY[t] = -GroundJumpImpulse` (= -13.2) or `-AirJumpImpulse` (= -12.0)
- Then: `Y[t+1] ≈ Y[t] + VelY[t]` (simplified, before collision)

Since we don't have VelY, approximate it from positions:
```
approxVelY[t] = Y[t] - Y[t-1]     // units per tick
```

**Ground jump signature**: `approxVelY` suddenly becomes very negative (≈ -13)
```
approxVelY[t-1] ≈ 0  (standing on ground)
approxVelY[t]   ≈ -13 (jumped)
```

**Air jump signature**: `approxVelY` was positive (falling) or slightly negative, suddenly becomes very negative
```
approxVelY[t-1] > -8   (falling or slow rise)
approxVelY[t]   ≈ -12  (air jumped)
```

**Algorithm**:
```go
const (
    GroundJumpThreshold = -10.0  // Y delta that indicates ground jump
    AirJumpThreshold    = -8.0   // Y delta change that indicates air jump
)

func detectJump(prev, cur, next CharacterFrame) bool {
    if prev.Tick == 0 { return false }
    
    dy := cur.Y - prev.Y  // positive = downward
    
    // Strong upward movement indicates jump
    // Ground jump: dy ≈ -13, Air jump: dy ≈ -12
    if dy < GroundJumpThreshold {
        return true
    }
    
    // If there's a next frame, check for sudden velocity reversal
    // (velocity was going down, now going up sharply)
    if next.Tick != 0 {
        dyPrev := cur.Y - prev.Y
        dyNext := next.Y - cur.Y
        deltaVelY := dyNext - dyPrev
        if deltaVelY < AirJumpThreshold {
            return true // acceleration changed sharply — air jump
        }
    }
    
    return false
}
```

**Critical: Jump input must be sent ONE TICK BEFORE the expected position change**. The server processes input at tick T, which affects position at tick T+1 (after Move()). Therefore, shift detected jumps back by 1 frame.

#### 1.3 Jump Edge Detection (preventing held-jump)

The server uses edge detection for jumps:
```cpp
if(m_Input.m_Jump) {
    if(!(m_Jumped & 1)) {
        // trigger jump
        m_Jumped |= 1;
    }
}
else {
    m_Jumped &= ~1;   // release allows next jump
}
```

This means:
- Jump=1 for one tick triggers a ground jump
- Jump must return to 0 before an air jump can trigger
- Holding Jump=1 does NOT trigger additional jumps

**Algorithm for edge conversion**:
```go
// After detecting all jump frames, convert to edge-triggered pulses:
// For each jump event:
//   frame[t].Jump = 1   (trigger)
//   frame[t+1].Jump = 0 (release)
//   ... minimum 1 tick of Jump=0 between jumps
//
// For double-jumps (ground + air), ensure:
//   frame[t].Jump = 1   (ground jump)
//   frame[t+1].Jump = 0 (release)
//   frame[t+k].Jump = 1 (air jump, k >= 2)
//   frame[t+k+1].Jump = 0 (release)
```

#### 1.4 Hook (from HookState transitions)

```go
func deriveHook(prev, cur CharacterFrame) (hook packet.HookState, aimX, aimY int) {
    // Default aim from angle
    rads := float64(cur.Angle) / 256.0
    aimX = int(math.Round(math.Cos(rads) * 256))
    aimY = int(math.Round(math.Sin(rads) * 256))
    
    switch {
    case cur.HookState == 0 || cur.HookState == -1:
        // IDLE or RETRACTED — hook not pressed
        hook = packet.HookOff
        
    case cur.HookState == 1:
        // FLYING — hook was just launched or is extending
        hook = packet.HookOn
        // Override aim toward hook position for accuracy
        aimX = cur.HookX - cur.X
        aimY = cur.HookY - cur.Y
        
    case cur.HookState == 3:
        // GRABBED — hook is attached, keep holding
        hook = packet.HookOn
        aimX = cur.HookX - cur.X
        aimY = cur.HookY - cur.Y
        
    case cur.HookState >= 4:
        // RETRACTING — server is pulling hook back
        // Hook input should be OFF (release causes retract)
        // But also could be: hook hit NOHOOK tile
        // Check if previous state was FLYING or GRABBED
        if prev.HookState == 1 || prev.HookState == 3 {
            hook = packet.HookOff // just released
        } else {
            hook = packet.HookOff // still retracting
        }
    }
    
    return
}
```

**Hook launch timing**: The hook must be pressed ONE tick before HookState transitions from IDLE to FLYING in the ghost. The server processes Hook input at tick T, and the state change appears at tick T+1.

#### 1.5 Fire (from AttackTick changes)

```go
func deriveFire(prev, cur CharacterFrame, prevFireCount int) (fire packet.FireCount) {
    if prev.AttackTick != cur.AttackTick {
        // New attack detected — increment fire counter, set held bit
        return packet.FireCount((prevFireCount + 1) | 1)
    }
    if prevFireCount & 1 != 0 {
        // Previous was held — release
        return packet.FireCount((prevFireCount + 1) &^ 1)
    }
    return packet.FireCount(prevFireCount)
}
```

#### 1.6 Weapon

```go
input.WantedWeapon = packet.Weapon(cur.Weapon + 1) // 0-indexed → 1-indexed
```

### Phase 2: Frame Synchronization (Time-Based)

**Critical insight**: The ghost cursor must advance based on **server tick count**, not position proximity. The ghost was recorded at 50 Hz (every server tick). Our client also sends inputs at ~50 Hz.

```go
type FrameCursor struct {
    startServerTick int  // server tick when race started
    frames         []GhostFrame
}

func (c *FrameCursor) FrameForTick(serverTick int) int {
    elapsed := serverTick - c.startServerTick
    if elapsed < 0 { return 0 }
    if elapsed >= len(c.frames) { return len(c.frames) - 1 }
    return elapsed
}
```

**Why time-based, not position-based?**
- Position-based gating creates feedback loops: if the tee is slow, the cursor stalls, causing the tee to receive "stay here" inputs, making it even slower
- Time-based ensures the ghost cursor always advances, and the PD controller handles catching up
- The ghost was recorded 1 frame per tick, so frame index = elapsed ticks

### Phase 3: PD Feedback Controller (X-Axis Only)

The controller modifies only the **Direction** component of the feed-forward input. It never touches Jump, Hook, or Fire.

```go
type PDController struct {
    Kp       float64 // proportional gain
    Kd       float64 // derivative gain  
    deadband float64 // error below this → no correction
    prevErr  float64 // for derivative term
}

func NewPDController() *PDController {
    return &PDController{
        Kp:       0.05,    // conservative — discrete output limits correction
        Kd:       0.02,    // damping to prevent oscillation
        deadband: 14.0,    // TeeRadius — within this, no correction
    }
}

func (pd *PDController) Correct(
    ffDirection packet.Direction, // feed-forward direction from ghost
    expectedX int,                // ghost X at this frame
    actualX int,                  // server-reported X
) packet.Direction {
    errX := float64(expectedX - actualX) // positive = tee is left of target
    
    // Deadband: within TeeRadius, trust the feed-forward
    if math.Abs(errX) <= pd.deadband {
        pd.prevErr = errX
        return ffDirection
    }
    
    // PD output
    dErr := errX - pd.prevErr
    pd.prevErr = errX
    output := pd.Kp*errX + pd.Kd*dErr
    
    // Map continuous output to discrete direction
    switch {
    case output > 1.0:
        return packet.DirRight // need to move right
    case output < -1.0:
        return packet.DirLeft  // need to move left
    default:
        return ffDirection     // within tolerance, use ghost direction
    }
}
```

**Key rules for the PD corrector**:
1. **Never override a non-zero feed-forward direction with the opposite**: If the ghost says "go left" and the corrector says "go right", something is very wrong (probably jump sequence). Trust the ghost.
2. **Never modify Jump**: Jump timing is frame-critical. A wrong jump ruins the entire trajectory.
3. **Never modify Hook**: Hook direction and timing are critical for swings.
4. **Only fill in when feed-forward says Direction=0**: The safest correction.
5. **Large drift (> 5 tiles) = desync, stop correcting**: If drift exceeds 160 units, the replay is broken and correction would make things worse.

```go
func safeMerge(ff, pd packet.Direction) packet.Direction {
    if ff != 0 {
        // Ghost has an explicit direction — NEVER contradict it
        return ff
    }
    // Ghost says idle — PD can nudge
    return pd
}
```

### Phase 4: Recovery & Desync Detection

When drift exceeds a threshold, the trajectory is unrecoverable using only Direction correction. Options:

1. **Soft resync** (drift 2-5 tiles): Speed up/slow down by modifying Direction for 5-10 ticks
2. **Hard resync** (drift > 5 tiles): Attempt to reconnect to the ghost path at a future frame. Scan ahead for a frame where the current actual position is close to the expected position. Reset the frame cursor.
3. **Abort** (drift > 10 tiles): Log the failure and continue with best-effort playback.

```go
const (
    SoftResyncThresholdTiles = 2   // 64 units
    HardResyncThresholdTiles = 5   // 160 units
    AbortThresholdTiles      = 10  // 320 units
)
```

### Phase 5: Pre-processing Pipeline

At construction time, process all ghost frames into a pre-computed input sequence:

```go
func PreprocessGhost(frames []CharacterFrame) []InputFrame {
    inputs := make([]InputFrame, len(frames))
    
    // Pass 1: Derive raw inputs from state transitions
    for i, cur := range frames {
        var prev CharacterFrame
        if i > 0 { prev = frames[i-1] }
        
        inputs[i].Direction = packet.Direction(cur.Direction)
        inputs[i].TargetX, inputs[i].TargetY = deriveAim(cur)
        inputs[i].Hook, hookAimX, hookAimY = deriveHook(prev, cur)
        if inputs[i].Hook == packet.HookOn {
            inputs[i].TargetX = hookAimX
            inputs[i].TargetY = hookAimY
        }
        inputs[i].WantedWeapon = packet.Weapon(cur.Weapon + 1)
        inputs[i].ExpectedX = cur.X
        inputs[i].ExpectedY = cur.Y
    }
    
    // Pass 2: Detect jumps from position deltas
    for i := 1; i < len(frames); i++ {
        dy := frames[i].Y - frames[i-1].Y
        if dy < -10 {
            inputs[i].HasJump = true
        }
        // Also check acceleration (2nd derivative)
        if i >= 2 {
            vel1 := frames[i-1].Y - frames[i-2].Y
            vel2 := frames[i].Y - frames[i-1].Y
            accel := vel2 - vel1
            if accel < -8 {
                inputs[i].HasJump = true
            }
        }
    }
    
    // Pass 3: Shift jumps back by 1 tick (input → effect delay)
    for i := len(inputs)-1; i >= 1; i-- {
        if inputs[i].HasJump {
            inputs[i].HasJump = false
            inputs[i-1].HasJump = true
        }
    }
    
    // Pass 4: Convert jumps to edge-triggered pulses
    jumpHeld := false
    for i := range inputs {
        if inputs[i].HasJump && !jumpHeld {
            inputs[i].Jump = packet.JumpOn
            jumpHeld = true
        } else if inputs[i].HasJump && jumpHeld {
            // Already holding — this might be an air jump
            // Insert a release frame: set current to Off, next to On
            inputs[i].Jump = packet.JumpOff
            jumpHeld = false
            // The air jump will be detected in the next iteration
        } else {
            inputs[i].Jump = packet.JumpOff
            jumpHeld = false
        }
    }
    
    // Pass 5: Shift hook presses back by 1 tick
    for i := len(inputs)-1; i >= 1; i-- {
        if inputs[i].Hook == packet.HookOn && 
           (i == 0 || inputs[i-1].Hook == packet.HookOff) {
            // Hook launch: shift back
            inputs[i-1].Hook = packet.HookOn
            inputs[i-1].TargetX = inputs[i].TargetX
            inputs[i-1].TargetY = inputs[i].TargetY
        }
    }
    
    // Pass 6: Fire counter
    fireCounter := 0
    for i := range inputs {
        if i > 0 && frames[i].AttackTick != frames[i-1].AttackTick {
            fireCounter = (fireCounter + 1) | 1
        } else if fireCounter & 1 != 0 {
            fireCounter = (fireCounter + 1) &^ 1
        }
        inputs[i].Fire = packet.FireCount(fireCounter)
    }
    
    return inputs
}
```

### Phase 6: Runtime Replay Loop

```go
func (r *Replayer) OnTick(serverTick int, actualX, actualY int) packet.PlayerInput {
    // 1. Determine frame index from elapsed time
    frameIdx := r.cursor.FrameForTick(serverTick)
    
    // 2. Get feed-forward input
    ff := r.inputs[frameIdx]
    input := ff.ToPlayerInput()
    
    // 3. Check drift
    errX := float64(ff.ExpectedX - actualX)
    errY := float64(ff.ExpectedY - actualY)
    drift := math.Sqrt(errX*errX + errY*errY)
    
    if drift > AbortThresholdTiles * 32 {
        r.logger.Warn("desync too large, aborting correction", 
            "drift", drift, "frame", frameIdx)
        return input
    }
    
    // 4. Apply PD correction (X-axis only, Direction only)
    pdDir := r.pd.Correct(input.Direction, ff.ExpectedX, actualX)
    input.Direction = safeMerge(input.Direction, pdDir)
    
    return input
}
```

## Key Differences from Previous Approach

| Aspect | Previous (broken) | New |
|--------|-------------------|-----|
| Frame advancement | Position-gated (stalls on drift) | Time-based (always advances) |
| Jump detection | Simple Y-delta threshold | 2nd derivative (acceleration), with 1-tick shift-back |
| Jump triggering | Held for multiple frames | Edge-triggered pulses with mandatory release |
| Direction correction | Complex multi-strategy | Simple PD with deadband, ONLY when ff=0 |
| Hook timing | Reactive to current state | Pre-shifted 1 tick back at processing time |
| Overall architecture | Tangled reactive logic | Clean separation: preprocessing → feed-forward → feedback |
| Correction scope | Modifies all inputs | ONLY modifies Direction (never Jump/Hook/Fire) |

## Testing Strategy

1. **Unit test**: Pre-process a known ghost, verify derived inputs match expected
2. **Simulation test**: Run derived inputs through a local physics simulation (if available), compare resulting positions with ghost positions
3. **Integration test**: Run against live DDNet server, measure position drift over time
4. **Metrics**: Track max drift, mean drift, finish success rate, time-to-finish delta

## Implementation Order

1. Implement `PreprocessGhost()` — the 6-pass pipeline
2. Implement `FrameCursor` — pure time-based
3. Implement `PDController` — with deadband and safe-merge
4. Wire into `Replayer.OnTick()`
5. Remove all old correction logic (barrier detection, free-run, recovery loops, etc.)
6. Test against `TestReplayGhost`

## References

- **DDNet source**: `src/game/gamecore.cpp` (CCharacterCore::Tick — physics), `src/game/client/components/ghost.cpp` (ghost rendering, NOT replay)
- **DDNet tuning**: `src/game/tuning.h` (all physics constants)
- **DDNet ghost format**: `CGhostCharacter` in `ghost.h` (11 int32 fields + tick)
- **Control theory**: PD controller is the standard for discrete-output trajectory following (Craig, "Introduction to Robotics", 4th ed., Ch. 12)
- **Feed-forward + feedback**: Standard in robotics for known trajectory following (Siciliano et al., "Robotics: Modelling, Planning and Control", Ch. 11)
- **Why not PID**: Integral term causes windup with discrete (ternary) outputs; deadband eliminates steady-state error concern
