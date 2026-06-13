package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"github.com/jxsl13/twclient/packer"
	"github.com/teeworlds-go/varint"
)

// VarintDecompress decompresses a stream of teeworlds CVariableInt-encoded
// values into an array of little-endian int32 values. Returns the number
// of bytes written to dst, or -1 on error.
func VarintDecompress(src, dst []byte) int {
	srcPos := 0
	dstPos := 0

	for srcPos < len(src) && dstPos+4 <= len(dst) {
		val, n := varint.Varint(src[srcPos:])
		if n <= 0 {
			return -1
		}
		srcPos += n
		binary.LittleEndian.PutUint32(dst[dstPos:dstPos+4], uint32(int32(val)))
		dstPos += 4
	}

	return dstPos
}

// VarintCompress compresses an array of little-endian int32 values into
// a stream of teeworlds CVariableInt-encoded values.
// This is the inverse of VarintDecompress.
func VarintCompress(src []byte) []byte {
	var out []byte
	for i := 0; i+4 <= len(src); i += 4 {
		v := int(int32(binary.LittleEndian.Uint32(src[i : i+4])))
		out = varint.AppendVarint(out, v)
	}
	return out
}

// ReadInt32LE reads a little-endian int32 from a 4-byte slice.
func ReadInt32LE(b []byte) int {
	return int(int32(binary.LittleEndian.Uint32(b)))
}

// CString extracts a null-terminated C string from a byte slice.
func CString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// CountVitalChunks scans chunk headers and updates the ack counter
// based on vital chunk sequence numbers. Resent chunks (with sequence
// numbers we've already seen) are not counted.
// The split parameter controls chunk header format (4 for 0.6, 6 for 0.7).
func CountVitalChunks(payload []byte, numChunks int, currentAck int, split int) int {
	sizeLowMask := (1 << split) - 1
	ack := currentAck
	offset := 0
	for i := 0; i < numChunks && offset < len(payload); i++ {
		if offset+2 > len(payload) {
			break
		}
		flagBits := (payload[offset] >> 6) & 0x03
		vital := flagBits&1 != 0
		size := (int(payload[offset]&0x3F) << split) | (int(payload[offset+1]) & sizeLowMask)
		hdrSize := 2
		if vital {
			hdrSize = 3
			if offset+3 <= len(payload) {
				// Extract the sequence number from the chunk header
				seq := (int(payload[offset+1]) & ^sizeLowMask & 0xFF) << 2
				seq |= int(payload[offset+2])
				// Only advance ack if this is the next expected sequence
				if seq == (ack+1)%MaxSequence {
					ack = seq
				}
			}
		}
		offset += hdrSize + size
	}
	return ack
}

// ContainsSysMsg scans chunk payload for a system message with the given ID.
func ContainsSysMsg(payload []byte, msgID int, split int) bool {
	return containsMsg(payload, msgID, true, split)
}

// ContainsGameMsg scans chunk payload for a game message with the given ID.
func ContainsGameMsg(payload []byte, msgID int, split int) bool {
	return containsMsg(payload, msgID, false, split)
}

func containsMsg(payload []byte, targetMsgID int, wantSystem bool, split int) bool {
	sizeLowMask := (1 << split) - 1
	offset := 0
	for offset < len(payload) {
		if offset+2 > len(payload) {
			break
		}
		flagBits := (payload[offset] >> 6) & 0x03
		vital := flagBits&1 != 0
		size := (int(payload[offset]&0x3F) << split) | (int(payload[offset+1]) & sizeLowMask)
		hdrSize := 2
		if vital {
			hdrSize = 3
		}
		if offset+hdrSize >= len(payload) {
			break
		}
		dataStart := offset + hdrSize
		if dataStart < len(payload) {
			b := payload[dataStart]
			// Simple int unpack for small values (common case)
			msgRaw := int(b & 0x3F)
			if b&0x40 != 0 {
				msgRaw = ^msgRaw
			}
			sys := msgRaw&1 != 0
			msgIDDecoded := msgRaw >> 1
			if sys == wantSystem && msgIDDecoded == targetMsgID {
				return true
			}
		}
		offset += hdrSize + size
	}
	return false
}

// ExtractSysMsgPayload scans chunk payload for a system message with the given ID
// and returns the message data (after the msg-id varint). Returns nil if not found.
func ExtractSysMsgPayload(payload []byte, targetMsgID int, split int) []byte {
	sizeLowMask := (1 << split) - 1
	offset := 0
	for offset < len(payload) {
		if offset+2 > len(payload) {
			break
		}
		flagBits := (payload[offset] >> 6) & 0x03
		vital := flagBits&1 != 0
		size := (int(payload[offset]&0x3F) << split) | (int(payload[offset+1]) & sizeLowMask)
		hdrSize := 2
		if vital {
			hdrSize = 3
		}
		dataStart := offset + hdrSize
		dataEnd := dataStart + size
		if dataStart < len(payload) && dataEnd <= len(payload) && size > 0 {
			u := packer.NewUnpacker(payload[dataStart:dataEnd])
			msgRaw, err := u.GetInt()
			if err == nil {
				sys := msgRaw&1 != 0
				msgIDDecoded := msgRaw >> 1
				if sys && msgIDDecoded == targetMsgID {
					remaining := u.RemainingSize()
					if remaining > 0 {
						raw, _ := u.GetRaw(remaining)
						return raw
					}
					return []byte{}
				}
			}
		}
		offset += hdrSize + size
	}
	return nil
}

// ExtractAllSysMsgPayloads returns the payloads of every system message in the
// packet matching targetMsgID (in chunk order). Unlike ExtractSysMsgPayload it
// does not stop at the first match — needed when a packet carries several
// NETMSG_EX chunks (e.g. capabilities alongside other extended messages).
func ExtractAllSysMsgPayloads(payload []byte, targetMsgID int, split int) [][]byte {
	sizeLowMask := (1 << split) - 1
	var out [][]byte
	offset := 0
	for offset < len(payload) {
		if offset+2 > len(payload) {
			break
		}
		flagBits := (payload[offset] >> 6) & 0x03
		vital := flagBits&1 != 0
		size := (int(payload[offset]&0x3F) << split) | (int(payload[offset+1]) & sizeLowMask)
		hdrSize := 2
		if vital {
			hdrSize = 3
		}
		dataStart := offset + hdrSize
		dataEnd := dataStart + size
		if dataStart < len(payload) && dataEnd <= len(payload) && size > 0 {
			u := packer.NewUnpacker(payload[dataStart:dataEnd])
			if msgRaw, err := u.GetInt(); err == nil {
				sys := msgRaw&1 != 0
				if sys && msgRaw>>1 == targetMsgID {
					if remaining := u.RemainingSize(); remaining > 0 {
						if raw, err := u.GetRaw(remaining); err == nil {
							out = append(out, raw)
						}
					}
				}
			}
		}
		offset += hdrSize + size
	}
	return out
}

// ParseMapChangePayload unpacks the MAP_CHANGE message fields:
// String(map) + Int(crc) + Int(size) + [Int(chunksPerReq) + Int(chunkSize) + Raw(32)(sha256) + String(url)].
// The bracketed fields are DDNet extensions and may be absent.
func ParseMapChangePayload(data []byte) (MapInfo, error) {
	u := packer.NewUnpacker(data)
	name, err := u.GetString()
	if err != nil {
		return MapInfo{}, fmt.Errorf("map_change: name: %w", err)
	}
	crc, err := u.GetInt()
	if err != nil {
		return MapInfo{}, fmt.Errorf("map_change: crc: %w", err)
	}
	size, err := u.GetInt()
	if err != nil {
		return MapInfo{}, fmt.Errorf("map_change: size: %w", err)
	}
	info := MapInfo{Name: name, CRC: crc, Size: size}
	// DDNet extensions: chunksPerRequest, chunkSize, sha256, url
	if _, err := u.GetInt(); err == nil { // chunksPerRequest
		if _, err := u.GetInt(); err == nil { // chunkSize
			if raw, err := u.GetRaw(32); err == nil {
				copy(info.Sha256[:], raw)
			}
		}
	}
	return info, nil
}

// IsTimeout reports whether an error is a network timeout.
func IsTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
