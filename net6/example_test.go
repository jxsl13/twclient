package net6_test

import (
	"fmt"

	"github.com/jxsl13/twclient/net6"
)

// Build a 0.6 connless server-info request (no session). The packet is the
// 6-byte 0xFF connless prefix + SERVERBROWSE_GETINFO magic ("gie3") + a request
// token byte — see DDNet src/engine/client/serverbrowser.cpp.
func ExampleBuildInfoRequestConnless() {
	req := net6.BuildInfoRequestConnless(0x42)
	fmt.Println(string(req[10:14])) // 6×0xFF + 4×0xFF, then the magic tag
	// Output: gie3
}
