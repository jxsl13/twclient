package teehistorian_test

import (
	"bytes"
	"fmt"

	"github.com/jxsl13/twclient/packet"
	"github.com/jxsl13/twclient/record/teehistorian"
)

// Build a minimal file, write it, and parse it back — the records round-trip.
func ExampleFile_WriteTo() {
	f := &teehistorian.File{
		Header: teehistorian.Header{Raw: []byte(`{"version":"2"}`)},
		Records: []teehistorian.Record{
			teehistorian.TickSkip{Dt: 0},
			teehistorian.PlayerNew{Cid: 0, X: 100, Y: 200},
			teehistorian.PlayerDiff{Cid: 0, Dx: 5, Dy: -3},
			teehistorian.Finish{},
		},
	}

	var buf bytes.Buffer
	f.WriteTo(&buf)

	g, _ := teehistorian.Parse(&buf)
	fmt.Println(len(g.Records), g.Records[1])
	// Output: 4 {0 100 200}
}

// Ticks yields each player's absolute position per tick.
func ExampleFile_Ticks() {
	f := &teehistorian.File{
		Header: teehistorian.Header{Raw: []byte(`{"version":"2"}`)},
		Records: []teehistorian.Record{
			teehistorian.TickSkip{Dt: 0}, // tick 1
			teehistorian.PlayerNew{Cid: 0, X: 100, Y: 200},
			teehistorian.PlayerDiff{Cid: 0, Dx: 10, Dy: 0}, // cid resets → tick 2
		},
	}
	f.Ticks(func(tick int, players map[int]teehistorian.PlayerState, _ map[int]packet.PlayerInput) bool {
		fmt.Printf("tick %d: %v\n", tick, players[0])
		return true
	})
	// Output:
	// tick 1: {100 200}
	// tick 2: {110 200}
}
