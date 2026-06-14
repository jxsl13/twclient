package demo_test

import (
	"bytes"
	"fmt"

	"github.com/jxsl13/twclient/record/demo"
)

// Parse a minimal demo and write it back unchanged.
func ExampleFile_WriteTo() {
	f := &demo.File{
		Header: demo.Header{
			Version:    demo.Version,
			NetVersion: "0.6 626fce9a778df4d4",
			MapName:    "Tutorial",
			Type:       "client",
			Timestamp:  "2026-04-08_21-52-08",
		},
		Chunks: []demo.Chunk{
			demo.TickMarker{Tick: 10, Keyframe: true},
			demo.DataChunk{Type: demo.ChunkTypeSnapshot, Data: []byte{0x01, 0x02}},
			demo.TickMarker{Tick: 11},
			demo.DataChunk{Type: demo.ChunkTypeMessage, Data: []byte{0x03}},
		},
	}

	var buf bytes.Buffer
	f.WriteTo(&buf)

	g, _ := demo.Parse(&buf)
	fmt.Println(g.Header.MapName, len(g.Chunks))
	fmt.Println(g.Chunks[0], g.Chunks[2])
	// Output:
	// Tutorial 4
	// {10 true} {11 false}
}

// Stream a demo chunk by chunk with the Decoder.
func ExampleDecoder() {
	src := &demo.File{
		Header: demo.Header{Version: demo.Version, MapName: "Tutorial"},
		Chunks: []demo.Chunk{
			demo.TickMarker{Tick: 5, Keyframe: true},
			demo.DataChunk{Type: demo.ChunkTypeDelta, Data: []byte{0xaa}},
			demo.TickMarker{Tick: 6},
		},
	}
	var buf bytes.Buffer
	src.WriteTo(&buf)

	d, _ := demo.NewDecoder(&buf)
	fmt.Println(d.Header().MapName)
	for {
		ch, err := d.Next()
		if err != nil {
			break
		}
		switch c := ch.(type) {
		case demo.TickMarker:
			fmt.Printf("tick %d keyframe=%t\n", c.Tick, c.Keyframe)
		case demo.DataChunk:
			fmt.Printf("data %s %d bytes\n", c.Type, len(c.Data))
		}
	}
	// Output:
	// Tutorial
	// tick 5 keyframe=true
	// data delta 1 bytes
	// tick 6 keyframe=false
}
