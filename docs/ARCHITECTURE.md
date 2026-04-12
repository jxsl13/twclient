---
doc_title: Architecture
summary: High-level package map and dependency guide for twclient.
canonical_for: package responsibilities, dependency direction, system overview
keywords:
  - architecture
  - package map
  - dependencies
  - client
  - replay
  - protocol layers
---

# Architecture

Use this document for package boundaries and code navigation, not for protocol facts or replay heuristics.

## When To Read

Read this document when you need:

1. the package map,
2. dependency direction,
3. the right package to inspect next.

## Not For

Do not use this document for:

1. wire-level packet rules,
2. input semantics,
3. ghost replay failure analysis.

This document describes the high-level architecture of the twclient project.
If you want to familiarize yourself with the codebase, start here.
Use symbol search to find the types and functions mentioned — names are chosen to be grep-friendly.

## Bird's Eye View

twclient (`github.com/jxsl13/twclient`) is a headless Teeworlds/DDNet client library in Go, with an ML training bot on top.
It implements the Teeworlds 0.6 (DDNet variant) and 0.7 network protocols from scratch:
parsing binary packet headers, chunk frames, varint-encoded messages, delta-compressed snapshots,
and the full connection handshake including DDNet's TKEN security token extension.

The primary consumers are:
- **racebot** — a neural network agent that learns DDRace maps via reinforcement learning
- **replay** — a tool that replays recorded game sessions (demo, ghost, teehistorian files) against a live server

The canonical protocol specification lives in [PROTOCOL.md](PROTOCOL.md).

## Code Map

Dependency direction flows strictly downward in this diagram:

```
cmd/racebot/  cmd/replay/
     │              │
     ▼              ▼
  cmd/ml/       replay/
     │         ╱       ╲
     ▼        ▼         ▼
   client/  replay/demo/  replay/ghost/  replay/teehistorian/  replay/convert/
     │
     ├──────────┐
     ▼          ▼
   net6/      net7/
     │          │
     ├────┬─────┘
     ▼    ▼
  network/  packer/
     │        │
     ▼        ▼
   packet/
```

### `packet/` — Protocol-agnostic shared types

The foundation package. Everything else depends on it; it depends on nothing internal.

Key types:
- `Token` — 4-byte security token
- `ChunkHeader` — chunk-level packet framing (vital/resend flags, size, sequence number)
- `Snapshot`, `SnapItem`, `SnapStorage` — game state snapshots with delta decompression ring buffer
- `PlayerInput` — input state struct (direction, jump, hook, fire, weapon, aim)
- `Direction`, `JumpState`, `HookState`, `Weapon` — input enums and constructors
- `Event` interface — event types delivered by sessions (`EventSnapshot`, `EventMapChange`, `EventClose`, `EventRaceFinish`, `EventCheckpoint`, `EventRecord`, `EventInputTiming`)
- `MapInfo`, `MapCache` — map metadata and thread-safe download deduplication

Key functions:
- `UnpackChunks()` — split payload into chunk structures
- `CountVitalChunks()`, `ContainsSysMsg()`, `ContainsGameMsg()` — chunk scanning
- `PackMsgID()`, `PackInt()`, `PackStr()` — message packing helpers
- Physics constants (gravity, hook parameters, tee sizes, velocities)
- Coordinate/tile conversion helpers

Architecture invariant: `packet/` never imports other internal packages. All protocol-version-specific logic lives in `net6/` or `net7/`.

### `packer/` — Varint and string packing

Thin wrapper around `github.com/teeworlds-go/varint` with `Unpacker` for reading packed data and `PackInt`/`PackStr`/`PackMsgID` for writing.

Also implements `CalculateUUID()` for DDNet extended message UUIDs (UUID v3).

### `network/` — UDP transport

`Conn` wraps `net.UDPConn` with configurable timeouts and logger. Methods: `Dial()`, `SendRaw()`, `RecvContext()`.

Architecture invariant: `network/` knows nothing about protocol versions. It only moves raw bytes.

### `net6/` — Teeworlds 0.6 protocol (DDNet variant)

Implements the 0.6.4-based protocol with DDNet TKEN security token extension.

Key constants: `Split = 4` (chunk header format), `NetVersion = "0.6 626fce9a778df4d4"`, `DDNetVersion = 19070`.

Key types:
- `Header` — 3-byte packet header (7-byte with 0.6.5 token flag)
- `Flags` — Compression, Resend, Connless, Control, Token
- `Session` — full client session: token exchange, ack tracking, snap assembly, event delivery

Key functions:
- Builders: `BuildConnect()`, `BuildInfoPacket()`, `BuildReadyPacket()`, `BuildEnterGamePacket()`, `BuildStartInfoPacket()`
- Messages: system (`MsgSysInfo`, `MsgSysMapChange`, `MsgSysSnap`, `MsgSysInput`, etc.) and game (`MsgGameSvChat`, `MsgGameClSay`, `MsgGameSvDDRaceTimeLegacy`, etc.)
- Snap item sizes defined in `snap.go` (`ObjPlayerInput`, `ObjCharacter`, `ObjCharacterCore`, etc.)

Architecture invariant: `net6/` does NOT depend on `client/`. The dependency flows the other way.

### `net7/` — Teeworlds 0.7 protocol

Same structure as `net6/` but for the 0.7 protocol.

Key differences: `Split = 6`, 7-byte header (always token-aware), different message IDs, native race messages (`MsgGameSvRaceFinish`, `MsgGameSvCheckpoint`).

### `client/` — Protocol-agnostic client interface

Unified API wrapping `net6.Session` or `net7.Session`.

Key types:
- `Client` — main struct managing lifecycle, login, map download, snap processing
- `Session` interface — protocol-independent session contract that `net6.Session` and `net7.Session` both implement. Methods: `Login()`, `Close()`, `StartReader()`, `EventCh()`, `Poll()`, `SendInput()`, `SendChat()`, `SendKill()`, `DownloadMap()`, `Map()`, `SetMap()`
- `SnapStorage` — extracted game state: `CharacterState` (position, velocity, health, weapon), `GameInfoState`
- `PredictedTime` — predicted game tick tracker advancing at 50 ticks/sec from last server ack
- `RaceTime` — race timer with tick-based and wall-clock tracking

Key methods: `New()`, `Connect()`, `IsConnected()`, `Character()`, `RaceTime()`, `SendInput()`, `SendChat()`, `Close()`

Architecture invariant: `client/` is the API boundary. Bot code and replay code only talk to `client.Client`, never directly to `net6` or `net7` sessions.

### `cmd/ml/` — Neural network reinforcement learning

Actor-critic neural network for DDRace map training with policy gradients.

Key types:
- `Bot` — one training agent: owns a `client.Client`, reads state, decides actions, sends input
- `PolicyNetwork` — Gorgonia-based actor-critic network. Input: tile features (50×40×2 map viewport) + 10 extra scalars. Output: policy (17 discrete actions) + value estimate
- `Coordinator` — manages N parallel bots across M servers with shared network and per-server connection mutex
- `Action` — one of 17 discrete actions (no-op, move, jump, hook, fire, combos, weapon switch). `ActionFromIndex()` converts 0–16 to `Action`, `ToPlayerInput()` converts to `packet.PlayerInput`
- `Window`, `GameRenderer` — Ebiten-based visualization

### `cmd/racebot/` — ML training entry point

`main.go` launches `runSingleBot()` or `runParallelBots()` based on flags (`-server`, `-name`, `-steps`, `-headless`, `-checkpoint`, `-bots`).

### `replay/` — Recording format abstraction

Common interface for replay files yielding per-tick frames.

Key types:
- `InputProvider` interface — yields `InputFrame` (tick + `PlayerInput` + optional expected position)
- `CharacterProvider` interface — yields `CharacterFrame` (tick + position, velocity, weapon, hook, angle)
- `Replayer` — buffers all frames, provides `NextInput()` with optional drift correction, auto-navigates to start position
- `RecordingInfo` — format, map, player name, tick count

### `replay/demo/` — .demo file parser

Parses Teeworlds/DDNet demo files (client-side recordings). Extracts `PlayerInput` from snapshot chunks. Implements `InputProvider`.

### `replay/ghost/` — .gho file parser

Parses DDNet ghost files (character position recordings). More useful than demos because they store character state. Implements `CharacterProvider`. Ghost data is huffman-compressed.

### `replay/teehistorian/` — .teehistorian file parser

Parses server-side teehistorian files. Most valuable format: contains actual raw `InputDiff`/`InputNew` messages at tick granularity. Implements `InputProvider`.

### `replay/convert/` — Format conversion

Bidirectional lossy conversion between demo and teehistorian formats. `ToTeehistorian()` and `ToDemo()` functions.

### `cmd/replay/` — Replay execution entry point

Connects to server, auto-detects recording format, and replays the file. Usage: `replay -server localhost:8303 recording.demo`

## Data Flow

### Network → Client → Bot

```
Server UDP packet
  → network.Conn.RecvContext()           raw bytes
  → net6.Session (background reader)     unpack header, ack, decompress, unpack chunks
  → processMessage()                     decode system/game messages
  → packet.Event on eventCh              EventSnapshot, EventMapChange, EventRaceFinish, ...
  → client.Client event loop             extract CharacterState, GameInfoState, update PredictedTime
  → Bot / Replayer reads state           Character(), RaceTime(), LastSnapTick()
  → compute action                       PolicyNetwork forward pass / replay frame lookup
  → client.SendInput()                   pack PlayerInput, wrap in NETMSG_INPUT, send via UDP
```

### Replay → Client

```
Recording file (.demo / .gho / .teehistorian)
  → format-specific parser               InputProvider or CharacterProvider
  → Replayer                              buffers frames, drift correction
  → NextInput()                           returns PlayerInput for current tick
  → client.SendInput()                    send to server
```

## Cross-Cutting Concerns

### Concurrency

- Each `net6.Session` / `net7.Session` runs a background reader goroutine. Protects `ack`, `sequence` with mutex. Protects `mapInfo`, parsed state with RWMutex.
- `client.Client` runs a background event loop goroutine. Protects snap state with RWMutex. All public accessors are thread-safe.
- `PolicyNetwork` methods are mutex-guarded for concurrent trainer threads.
- `MapCache` is thread-safe with mutex — coordinates map downloads across sessions.
- `Coordinator` uses per-server connection mutex to rate-limit parallel bot startups and prevent thundering herd.

### Tick Rate

50 ticks/sec (20ms per tick). `PredictedTime` advances from the last server-acknowledged tick at this rate.

### Testing

```bash
go build ./...                                                    # build everything
go test ./... -v                                                  # all tests
TW_TARGET=localhost:8303 go test ./client -run TestLogin06 -v     # integration test vs live server
go test ./client -fuzz FuzzPostHandshakeChunks -fuzztime 30s      # fuzz testing
```

### External Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/hajimehoshi/ebiten/v2` | Game window rendering |
| `github.com/jxsl13/twmap` | Map file parsing |
| `github.com/teeworlds-go/huffman/v2` | Packet compression |
| `github.com/teeworlds-go/varint` | Variable-length integer encoding |
| `gorgonia.org/gorgonia` | Neural network framework |
| `gorgonia.org/tensor` | Tensor operations |

### ML Training Setup

- 20 DDNet servers (ports 8303–8322), `sv_sixup 1`, `sv_max_clients 64`
- 32 bots per server = 640 total (64/server overwhelms snapshot handling)
- `GOMAXPROCS = NumCPU()-2`; UDP recv buffer 2MB
- Reconnect jitter 0–2s prevents thundering herd
- `maps7/` directory must exist alongside `maps/` for sixup support
