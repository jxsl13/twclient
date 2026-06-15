package net6

import (
	"crypto/rand"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// --- Control message builders (0.6.5) ---

// CtrlConnect builds a NET_CTRLMSG_CONNECT control message payload (0.6.5).
// The clientToken is the client's own token placed at offset 4.
func CtrlConnect(clientToken packet.Token) []byte {
	data := make([]byte, 1+512)
	data[0] = MsgCtrlConnect
	// first 4 bytes of payload are null (offset 1-4)
	copy(data[5:9], clientToken[:])
	// rest is null padding for anti-reflection
	return data
}

// CtrlConnectAccept builds a NET_CTRLMSG_CONNECTACCEPT (0.6.5).
func CtrlConnectAccept(serverToken packet.Token) []byte {
	data := make([]byte, 5)
	data[0] = MsgCtrlConnectAccept
	copy(data[1:5], serverToken[:])
	return data
}

// CtrlKeepAlive builds a NET_CTRLMSG_KEEPALIVE.
func CtrlKeepAlive() []byte {
	return []byte{MsgCtrlKeepAlive}
}

// CtrlClose builds a NET_CTRLMSG_CLOSE.
func CtrlClose(reason string) []byte {
	data := []byte{MsgCtrlClose}
	if reason != "" {
		data = append(data, packer.PackString(reason)...)
	}
	return data
}

// --- System message builders ---

// SysInfo builds a NETMSG_INFO system message chunk payload.
func SysInfo(version string, password string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgSysInfo, true)...)
	data = append(data, packer.PackString(version)...)
	data = append(data, packer.PackString(password)...)
	return data
}

// clientVerUUID is the pre-computed UUID v3 for "clientver@ddnet.tw".
var clientVerUUID = packer.CalculateUUID("clientver@ddnet.tw")

// SysClientVer builds a NETMSG_CLIENTVER extended system message chunk payload.
// This DDNet extension tells the server our DDNet version so it enables
// DDNet-specific features (capabilities, extended snap objects, etc.).
//
// Wire format:
//
//	varint(1)              — NETMSG_EX (=0) with sys flag → (0<<1)|1 = 1
//	[16 bytes]             — UUID of "clientver@ddnet.tw"
//	[16 bytes]             — random connection UUID (unique per connection)
//	varint(DDNetVersion)   — e.g. 19070
//	string(DDNetVersionStr) — e.g. "DDNet 19.0.7 (tw-protocol/go)"
func SysClientVer() []byte {
	var data []byte
	// NETMSG_EX with system flag
	data = append(data, packer.PackMsgID(MsgSysEx, true)...)
	// UUID for "clientver@ddnet.tw"
	data = append(data, clientVerUUID[:]...)
	// Random connection UUID (v4)
	var connUUID [16]byte
	_, _ = rand.Read(connUUID[:])
	connUUID[6] = (connUUID[6] & 0x0f) | 0x40 // version 4
	connUUID[8] = (connUUID[8] & 0x3f) | 0x80 // variant 1
	data = append(data, connUUID[:]...)
	// DDNet version number
	data = append(data, packer.PackInt(DDNetVersion)...)
	// DDNet version string
	data = append(data, packer.PackString(DDNetVersionStr)...)
	return data
}

// SysReady builds a NETMSG_READY system message chunk payload.
func SysReady() []byte {
	return packer.PackMsgID(MsgSysReady, true)
}

// SysEnterGame builds a NETMSG_ENTERGAME system message chunk payload.
func SysEnterGame() []byte {
	return packer.PackMsgID(MsgSysEnterGame, true)
}

// SysInput builds a NETMSG_INPUT system message chunk payload.
func SysInput(ackGameTick, predTick int, inputSize int, inputData []byte) []byte {
	// Hot 50Hz send path: append directly into one buffer (no per-field
	// PackInt allocation). 5 bytes header varints + input payload (T38, V51).
	data := make([]byte, 0, 5+len(inputData))
	data = packer.AppendMsgID(data, MsgSysInput, true)
	data = packer.AppendInt(data, ackGameTick)
	data = packer.AppendInt(data, predTick)
	data = packer.AppendInt(data, inputSize)
	data = append(data, inputData...)
	return data
}

// SysPing builds a NETMSG_PING system message chunk payload.
func SysPing() []byte {
	return packer.PackMsgID(MsgSysPing, true)
}

// SysPingReply builds a NETMSG_PING_REPLY system message chunk payload.
func SysPingReply() []byte {
	return packer.PackMsgID(MsgSysPingReply, true)
}

// SysRconAuth builds a NETMSG_RCON_AUTH system message chunk payload (DDNet 0.6
// form): a login NAME, the password, then m_SendRconCmds. DDNet's handler
// (server.cpp NETMSG_RCON_AUTH, non-sixup) reads name → password → int, so a
// password-only message is mis-read (the password becomes the name) and auth is
// silently dropped (B19). An empty name + the sv_rcon_password authenticates as
// admin. (Vanilla teeworlds 0.6 rcon is password-only — divergent; DDNet is the
// 0.6 target here, V107/?.)
func SysRconAuth(password string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgSysRconAuth, true)...)
	data = append(data, packer.PackString("")...)       // login name ("" = sv_rcon_password admin)
	data = append(data, packer.PackString(password)...) // password
	data = append(data, packer.PackInt(1)...)           // m_SendRconCmds: request the command list
	return data
}

// SysRconCmd builds a NETMSG_RCON_CMD system message chunk payload.
func SysRconCmd(cmd string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgSysRconCmd, true)...)
	data = append(data, packer.PackString(cmd)...)
	return data
}

// SysRequestMapData builds a NETMSG_REQUEST_MAP_DATA system message chunk payload.
func SysRequestMapData(chunkIdx int) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgSysRequestMapData, true)...)
	data = append(data, packer.PackInt(chunkIdx)...)
	return data
}

// --- Game message builders ---

// GameClSay builds a CL_SAY game message chunk payload.
func GameClSay(team bool, message string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClSay, false)...)
	data = append(data, packer.PackBool(team)...)
	data = append(data, packer.PackString(message)...)
	return data
}

// GameClStartInfo builds a CL_STARTINFO game message chunk payload.
// 0.6 field order: Name, Clan, Country(int), Skin, UseCustomColor, ColorBody, ColorFeet
func GameClStartInfo(name, clan string, country int, skin string, useCustomColor bool, colorBody, colorFeet int) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClStartInfo, false)...)
	data = append(data, packer.PackString(name)...)
	data = append(data, packer.PackString(clan)...)
	data = append(data, packer.PackInt(country)...)
	data = append(data, packer.PackString(skin)...)
	data = append(data, packer.PackBool(useCustomColor)...)
	data = append(data, packer.PackInt(colorBody)...)
	data = append(data, packer.PackInt(colorFeet)...)
	return data
}

// GameClSetTeam builds a CL_SETTEAM game message chunk payload.
func GameClSetTeam(team int) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClSetTeam, false)...)
	data = append(data, packer.PackInt(team)...)
	return data
}

// GameClKill builds a CL_KILL game message chunk payload.
func GameClKill() []byte {
	return packer.PackMsgID(MsgGameClKill, false)
}

// GameClEmoticon builds a CL_EMOTICON game message chunk payload.
func GameClEmoticon(emoticon packet.Emoticon) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClEmoticon, false)...)
	data = append(data, packer.PackInt(int(emoticon))...)
	return data
}

// GameClVote builds a CL_VOTE game message chunk payload.
func GameClVote(vote int) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClVote, false)...)
	data = append(data, packer.PackInt(vote)...)
	return data
}

// GameClCallVote builds a CL_CALLVOTE game message chunk payload.
func GameClCallVote(voteType, value, reason string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClCallVote, false)...)
	data = append(data, packer.PackString(voteType)...)
	data = append(data, packer.PackString(value)...)
	data = append(data, packer.PackString(reason)...)
	return data
}

// GameClSetSpectatorMode builds a CL_SETSPECTATORMODE game message chunk payload.
func GameClSetSpectatorMode(spectatorID int) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClSetSpectatorMode, false)...)
	data = append(data, packer.PackInt(spectatorID)...)
	return data
}

// GameClChangeInfo builds a CL_CHANGEINFO game message chunk payload.
func GameClChangeInfo(name, clan string, country int, skin string, useCustomColor bool, colorBody, colorFeet int) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClChangeInfo, false)...)
	data = append(data, packer.PackString(name)...)
	data = append(data, packer.PackString(clan)...)
	data = append(data, packer.PackInt(country)...)
	data = append(data, packer.PackString(skin)...)
	data = append(data, packer.PackBool(useCustomColor)...)
	data = append(data, packer.PackInt(colorBody)...)
	data = append(data, packer.PackInt(colorFeet)...)
	return data
}

// --- Chunk wrapping helpers ---

// WrapVitalChunk wraps a message payload in a vital chunk header (0.6 format, Split=4).
func WrapVitalChunk(payload []byte, seq int) []byte {
	hdr := packet.ChunkHeader{
		Flags: packet.ChunkFlags{Vital: true},
		Size:  len(payload),
		Seq:   seq,
	}
	return append(hdr.Pack(Split), payload...)
}

// WrapChunk wraps a message payload in a non-vital chunk header (0.6 format, Split=4).
func WrapChunk(payload []byte) []byte {
	hdr := packet.ChunkHeader{
		Flags: packet.ChunkFlags{Vital: false},
		Size:  len(payload),
	}
	return append(hdr.Pack(Split), payload...)
}
