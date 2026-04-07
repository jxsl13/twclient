# Teeworlds Protocol Fuzzer – Agent Instructions

## Protocol Reference

**Always** read [PROTOCOL.md](PROTOCOL.md) before any protocol work.
It contains the authoritative Mermaid diagrams for:
- Packet header layouts (0.6.4/DDNet vs 0.6.5 vs 0.7)
- Chunk header bit packing (Split=4 for 0.6, Split=6 for 0.7)
- Connection handshake sequences (DDNet TKEN vs vanilla 0.6.5)
- All control, system, and game message formats with field order
- Varint encoding format
- Security token placement rules

## Protocol Definition Improvement Process

The protocol definition in `PROTOCOL.md` is a **living document**.
Follow this process when discovering new information:

### Step 1: Identify discrepancy
When a test fails, a connection drops, or behavior differs from documentation:
1. Note the exact symptom (e.g., "server drops packet after READY")
2. Capture the raw bytes if possible

### Step 2: Cross-reference sources (in priority order)
1. **DDNet source code** (canonical for DDNet servers):
   - `src/engine/shared/network.h` — constants, flag bits, struct definitions
   - `src/engine/shared/network.cpp` — `SendPacket()`, `UnpackPacket()`, `CNetChunkHeader::Pack/Unpack()`
   - `src/engine/shared/network_conn.cpp` — `SendConnect()`, `Feed()`, state machine
   - `src/engine/shared/network_server.cpp` — `OnPreConnMsg()`, `OnConnCtrlMsg()`
2. **chillerdragon docs** (excellent for understanding, may lag behind source):
   - https://chillerdragon.github.io/teeworlds-protocol/06/
3. **teeworlds-go/protocol** (Go reference implementation for 0.7):
   - https://github.com/teeworlds-go/protocol

### Step 3: Update PROTOCOL.md
- Fix the affected diagram or table
- Add a row to the **Revision Log** at the bottom with:
  - Date
  - What changed
  - Which source confirmed the correction

### Step 4: Update implementation
- Fix the corresponding Go code in `packet/`, `chunk/`, `net6/`, `net7/`, or `client/`
- Run tests: `go test ./... -v`

## Key Gotchas Discovered

### DDNet ≠ Vanilla 0.6.5
DDNet is based on **0.6.4** with the TKEN extension. It does NOT use the 0.6.5 token header flag.
- 0.6.5: 7-byte header with token in bytes 3-6, flag bit 1 set
- DDNet: 3-byte header, security token appended to end of payload

### Flag Byte Layout
The flag byte stores flags in bits 2-7 and ack high bits in bits 0-1:
```
byte[0] = ((flags << 2) & 0xFC) | ((ack >> 8) & 0x03)
```
DDNet uses `flags << 2`, and the **internal** flag values are:
```
UNUSED=1, TOKEN=2, CONTROL=4, CONNLESS=8, RESEND=16, COMPRESSION=32
```
So on the wire, CONTROL appears at bit 4 of byte[0], not bit 2.

### Chunk Header Split Parameter
- **0.6:** Split = 4 → size uses 6+4=10 bits, seq uses 4+8=12 bits (but only 10 used)
- **0.7:** Split = 6 → size uses 6+6=12 bits, seq uses 2+8=10 bits

### CONNECT Payload Must Be ≥512 Bytes
The server requires `pPacket->m_DataSize >= 1+512` for CONNECT packets (anti-reflection).
That means 1 byte ctrl msg + 512 bytes extra data.

### Security Token in DDNet CONNECTACCEPT
The server sends `CONNECTACCEPT` with payload: `[0x02] [T K E N] [SecurityToken(4 bytes)]`.
The "TKEN" magic is at `payload[1:5]`, the actual token at `payload[5:9]`.

### Compression Includes Security Token
DDNet appends the security token to `m_aChunkData` **before** attempting huffman compression.
This means the token gets compressed along with the chunk data.

## Development Commands

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
