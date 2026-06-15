package net7_test

import (
	"context"
	"fmt"
	"log"

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

// Drive a full 0.7 (sixup) session: dial, log in (token handshake → CONNECT →
// ACCEPT → INFO → map download → READY → enter game), then stream decoded server
// events from the background reader. This is the low-level path the client
// package wraps. (Compile-only: it needs a live server, and Login blocks until the
// join completes or the context is done — pass a context.WithTimeout/WithCancel in
// real code.)
func ExampleSession() {
	sess, err := net7.NewSession("127.0.0.1:8303")
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
