package net7

import "github.com/jxsl13/twclient/packet"

// BuildCtrlPacket builds a complete 0.7 control packet.
func BuildCtrlPacket(token packet.Token, ack int, payload []byte) []byte {
	hdr := Header{
		Flags: Flags{Control: true},
		Ack:   ack,
		Token: token,
	}
	return append(hdr.Pack(), payload...)
}

// BuildTokenRequest builds the initial client→server token request packet (0.7).
func BuildTokenRequest(clientToken packet.Token) []byte {
	return BuildCtrlPacket(packet.TokenEmpty, 0, CtrlToken(clientToken, true))
}

// BuildConnect builds a client→server connect packet (0.7).
func BuildConnect(serverToken, clientToken packet.Token) []byte {
	return BuildCtrlPacket(serverToken, 0, CtrlConnect(clientToken))
}

// BuildKeepAlive builds a keep-alive packet (0.7).
func BuildKeepAlive(token packet.Token, ack int) []byte {
	return BuildCtrlPacket(token, ack, CtrlKeepAlive())
}

// BuildClose builds a disconnect packet (0.7).
func BuildClose(token packet.Token, ack int, reason string) []byte {
	return BuildCtrlPacket(token, ack, CtrlClose(reason))
}

// BuildChunkPacket builds a 0.7 packet containing chunk data.
func BuildChunkPacket(token packet.Token, ack, numChunks int, compressed bool, chunkData []byte) []byte {
	hdr := Header{
		Flags: Flags{
			Compression: compressed,
		},
		Ack:       ack,
		NumChunks: numChunks,
		Token:     token,
	}
	return append(hdr.Pack(), chunkData...)
}

// BuildInfoPacket builds a full NETMSG_INFO packet (version + password) for 0.7.
func BuildInfoPacket(token packet.Token, ack int, seq int) []byte {
	msgPayload := SysInfo(NetVersion, "")
	chunkData := WrapVitalChunk(msgPayload, seq)
	return BuildChunkPacket(token, ack, 1, false, chunkData)
}

// BuildReadyPacket builds a NETMSG_READY packet for 0.7.
func BuildReadyPacket(token packet.Token, ack int, seq int) []byte {
	chunkData := WrapVitalChunk(SysReady(), seq)
	return BuildChunkPacket(token, ack, 1, false, chunkData)
}

// BuildEnterGamePacket builds a NETMSG_ENTERGAME packet for 0.7.
func BuildEnterGamePacket(token packet.Token, ack int, seq int) []byte {
	chunkData := WrapVitalChunk(SysEnterGame(), seq)
	return BuildChunkPacket(token, ack, 1, false, chunkData)
}

// BuildStartInfoPacket builds a CL_STARTINFO packet for 0.7.
func BuildStartInfoPacket(token packet.Token, ack int, seq int,
	name, clan string, country int) []byte {
	skinParts := [6]string{"standard", "", "", "standard", "standard", "standard"}
	useCustom := [6]bool{true, false, false, true, true, false}
	colors := [6]int{65408, 0, 0, 65408, 65408, 0}
	msgPayload := GameClStartInfo(name, clan, country, skinParts, useCustom, colors)
	chunkData := WrapVitalChunk(msgPayload, seq)
	return BuildChunkPacket(token, ack, 1, false, chunkData)
}
