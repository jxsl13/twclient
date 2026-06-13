package net6

import (
	"bytes"

	"github.com/jxsl13/twclient/packet"
)

// connlessPrefix is the 6-byte 0xFF sentinel that frames a 0.6 connless packet
// (real Teeworlds connless framing — distinct from the connless flag bit in the
// regular 3-byte header).
const connlessPrefix = 6

// BuildInfoRequestConnless builds a 0.6 connless server-info request
// (SERVERBROWSE_GETINFO) carrying a one-byte request token the server echoes
// back. No session/handshake is involved (V59).
func BuildInfoRequestConnless(reqToken byte) []byte {
	b := make([]byte, 0, connlessPrefix+len(packet.ServerBrowseGetInfo)+1)
	for range connlessPrefix {
		b = append(b, 0xff)
	}
	b = append(b, packet.ServerBrowseGetInfo...)
	b = append(b, reqToken)
	return b
}

// ConnlessInfoPayload strips the 6-byte connless prefix and the SERVERBROWSE_INFO
// magic from a received 0.6 datagram, returning the body that follows the magic.
// ok is false if the datagram is too short or is not an inf3 packet.
func ConnlessInfoPayload(datagram []byte) ([]byte, bool) {
	if len(datagram) < connlessPrefix+len(packet.ServerBrowseInfo) {
		return nil, false
	}
	body := datagram[connlessPrefix:]
	if !bytes.Equal(body[:len(packet.ServerBrowseInfo)], packet.ServerBrowseInfo) {
		return nil, false
	}
	return body[len(packet.ServerBrowseInfo):], true
}
