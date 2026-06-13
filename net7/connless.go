package net7

import (
	"bytes"
	"fmt"

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

// ParseInfoResponse decodes a 0.7 connless info body (bytes after the
// SERVERBROWSE_INFO magic, from ConnlessInfoPayload). 0.7 uses varint numbers
// and carries hostname + skill-level fields absent in 0.6; the per-client flag
// is 0 = player, 1 = spectator (V60).
func ParseInfoResponse(body []byte) (packet.ServerInfo, error) {
	u := packer.NewUnpacker(body)
	if _, err := u.GetInt(); err != nil { // request token echo
		return packet.ServerInfo{}, fmt.Errorf("net7: info token: %w", err)
	}
	if _, err := u.GetString(); err != nil { // version
		return packet.ServerInfo{}, fmt.Errorf("net7: info version: %w", err)
	}
	name, err := u.GetString()
	if err != nil {
		return packet.ServerInfo{}, fmt.Errorf("net7: info name: %w", err)
	}
	if _, err := u.GetString(); err != nil { // hostname
		return packet.ServerInfo{}, fmt.Errorf("net7: info hostname: %w", err)
	}
	mapName, _ := u.GetString()
	gameType, _ := u.GetString()
	flags, _ := u.GetInt()
	if _, err := u.GetInt(); err != nil { // skill level
		return packet.ServerInfo{}, fmt.Errorf("net7: info skill: %w", err)
	}
	numPlayers, _ := u.GetInt()
	maxPlayers, _ := u.GetInt()
	numClients, _ := u.GetInt()
	maxClients, _ := u.GetInt()
	info := packet.ServerInfo{
		Name:       name,
		GameType:   gameType,
		MapName:    mapName,
		Passworded: flags&packet.ServerInfoFlagPassword != 0,
		NumPlayers: numPlayers,
		MaxPlayers: maxPlayers,
		NumClients: numClients,
		MaxClients: maxClients,
	}
	for {
		cname, err := u.GetString()
		if err != nil {
			break
		}
		cclan, _ := u.GetString()
		country, _ := u.GetInt()
		score, _ := u.GetInt()
		pflag, _ := u.GetInt()
		info.Clients = append(info.Clients, packet.PlayerInfo{
			Name:     cname,
			Clan:     cclan,
			Country:  country,
			Score:    score,
			IsPlayer: pflag == 0, // 0.7: 0 = player, 1 = spectator
		})
	}
	return info, nil
}
