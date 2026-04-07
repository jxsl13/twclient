package net6

import "github.com/jxsl13/twclient/packet"

// BuildCtrlPacket builds a complete 0.6.5 control packet (with token).
func BuildCtrlPacket(token packet.Token, ack int, payload []byte) []byte {
	hdr := Header{
		Flags: Flags{
			Control: true,
			Token:   true,
		},
		Ack:   ack,
		Token: token,
	}
	return append(hdr.Pack(), payload...)
}

// BuildCtrlPacketNoToken builds a 0.6.4-style control packet (no token).
func BuildCtrlPacketNoToken(ack int, payload []byte) []byte {
	hdr := Header{
		Flags: Flags{
			Control: true,
		},
		Ack: ack,
	}
	return append(hdr.Pack(), payload...)
}

// BuildConnect builds a client→server connect packet (0.6.5).
// Per the C++ source, CONNECT is always sent WITHOUT the token flag in the header.
// The client token is embedded in the payload at offset 4 instead.
func BuildConnect(clientToken packet.Token) []byte {
	return BuildCtrlPacketNoToken(0, CtrlConnect(clientToken))
}

// BuildKeepAlive builds a keep-alive packet (0.6).
func BuildKeepAlive(token packet.Token, ack int) []byte {
	return BuildCtrlPacket(token, ack, CtrlKeepAlive())
}

// BuildClose builds a disconnect packet (0.6).
func BuildClose(token packet.Token, ack int, reason string) []byte {
	return BuildCtrlPacket(token, ack, CtrlClose(reason))
}

// BuildChunkPacket builds a 0.6.5 packet with chunk data.
func BuildChunkPacket(token packet.Token, ack, numChunks int, compressed bool, chunkData []byte) []byte {
	hdr := Header{
		Flags: Flags{
			Token:       true,
			Compression: compressed,
		},
		Ack:       ack,
		NumChunks: numChunks,
		Token:     token,
	}
	return append(hdr.Pack(), chunkData...)
}

// BuildInfoPacket builds a NETMSG_INFO packet for 0.6.
func BuildInfoPacket(token packet.Token, ack int, seq int) []byte {
	msgPayload := SysInfo(NetVersion, "")
	chunkData := WrapVitalChunk(msgPayload, seq)
	return BuildChunkPacket(token, ack, 1, false, chunkData)
}

// BuildReadyPacket builds a NETMSG_READY packet for 0.6.
func BuildReadyPacket(token packet.Token, ack int, seq int) []byte {
	chunkData := WrapVitalChunk(SysReady(), seq)
	return BuildChunkPacket(token, ack, 1, false, chunkData)
}

// BuildEnterGamePacket builds a NETMSG_ENTERGAME packet for 0.6.
func BuildEnterGamePacket(token packet.Token, ack int, seq int) []byte {
	chunkData := WrapVitalChunk(SysEnterGame(), seq)
	return BuildChunkPacket(token, ack, 1, false, chunkData)
}

// BuildStartInfoPacket builds a CL_STARTINFO packet for 0.6.
func BuildStartInfoPacket(token packet.Token, ack int, seq int,
	name, clan, skin string, country int) []byte {
	msgPayload := GameClStartInfo(name, clan, country, skin, true, 65408, 65408)
	chunkData := WrapVitalChunk(msgPayload, seq)
	return BuildChunkPacket(token, ack, 1, false, chunkData)
}
