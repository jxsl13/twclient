package net6_test

import (
	"context"
	"fmt"
	"log"

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

// Drive a full 0.6 session: dial, log in (handshake → INFO → READY → enter game),
// download the map, then stream decoded server events from the background reader.
// This is the low-level path the client package wraps. (Compile-only: it needs a
// live server, and Login blocks until the join completes or the context is done —
// pass a context.WithTimeout/WithCancel in real code.)
func ExampleSession() {
	sess, err := net6.NewSession("127.0.0.1:8303")
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()

	ctx := context.Background()
	if err := sess.Login(ctx, "nameless tee", ""); err != nil {
		log.Fatal(err)
	}
	if _, err := sess.DownloadMap(ctx); err != nil {
		log.Fatal(err)
	}
	sess.StartReader(ctx)
	for ev := range sess.EventCh() {
		fmt.Printf("event: %T\n", ev)
	}
}
