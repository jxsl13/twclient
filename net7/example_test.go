package net7_test

import (
	"fmt"

	"github.com/jxsl13/twclient/net7"
	"github.com/jxsl13/twclient/packet"
)

// Build a 0.7 connless server-info request (no session). 0.7 frames connless
// packets with a 9-byte token header, then SERVERBROWSE_GETINFO ("gie3") and a
// varint request token — see teeworlds src/engine/client/serverbrowser.cpp.
// The token handshake (BuildTokenRequest/ParseTokenResponse) supplies the
// server token first.
func ExampleBuildInfoRequestConnless() {
	var srv, cli packet.Token
	req := net7.BuildInfoRequestConnless(srv, cli, 1)
	fmt.Println(string(req[13:17])) // 9-byte header + 4×0xFF, then the magic tag
	// Output: gie3
}
