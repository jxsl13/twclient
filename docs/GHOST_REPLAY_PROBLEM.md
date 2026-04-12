---
doc_title: Ghost Replay Problem Description And Knowledge Base
summary: Canonical ghost replay knowledge base with constraints, measured failure modes, open questions, and timestamped failed approaches.
canonical_for: ghost replay problem framing, constraints, experiment history, failed approaches
keywords:
  - ghost replay
  - input derivation
  - momentum
  - recovery
  - jump inference
  - knowledge base
  - failed approaches
---

# Ghost Replay Problem Description And Knowledge Base

## When To Read

Read this document when you need:

1. the canonical replay problem framing,
2. the measured failure symptoms,
3. the history of failed approaches,
4. the current open questions before starting another experiment.

## Not For

Do not use this document for:

1. wire-level protocol facts,
2. full player input semantics,
3. the active execution plan.

Use [PROTOCOL.md](PROTOCOL.md) for protocol facts, [INPUT_AND_REPLAY.md](INPUT_AND_REPLAY.md) for input semantics, and [GHOST_REPLAY_PLAN.md](GHOST_REPLAY_PLAN.md) for the current plan.

## Maintenance Rules

This file is the canonical iterative knowledge base for ghost replay work.

Before starting another ghost replay implementation attempt:

1. Read this document fully.
2. Check the failed-approach log.
3. Do not retry a previously failed approach unchanged.
4. If an old idea is retried with a materially different mechanism, state that difference explicitly in the new experiment notes.

For every future failed or partially failed approach, append a new entry with an ISO 8601 timestamp including timezone.

Each failed-approach entry must include:

1. timestamp,
2. short approach name,
3. hypothesis,
4. exact strategy,
5. observed outcome,
6. why it failed,
7. redundancy guard.

## Facts

### Problem Definition

Ghost replay is not normal playback.
A DDNet ghost stores sampled character snapshot state, not a ready-to-send stream of raw client inputs.
The replay system must reconstruct valid client inputs and send them to a live, authoritative DDNet server that runs its own physics simulation.

In practice this is a constrained inverse-physics and trajectory-tracking problem.

### Ghost File Facts

The relevant per-frame ghost data is:

| Field | Meaning | Replay value |
| --- | --- | --- |
| `X`, `Y` | world position | primary trajectory target |
| `VelX` | horizontal snapshot velocity | consistency signal only |
| `VelY` | always written as `0` in DDNet ghost recording | not useful for vertical reconstruction |
| `Angle` | aim angle | directly useful for aim reconstruction |
| `Direction` | snapshot direction from live input | directly useful for movement reconstruction |
| `Weapon` | active weapon | useful for weapon reconstruction |
| `HookState` | server hook state | useful for hook reconstruction |
| `HookX`, `HookY` | hook position | useful for hook aim reconstruction |
| `AttackTick` | last attack start tick | useful for fire-edge reconstruction |
| `Tick` | server tick of the frame | required for timing alignment |

Source-derived invariants:

1. `Direction` is copied directly from live input state.
2. `Angle`, `HookState`, `HookX`, `HookY`, and `AttackTick` are copied directly from snapshots.
3. `VelY` is intentionally not preserved and is therefore unusable for direct jump reconstruction.
4. `VelX` is preserved but does not uniquely determine the original input sequence.

### Reconstructed Input Facts

The live server expects `CNetObj_PlayerInput`, not ghost frames.

| Input field | Reconstruction status | Notes |
| --- | --- | --- |
| `Direction` | mostly deterministic | from ghost `Direction` |
| `TargetX`, `TargetY` | mostly deterministic | from `Angle`, or hook target while hooking |
| `Hook` | largely deterministic | from `HookState` and hook progression |
| `Fire` | largely deterministic | from `AttackTick` changes and fire parity |
| `WantedWeapon` | mostly deterministic | from snapshot weapon with DDNet caveats |
| `Jump` | inferred | not directly observable from ghost data |

### Hard Constraints

1. Jump is edge-triggered on the server.
2. Holding jump does not create repeated jumps.
3. Releasing jump between jump edges is mandatory.
4. The first replay frame is already inside the timed run.
5. The replay runs against a remote authoritative server with delay and quantization.
6. Position-only recovery is often insufficient because later obstacles depend on hidden dynamic state.

### Start And Tracking Facts

The replay has two separate control problems:

1. recreate the pre-start approach into frame 0,
2. track the ghost trajectory after the race has already begun.

This means correctness depends on both:

1. input reconstruction,
2. dynamic-state preservation under live-server drift.

## Symptoms

### Primary Failure Signature

The dominant failure is that the tee is too slow.

The tee can be locally near the ghost path while still being functionally wrong because it reaches the next obstacle with insufficient horizontal momentum or the wrong jump timing.

### Measured Velocity Mismatch

Representative live observations:

1. around frame 79: expected speed about `11.2`, actual average speed about `1.6`, then stall,
2. around frame 187: expected speed about `12.5`, actual average speed about `1.1`,
3. around frame 263: expected speed about `10.1`, actual average speed about `1.4`, immediately before a jump-sensitive region.

### Critical Region Around Frame 263

Ghost behavior around frames 255 to 265:

1. steady rightward movement,
2. low point of the trajectory,
3. jump pulse at frame 263,
4. immediate conversion into a faster upward arc.

Live replay behavior in the same region:

1. the tee is still too far left or on the wrong local platform,
2. the frame cursor reaches the jump frame,
3. the replay emits the jump pulse,
4. the tee lacks the required horizontal momentum,
5. later recovery destroys the original run-up assumptions instead of restoring them.

The most precise description of the failure is:

The replay emits a locally plausible jump input at a globally wrong dynamic state.

### Why Recovery Commonly Fails

Naive recovery to a future ghost position often fails because the future state assumes hidden context:

1. current horizontal velocity,
2. current vertical velocity,
3. jump press or release history,
4. hook state,
5. immediate collision history.

So position similarity alone is not enough.

## Failed Approaches

The entries below are intentionally blunt so they are searchable and not repeated.

### 2026-04-12T00:10:00+02:00 — Synthetic Start Run-Through From Coarse Approach Velocity

- Hypothesis: a simple synthetic run-through based on average approach velocity and approach type could recreate the race start.
- Exact strategy: `runThrough` used synthetic direction, aim, and coarse jump or hook behavior instead of replaying reconstructed early ghost inputs.
- Observed outcome: poor start alignment and wrong entry momentum.
- Why it failed: the start crossing depends on the exact opening input sequence, not just a coarse approach vector.
- Redundancy guard: do not use a purely velocity-derived synthetic run-through again unless it is explicitly combined with recorded opening inputs.

### 2026-04-12T00:20:00+02:00 — Large-Window Same-Level Recovery Target Selection

- Hypothesis: recovery should pick the nearest future grounded ghost point with a strong same-Y preference.
- Exact strategy: recovery searched far ahead and sorted candidates with a hard same-level bias.
- Observed outcome: recovery jumped to absurdly distant targets while the real problem was still local.
- Why it failed: same-level preference dominated locality and destroyed contextual continuity.
- Redundancy guard: do not reintroduce long-range same-level-biased recovery search.

### 2026-04-12T00:35:00+02:00 — Aggressive Always-On Frame Rewind

- Hypothesis: opportunistic rewinding to better-matching earlier frames would resynchronize playback.
- Exact strategy: the replay loop rewound during normal playback, not just during true stalls.
- Observed outcome: cursor oscillation and replay loops.
- Why it failed: position-only rewind without physical state restoration rewound the cursor much faster than the tee could actually recover.
- Redundancy guard: do not enable always-on opportunistic rewinds during forward playback.

### 2026-04-12T01:00:00+02:00 — Tight Frame Gating Without Momentum Rebuild

- Hypothesis: much tighter reverse and stationary frame gates would stop bad cursor advances.
- Exact strategy: reverse and stationary segments were gated much more aggressively.
- Observed outcome: some early alignment improved, but later frame stalls became longer, especially near frame 263.
- Why it failed: stricter gating prevented some bad advances but did not rebuild the missing dynamic state.
- Redundancy guard: do not assume tighter cursor gating alone can solve momentum-sensitive stalls.

### 2026-04-12T01:20:00+02:00 — Position-Only Recovery And Momentum Rewind Without State Reconstruction

- Hypothesis: bounded local rewinds plus short local syncs to earlier ghost positions would rebuild enough speed to continue.
- Exact strategy: on frame stalls, the replay rewound to an earlier frame with similar X and optionally navigated to that earlier ghost position.
- Observed outcome: some rewinds triggered, but the tee still stayed too slow, repeated local loops, and could get trapped on earlier flat sections.
- Why it failed: matching position and rough X locality still did not reconstruct the full dynamic state, especially velocity history and jump-edge timing.
- Redundancy guard: do not treat local X-matching or short physical sync alone as sufficient momentum reconstruction.

## Open Questions

1. What is the most reliable jump-edge inference rule for ground jumps and air jumps from position-only evidence?
2. Which dynamic-state signals should gate frame advancement beyond simple position checks?
3. How can recovery rebuild momentum without corrupting hidden state such as jump-release history?
4. What is the smallest state-aware rewind that can be applied without causing cursor oscillation?
5. At what point does a learned residual model become useful after the deterministic baseline is stable?

## Next Experiments

These are the most promising next categories of work.
Implementation ordering lives in [GHOST_REPLAY_PLAN.md](GHOST_REPLAY_PLAN.md).

1. improve jump-edge reconstruction in momentum-sensitive regions,
2. use expected-versus-actual velocity mismatch more directly in frame advancement,
3. replace position-only local rewinds with more state-aware bounded rewinds,
4. evaluate learned residual correction only after deterministic replay becomes stable.

## Current State

The repository already has:

1. ghost parsing in [replay/replay.go](../replay/replay.go), [replay/ghost](../replay/ghost), and [replay/replayer.go](../replay/replayer.go),
2. start navigation in [replay/navigate.go](../replay/navigate.go),
3. a live replay loop in [replay/replay_integration_test.go](../replay/replay_integration_test.go),
4. diagnostics for expected versus actual velocity and raw ghost metadata.

The open issue is no longer basic file parsing.
The open issue is preserving and reconstructing the dynamic state required for later trajectory segments.

## Acceptance Target

A successful solution must do all of the following:

1. derive deterministic input fields directly from ghost snapshots where possible,
2. infer jump edges with correct release timing,
3. recreate the start approach with usable momentum,
4. compare actual and expected velocity, not just position,
5. avoid advancing into segments the tee has not physically reached,
6. use recovery only when it restores both position and dynamic state,
7. finish the live Tutorial ghost replay reliably,
8. finish within a tight margin of the ghost's recorded time.

Criterion 8 is the sharpest gate: a run that completes the map but takes significantly longer than the ghost time is not a successful replay. The word "replay" means tick-level correspondence, not approximate path-following. Every meaningful deviation from ghost timing is a failure to be diagnosed and fixed.
