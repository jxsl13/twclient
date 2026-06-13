package master

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/jxsl13/twclient/network"
	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// Connless server-info query: ask a single server for its info over the
// connless protocol, without opening a play session (V57/V58). Wire format
// pinned from DDNet (0.6) and teeworlds (0.7) sources — see SPEC §R.
//
// Both protocols use the same browser magics. 0.6 frames connless packets with
// a 6-byte 0xFF prefix and encodes numbers as decimal strings; 0.7 frames them
// with a 9-byte token header (and a prior NET_CTRLMSG_TOKEN handshake) and
// encodes numbers as varints.
var (
	browseGetInfo = []byte{255, 255, 255, 255, 'g', 'i', 'e', '3'} // SERVERBROWSE_GETINFO
	browseInfo    = []byte{255, 255, 255, 255, 'i', 'n', 'f', '3'} // SERVERBROWSE_INFO
)

const (
	connless6Prefix = 6 // 0.6: six 0xFF bytes precede connless data
	connless7Header = 9 // 0.7: flag byte + token(4) + responsetoken(4)

	ctrlMsgToken      = 5          // NET_CTRLMSG_TOKEN (0.7)
	netTokenNone      = 0xffffffff // NET_TOKEN_NONE (0.7)
	flagControl7      = 1          // NET_PACKETFLAG_CONTROL (0.7)
	flagConnless7     = 8          // NET_PACKETFLAG_CONNLESS (0.7)
	packetVersion7    = 1          // NET_PACKETVERSION (0.7)
	tokenRequestBytes = 512        // padded token-request data (anti-amplification)

	serverInfoFlagPassword = 1 // SERVER_FLAG_PASSWORD / SERVERINFO_FLAG_PASSWORD

	maxInfoReads = 8 // stray-packet tolerance while waiting for the info reply
)

// queryConfig holds QueryServerInfo options.
type queryConfig struct {
	timeout time.Duration
}

// QueryOption configures QueryServerInfo.
type QueryOption func(*queryConfig)

// WithQueryTimeout sets the overall timeout for the query (default 5s).
func WithQueryTimeout(d time.Duration) QueryOption {
	return func(qc *queryConfig) {
		if d > 0 {
			qc.timeout = d
		}
	}
}

// QueryServerInfo asks the server at addr for its current info over the
// connless protocol, without logging in (V57). version selects the wire format
// (packet.Version06 / packet.Version07). It opens only a UDP socket — no
// Handshake, Login, or session — and returns the parsed ServerInfo (incl. the
// current Clients player list). The call is bounded by the option timeout and
// the context.
func QueryServerInfo(ctx context.Context, version packet.Version, addr string, opts ...QueryOption) (ServerInfo, error) {
	qc := queryConfig{timeout: 5 * time.Second}
	for _, o := range opts {
		o(&qc)
	}
	ctx, cancel := context.WithTimeout(ctx, qc.timeout)
	defer cancel()

	conn, err := network.Dial(addr)
	if err != nil {
		return ServerInfo{}, fmt.Errorf("master: dial %s: %w", addr, err)
	}
	defer conn.Close()

	switch version {
	case packet.Version06:
		return query6(ctx, conn)
	case packet.Version07:
		return query7(ctx, conn)
	default:
		return ServerInfo{}, fmt.Errorf("master: unsupported version %v", version)
	}
}

// --- 0.6 ---

func query6(ctx context.Context, conn *network.Conn) (ServerInfo, error) {
	token := byte(rand.Intn(256))
	req := make([]byte, 0, connless6Prefix+len(browseGetInfo)+1)
	for range connless6Prefix {
		req = append(req, 0xff)
	}
	req = append(req, browseGetInfo...)
	req = append(req, token)
	if err := conn.SendRaw(req); err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.6 send getinfo: %w", err)
	}

	for range maxInfoReads {
		data, err := conn.RecvContext(ctx)
		if err != nil {
			return ServerInfo{}, fmt.Errorf("master: 0.6 recv info: %w", err)
		}
		if len(data) < connless6Prefix+len(browseInfo) {
			continue
		}
		body := data[connless6Prefix:]
		if !bytes.Equal(body[:len(browseInfo)], browseInfo) {
			continue // not an inf3 packet
		}
		return parseInfo6(body[len(browseInfo):])
	}
	return ServerInfo{}, fmt.Errorf("master: 0.6 no info response")
}

// parseInfo6 decodes the 0.6 inf3 body: NUL-terminated strings throughout, with
// numbers encoded as decimal strings (DDNet ADD_INT).
func parseInfo6(b []byte) (ServerInfo, error) {
	u := packer.NewUnpacker(b)
	// token, version (skipped)
	if _, err := u.GetString(); err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.6 token: %w", err)
	}
	if _, err := u.GetString(); err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.6 version: %w", err)
	}
	name, err := u.GetString()
	if err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.6 name: %w", err)
	}
	mapName, _ := u.GetString()
	gameType, _ := u.GetString()
	flags := decInt6(u)
	info := ServerInfo{
		Name:       name,
		GameType:   gameType,
		MapName:    mapName,
		Passworded: flags&serverInfoFlagPassword != 0,
		NumPlayers: decInt6(u),
		MaxPlayers: decInt6(u),
		NumClients: decInt6(u),
		MaxClients: decInt6(u),
	}
	for {
		cname, err := u.GetString()
		if err != nil {
			break // end of client list
		}
		cclan, _ := u.GetString()
		info.Clients = append(info.Clients, PlayerInfo{
			Name:     cname,
			Clan:     cclan,
			Country:  decInt6(u),
			Score:    decInt6(u),
			IsPlayer: decInt6(u) != 0, // 0.6: 1 = player
		})
	}
	return info, nil
}

// decInt6 reads one decimal-string integer from the 0.6 body (0 on error/EOF).
func decInt6(u *packer.Unpacker) int {
	s, err := u.GetString()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// --- 0.7 ---

func query7(ctx context.Context, conn *network.Conn) (ServerInfo, error) {
	myToken := rand.Uint32()

	// 1. Token handshake: control NET_CTRLMSG_TOKEN with our token; the server
	// replies with its token, which routes our connless getinfo to it.
	tokReq := make([]byte, 0, 7+1+tokenRequestBytes)
	tokReq = append(tokReq, byte((flagControl7<<2)&0xfc), 0, 0) // flags=CONTROL, ack=0, numchunks=0
	tokReq = appendBE32(tokReq, netTokenNone)                   // header token = NONE
	tokReq = append(tokReq, ctrlMsgToken)                       // NET_CTRLMSG_TOKEN
	tokReq = appendBE32(tokReq, myToken)                        // our token
	for len(tokReq) < 7+1+tokenRequestBytes {
		tokReq = append(tokReq, 0) // pad (anti-amplification)
	}
	if err := conn.SendRaw(tokReq); err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.7 send token request: %w", err)
	}
	serverToken, err := recvServerToken7(ctx, conn)
	if err != nil {
		return ServerInfo{}, err
	}

	// 2. Connless getinfo, routed with the server's token.
	gi := make([]byte, 0, connless7Header+len(browseGetInfo)+2)
	gi = append(gi, byte((flagConnless7<<2)&0xfc|(packetVersion7&0x03)))
	gi = appendBE32(gi, serverToken)
	gi = appendBE32(gi, myToken)
	gi = append(gi, browseGetInfo...)
	gi = append(gi, packer.PackInt(int(rand.Int31()))...) // request token (varint, echoed back)
	if err := conn.SendRaw(gi); err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.7 send getinfo: %w", err)
	}

	for range maxInfoReads {
		data, err := conn.RecvContext(ctx)
		if err != nil {
			return ServerInfo{}, fmt.Errorf("master: 0.7 recv info: %w", err)
		}
		if len(data) < connless7Header+len(browseInfo) {
			continue
		}
		body := data[connless7Header:]
		if !bytes.Equal(body[:len(browseInfo)], browseInfo) {
			continue
		}
		return parseInfo7(body[len(browseInfo):])
	}
	return ServerInfo{}, fmt.Errorf("master: 0.7 no info response")
}

// recvServerToken7 waits for the server's NET_CTRLMSG_TOKEN control reply and
// returns the token it assigned (carried in the control data).
func recvServerToken7(ctx context.Context, conn *network.Conn) (uint32, error) {
	for range maxInfoReads {
		data, err := conn.RecvContext(ctx)
		if err != nil {
			return 0, fmt.Errorf("master: 0.7 recv token: %w", err)
		}
		// 7-byte control header + ctrl byte + 4-byte token.
		if len(data) < 7+1+4 {
			continue
		}
		flags := data[0] >> 2
		if flags&flagControl7 == 0 || data[7] != ctrlMsgToken {
			continue
		}
		return binary.BigEndian.Uint32(data[8:12]), nil
	}
	return 0, fmt.Errorf("master: 0.7 no token reply")
}

// parseInfo7 decodes the 0.7 inf3 body: strings are NUL-terminated, numbers are
// varints (teeworlds GenerateServerInfo). 0.7 carries hostname + skill_level
// fields absent in 0.6, and a per-client flag where 0 = player.
func parseInfo7(b []byte) (ServerInfo, error) {
	u := packer.NewUnpacker(b)
	if _, err := u.GetInt(); err != nil { // request token echo
		return ServerInfo{}, fmt.Errorf("master: 0.7 token: %w", err)
	}
	if _, err := u.GetString(); err != nil { // version
		return ServerInfo{}, fmt.Errorf("master: 0.7 version: %w", err)
	}
	name, err := u.GetString()
	if err != nil {
		return ServerInfo{}, fmt.Errorf("master: 0.7 name: %w", err)
	}
	if _, err := u.GetString(); err != nil { // hostname
		return ServerInfo{}, fmt.Errorf("master: 0.7 hostname: %w", err)
	}
	mapName, _ := u.GetString()
	gameType, _ := u.GetString()
	flags, _ := u.GetInt()
	if _, err := u.GetInt(); err != nil { // skill level
		return ServerInfo{}, fmt.Errorf("master: 0.7 skill: %w", err)
	}
	numPlayers, _ := u.GetInt()
	maxPlayers, _ := u.GetInt()
	numClients, _ := u.GetInt()
	maxClients, _ := u.GetInt()
	info := ServerInfo{
		Name:       name,
		GameType:   gameType,
		MapName:    mapName,
		Passworded: flags&serverInfoFlagPassword != 0,
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
		info.Clients = append(info.Clients, PlayerInfo{
			Name:     cname,
			Clan:     cclan,
			Country:  country,
			Score:    score,
			IsPlayer: pflag == 0, // 0.7: 0 = player, 1 = spectator
		})
	}
	return info, nil
}

func appendBE32(b []byte, v uint32) []byte {
	var x [4]byte
	binary.BigEndian.PutUint32(x[:], v)
	return append(b, x[:]...)
}
