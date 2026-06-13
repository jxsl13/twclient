package net7

import (
	"bytes"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// BuildInfoRequestConnless builds a 0.7 connless server-info request
// (SERVERBROWSE_GETINFO) routed with the server's token (obtained via the
// NET_CTRLMSG_TOKEN handshake — BuildTokenRequest + ParseTokenResponse). reqToken
// is an arbitrary value the server echoes back. No session is involved (V59).
func BuildInfoRequestConnless(serverToken, clientToken packet.Token, reqToken int) []byte {
	hdr := Header{
		Flags:         Flags{Connless: true},
		Token:         serverToken,
		ResponseToken: clientToken,
	}
	b := hdr.Pack()
	b = append(b, packet.ServerBrowseGetInfo...)
	b = append(b, packer.PackInt(reqToken)...)
	return b
}

// ParseTokenResponse extracts the server's token from a NET_CTRLMSG_TOKEN control
// reply (the token the server assigned us, carried at payload offset 8-11).
// Mirrors the offsets used by Session.Handshake. ok is false if the datagram is
// not such a reply.
func ParseTokenResponse(datagram []byte) (packet.Token, bool) {
	var hdr Header
	if err := hdr.Unpack(datagram); err != nil {
		return packet.Token{}, false
	}
	if !hdr.Flags.Control || len(datagram) < 12 || datagram[7] != MsgCtrlToken {
		return packet.Token{}, false
	}
	var tok packet.Token
	copy(tok[:], datagram[8:12])
	return tok, true
}

// ConnlessInfoPayload strips the 9-byte connless header and the SERVERBROWSE_INFO
// magic from a received 0.7 datagram, returning the body that follows the magic.
// ok is false if the datagram is too short or is not an inf3 packet.
func ConnlessInfoPayload(datagram []byte) ([]byte, bool) {
	if len(datagram) < HeaderSizeConnless+len(packet.ServerBrowseInfo) {
		return nil, false
	}
	body := datagram[HeaderSizeConnless:]
	if !bytes.Equal(body[:len(packet.ServerBrowseInfo)], packet.ServerBrowseInfo) {
		return nil, false
	}
	return body[len(packet.ServerBrowseInfo):], true
}
