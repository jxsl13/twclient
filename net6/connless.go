package net6

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/jxsl13/twclient/packer"
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

// ParseInfoResponse decodes a 0.6 connless info body (the bytes after the
// SERVERBROWSE_INFO magic, from ConnlessInfoPayload). 0.6 encodes every field
// as a NUL-terminated string; numbers are decimal strings (V60).
func ParseInfoResponse(body []byte) (packet.ServerInfo, error) {
	u := packer.NewUnpacker(body)
	// token, version (skipped)
	if _, err := u.GetString(); err != nil {
		return packet.ServerInfo{}, fmt.Errorf("net6: info token: %w", err)
	}
	if _, err := u.GetString(); err != nil {
		return packet.ServerInfo{}, fmt.Errorf("net6: info version: %w", err)
	}
	name, err := u.GetString()
	if err != nil {
		return packet.ServerInfo{}, fmt.Errorf("net6: info name: %w", err)
	}
	mapName, _ := u.GetString()
	gameType, _ := u.GetString()
	flags := decStr(u)
	info := packet.ServerInfo{
		Name:       name,
		GameType:   gameType,
		MapName:    mapName,
		Passworded: flags&packet.ServerInfoFlagPassword != 0,
		NumPlayers: decStr(u),
		MaxPlayers: decStr(u),
		NumClients: decStr(u),
		MaxClients: decStr(u),
	}
	for {
		cname, err := u.GetString()
		if err != nil {
			break // end of client list
		}
		cclan, _ := u.GetString()
		info.Clients = append(info.Clients, packet.PlayerInfo{
			Name:     cname,
			Clan:     cclan,
			Country:  decStr(u),
			Score:    decStr(u),
			IsPlayer: decStr(u) != 0, // 0.6: 1 = player
		})
	}
	return info, nil
}

// decStr reads one decimal-string integer from a 0.6 info body (0 on error/EOF).
func decStr(u *packer.Unpacker) int {
	s, err := u.GetString()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
