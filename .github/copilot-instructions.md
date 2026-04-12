# twclient – Copilot Instructions

This project implements a Teeworlds 0.6/0.7 protocol client and ML training bot in Go.
The canonical protocol reference is in [docs/PROTOCOL.md](../docs/PROTOCOL.md).
For codebase navigation, see [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) — it contains the package dependency graph, key types, data flow, and cross-cutting concerns.

## Key conventions

- **Always** consult `docs/PROTOCOL.md` before implementing or modifying protocol logic.
- DDNet uses the **0.6.4-based** protocol with **TKEN security token extension** (not vanilla 0.6.5 header tokens).
- Security tokens are **appended to packet payload**, not placed in the header.
- Chunk header Split = 4 for 0.6, Split = 6 for 0.7.
- All integers are varint-encoded (ESDDDDDD EDDDDDDD ...).
- Strings are null-terminated C strings.

## Protocol improvement process

The protocol definition in `docs/PROTOCOL.md` is a **living document**.

When working on protocol code:

1. **Read** `docs/PROTOCOL.md` first for the current understanding.
2. **Cross-reference** with (in priority order):
   1. **DDNet source code** (canonical for DDNet servers):
      - `src/engine/shared/network.h` — constants, flag bits, struct definitions
      - `src/engine/shared/network.cpp` — `SendPacket()`, `UnpackPacket()`, `CNetChunkHeader::Pack/Unpack()`
      - `src/engine/shared/network_conn.cpp` — `SendConnect()`, `Feed()`, state machine
      - `src/engine/shared/network_server.cpp` — `OnPreConnMsg()`, `OnConnCtrlMsg()`
   2. **chillerdragon docs** (excellent for understanding, may lag behind source):
      - https://chillerdragon.github.io/teeworlds-protocol/06/
   3. **teeworlds-go/protocol** (Go reference implementation for 0.7):
      - https://github.com/teeworlds-go/protocol
3. **Update** `docs/PROTOCOL.md` if you discover discrepancies.
4. **Document** the source of each correction in the revision log at the bottom.

## Key gotchas

### DDNet ≠ Vanilla 0.6.5
DDNet is based on **0.6.4** with the TKEN extension. It does NOT use the 0.6.5 token header flag.
- 0.6.5: 7-byte header with token in bytes 3-6, flag bit 1 set
- DDNet: 3-byte header, security token appended to end of payload

### Flag Byte Layout
```
byte[0] = ((flags << 2) & 0xFC) | ((ack >> 8) & 0x03)
```
Internal flag values: `UNUSED=1, TOKEN=2, CONTROL=4, CONNLESS=8, RESEND=16, COMPRESSION=32`.
On the wire, CONTROL appears at bit 4 of byte[0], not bit 2.

### CONNECT Payload Must Be ≥512 Bytes
The server requires `pPacket->m_DataSize >= 1+512` for CONNECT packets (anti-reflection).

### Security Token in DDNet CONNECTACCEPT
Payload: `[0x02] [T K E N] [SecurityToken(4 bytes)]`. TKEN magic at `payload[1:5]`, actual token at `payload[5:9]`.

### Compression Includes Security Token
DDNet appends the security token to `m_aChunkData` **before** huffman compression.

## Reuse before reimplementing

Before writing any helper function, constant, or utility from scratch, **search the existing codebase first** — especially the `packet/` package. Common things already defined there include:

- Physics constants (gravity, hook parameters, tee sizes, velocities)
- Input types and constructors (`PlayerInput`, `Direction`, `JumpState`, `HookState`, `Weapon`, etc.)
- Coordinate/tile conversion helpers
- Varint encoding/decoding
- Message packing utilities

Run `grep -r "YourThing" packet/ replay/ client/` or use semantic search before creating a new function.

## Development commands

```bash
# Build everything
go build ./...

# Run all tests
go test ./... -v

# Run specific test against DDNet server
TW_TARGET=localhost:8303 go test ./client -run TestLogin06 -v -count=1 -timeout 30s

# Run fuzz tests
go test ./client -fuzz FuzzPostHandshakeChunks -fuzztime 30s
```
