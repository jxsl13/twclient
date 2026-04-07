# Teeworlds Protocol – Copilot Instructions

This project implements a Teeworlds 0.6/0.7 protocol client and ML training bot in Go.
The canonical protocol reference is in [PROTOCOL.md](../PROTOCOL.md).

## Key conventions

- **Always** consult `PROTOCOL.md` before implementing or modifying protocol logic.
- DDNet uses the **0.6.4-based** protocol with **TKEN security token extension** (not vanilla 0.6.5 header tokens).
- Security tokens are **appended to packet payload**, not placed in the header.
- Chunk header Split = 4 for 0.6, Split = 6 for 0.7.
- All integers are varint-encoded (ESDDDDDD EDDDDDDD ...).
- Strings are null-terminated C strings.

## Protocol improvement process

When working on protocol code:

1. **Read** `PROTOCOL.md` first for the current understanding.
2. **Cross-reference** with:
   - chillerdragon docs: https://chillerdragon.github.io/teeworlds-protocol/06/
   - DDNet source: https://github.com/ddnet/ddnet/blob/master/src/engine/shared/network.cpp
   - teeworlds-go/protocol: https://github.com/teeworlds-go/protocol
3. **Update** `PROTOCOL.md` if you discover discrepancies.
4. **Document** the source of each correction in the revision log at the bottom.

## Project structure

```
packet/   – Packet header types (Header06, Header07) and shared constants
chunk/    – Chunk header Pack/Unpack (shared between 0.6 and 0.7)
packer/   – Varint, string, message-ID packing
net6/     – 0.6 protocol constants and message builders
net7/     – 0.7 protocol constants and message builders
client/  – Session management, packet builders, mutators, protocol tests
```
