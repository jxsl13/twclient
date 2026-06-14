package ghost_test

import (
	"bytes"
	"fmt"

	"github.com/jxsl13/twclient/record/ghost"
)

// Build a small ghost file, write it, and parse it back — the chunks round-trip.
func ExampleFile_WriteTo() {
	f := &ghost.File{
		Header: ghost.Header{Version: 6, Owner: "nameless tee", Map: "Tutorial", NumTicks: 3, Time: 120},
		Chunks: []ghost.Chunk{
			ghost.StartTick{Tick: 100},
			ghost.Skin{Skin: [6]int{1, 2, 3, 4, 5, 6}, UseCustomColor: 1, ColorBody: 255, ColorFeet: 65280},
			ghost.Character{CharacterNoTick: ghost.CharacterNoTick{X: 10, Y: 20}, Tick: 100},
			ghost.Character{CharacterNoTick: ghost.CharacterNoTick{X: 15, Y: 20}, Tick: 101},
		},
	}

	var buf bytes.Buffer
	f.WriteTo(&buf)

	g, _ := ghost.Parse(&buf)
	fmt.Println(len(g.Chunks))
	fmt.Println(g.Chunks[0])
	fmt.Println(g.Chunks[2].(ghost.Character).X, g.Chunks[3].(ghost.Character).X)
	// Output:
	// 4
	// {100}
	// 10 15
}

// Decoder streams items one at a time off a reader.
func ExampleDecoder() {
	f := &ghost.File{
		Header: ghost.Header{Version: 6, Owner: "tee", Map: "m", NumTicks: 1, Time: 1},
		Chunks: []ghost.Chunk{
			ghost.StartTick{Tick: 7},
			ghost.Character{CharacterNoTick: ghost.CharacterNoTick{X: 1}, Tick: 7},
			ghost.Character{CharacterNoTick: ghost.CharacterNoTick{X: 2}, Tick: 8},
		},
	}
	var buf bytes.Buffer
	f.WriteTo(&buf)

	d, _ := ghost.NewDecoder(&buf)
	fmt.Println(d.Header().Owner)
	count := 0
	for {
		if _, err := d.Next(); err != nil {
			break
		}
		count++
	}
	fmt.Println(count)
	// Output:
	// tee
	// 3
}
