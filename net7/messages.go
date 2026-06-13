package net7

import (
	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// --- Control message builders ---

// CtrlToken builds a NET_CTRLMSG_TOKEN control message payload.
// The responseToken is the sender's own token.
// If withPadding is true, 508 null bytes are appended (required by client).
func CtrlToken(responseToken packet.Token, withPadding bool) []byte {
	data := []byte{MsgCtrlToken}
	data = append(data, responseToken[:]...)
	if withPadding {
		data = append(data, make([]byte, packet.AntiReflectionSize)...)
	}
	return data
}

// CtrlConnect builds a NET_CTRLMSG_CONNECT control message payload.
// responseToken is the client's own token (echoed in payload).
func CtrlConnect(responseToken packet.Token) []byte {
	data := make([]byte, 1+4+packet.AntiReflectionSize)
	data[0] = MsgCtrlConnect
	copy(data[1:5], responseToken[:])
	return data
}

// CtrlAccept builds a NET_CTRLMSG_ACCEPT control message payload.
func CtrlAccept() []byte {
	return []byte{MsgCtrlAccept}
}

// CtrlKeepAlive builds a NET_CTRLMSG_KEEPALIVE control message payload.
func CtrlKeepAlive() []byte {
	return []byte{MsgCtrlKeepAlive}
}

// CtrlClose builds a NET_CTRLMSG_CLOSE control message payload.
func CtrlClose(reason string) []byte {
	data := []byte{MsgCtrlClose}
	if reason != "" {
		data = append(data, packer.PackStr(reason)...)
	}
	return data
}

// --- System message builders ---

// SysInfo builds a NETMSG_INFO system message chunk payload (version + password).
func SysInfo(version string, password string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgSysInfo, true)...)
	data = append(data, packer.PackStr(version)...)
	data = append(data, packer.PackStr(password)...)
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

// SysRconAuth builds a NETMSG_RCON_AUTH system message chunk payload.
func SysRconAuth(password string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgSysRconAuth, true)...)
	data = append(data, packer.PackStr(password)...)
	return data
}

// SysRconCmd builds a NETMSG_RCON_CMD system message chunk payload.
func SysRconCmd(cmd string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgSysRconCmd, true)...)
	data = append(data, packer.PackStr(cmd)...)
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
	data = append(data, packer.PackStr(message)...)
	return data
}

// GameClStartInfo builds a CL_STARTINFO game message chunk payload.
func GameClStartInfo(name, clan string, country int,
	skinParts [6]string, useCustomColors [6]bool, colorBody [6]int) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClStartInfo, false)...)
	data = append(data, packer.PackStr(name)...)
	data = append(data, packer.PackStr(clan)...)
	data = append(data, packer.PackInt(country)...)
	for i := range 6 {
		data = append(data, packer.PackStr(skinParts[i])...)
	}
	for i := range 6 {
		data = append(data, packer.PackBool(useCustomColors[i])...)
	}
	for i := range 6 {
		data = append(data, packer.PackInt(colorBody[i])...)
	}
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

// GameClVote builds a CL_VOTE chunk payload (1 yes, -1 no).
func GameClVote(vote int) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClVote, false)...)
	data = append(data, packer.PackInt(vote)...)
	return data
}

// GameClCallVote builds a CL_CALLVOTE chunk payload. 0.7 adds a Force flag
// (always 0 here).
func GameClCallVote(voteType, value, reason string) []byte {
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClCallVote, false)...)
	data = append(data, packer.PackStr(voteType)...)
	data = append(data, packer.PackStr(value)...)
	data = append(data, packer.PackStr(reason)...)
	data = append(data, packer.PackInt(0)...) // m_Force
	return data
}

// GameClSetSpectatorMode builds a CL_SETSPECTATORMODE chunk payload. 0.7 takes
// a spec mode plus a target id; mode 2 = SPEC_PLAYER when a valid id is given.
func GameClSetSpectatorMode(spectatorID int) []byte {
	mode := 0 // SPEC_FREEVIEW
	if spectatorID >= 0 {
		mode = 2 // SPEC_PLAYER
	}
	var data []byte
	data = append(data, packer.PackMsgID(MsgGameClSetSpectatorMode, false)...)
	data = append(data, packer.PackInt(mode)...)
	data = append(data, packer.PackInt(spectatorID)...)
	return data
}

// --- Chunk wrapping helper ---

// WrapVitalChunk wraps a message payload in a vital chunk header (0.7 format, Split=6).
func WrapVitalChunk(payload []byte, seq int) []byte {
	hdr := packet.ChunkHeader{
		Flags: packet.ChunkFlags{Vital: true},
		Size:  len(payload),
		Seq:   seq,
	}
	return append(hdr.Pack(Split), payload...)
}

// WrapChunk wraps a message payload in a non-vital chunk header (0.7 format, Split=6).
func WrapChunk(payload []byte) []byte {
	hdr := packet.ChunkHeader{
		Flags: packet.ChunkFlags{Vital: false},
		Size:  len(payload),
	}
	return append(hdr.Pack(Split), payload...)
}
