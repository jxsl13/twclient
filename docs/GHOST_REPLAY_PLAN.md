---
doc_title: Ghost Replay Implementation Plan
summary: Active implementation plan for finishing live DDNet ghost replay without duplicating canonical problem or protocol facts.
canonical_for: current replay work plan, next experiments, acceptance criteria
keywords:
  - ghost replay
  - implementation plan
  - next steps
  - experiments
  - acceptance criteria
---

# Ghost Replay Implementation Plan

This document is the active execution plan only.

Do not use it as the canonical source for protocol facts, input semantics, or historical failures.

## When To Read

Read this document when you need:

1. the current implementation goal,
2. the prioritized next experiments,
3. the active acceptance criteria.

## Not For

Do not use this document for:

1. protocol facts,
2. input semantics,
3. the full failed-approach history.

## Read First

1. [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md)
2. [INPUT_AND_REPLAY.md](INPUT_AND_REPLAY.md)
3. [PROTOCOL.md](PROTOCOL.md) only when wire-level detail is needed

## Purpose

Track the shortest path from the current replay implementation to a reliable live Tutorial ghost finish.

## Current Objective

Finish the Tutorial ghost replay reliably against a live DDNet server.

## Facts

1. Ghost parsing is in place.
2. Deterministic fields are mostly understood: direction, aim, hook, fire, weapon.
3. Replay diagnostics now expose actual versus expected velocity and raw ghost metadata.
4. The codebase already has a working integration test loop and start-navigation flow.

## Open Questions

1. How should jump edges be inferred more reliably from position-only evidence?
2. How should insufficient horizontal momentum influence frame advancement?
3. How should cursor state and physical state be kept aligned without oscillation?
4. What is the smallest recovery that restores dynamic state instead of only position?

## Symptoms

The dominant live failure is not primarily wrong direction or hook reconstruction.
It is insufficient momentum before critical jumps and platform transitions.

Representative stalled regions observed so far:

1. around frame 79,
2. around frame 187,
3. around frame 263.

See [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md) for the measured details and failed attempts.

## Active Workstreams

### 1. Jump Reconstruction

Goal: convert ghost trajectory changes into correct jump pulses with explicit release timing.

Focus:

1. edge timing relative to position deltas,
2. separation of ground-jump and air-jump signatures,
3. interaction with frame gating.

### 2. Start Approach Reconstruction

Goal: cross the race start line with the same dynamic context as the ghost.

Focus:

1. replay the early reconstructed ghost inputs instead of synthetic approximations,
2. preserve horizontal speed into frame 0,
3. avoid direct navigation that destroys run-up state.

### 3. Frame Cursor Control

Goal: keep the ghost frame cursor aligned with what the tee has actually reached.

Focus:

1. advance only when the tee has physically reached the segment,
2. detect dynamic lag, not just positional lag,
3. avoid oscillatory rewinds.

### 4. Momentum-Aware Recovery

Goal: recover from drift without skipping hidden state that later obstacles depend on.

Focus:

1. prefer local, state-preserving correction,
2. avoid long-range future jumps,
3. treat position-only sync as insufficient by default.

### 5. Evaluation Discipline

Goal: make replay iteration cheap and non-redundant.

Focus:

1. run short targeted probes first,
2. inspect velocity mismatch before inventing new heuristics,
3. log every failed approach in [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md).

## Experiment Loop

For each replay change:

1. State the exact hypothesis.
2. Make the smallest viable code change.
3. Run a targeted short integration probe.
4. Compare `velIst` versus `velSoll` at the affected region.
5. If the idea fails, append a timestamped log entry to [GHOST_REPLAY_PROBLEM.md](GHOST_REPLAY_PROBLEM.md).

## Acceptance Criteria

The replay work is only done when all of the following hold:

1. The Tutorial ghost finishes on a live DDNet server reliably, not just once.
2. The finish time on the live server is within a tight margin of the ghost's recorded time — the goal is a replay, not a re-walk.
3. The start approach reproduces usable momentum.
4. Critical jump regions no longer stall because of missing horizontal speed.
5. Recovery no longer loops on position-only pseudo-fixes.
6. The knowledge base is updated with any meaningful new constraint or failure.

Criterion 2 is the sharpest acceptance gate: a run that finishes but is significantly slower than the ghost time is not a successful replay.

## Next Experiments

These are the current best next bets, in order:

1. tighten jump-edge reconstruction around known momentum-sensitive regions,
2. base frame advancement partly on expected versus actual velocity mismatch,
3. replace position-only local rewinds with state-aware bounded replay rewinds,
4. evaluate whether a learned residual model is useful only after the deterministic baseline stabilizes.
