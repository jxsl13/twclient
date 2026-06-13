# twclient

A headless [Teeworlds](https://www.teeworlds.com/) / [DDNet](https://ddnet.org/)
client library in Go. It speaks both protocol **0.6** (DDNet variant) and **0.7**
(sixup) from scratch — packet headers, chunk framing, varint messages, delta
snapshots, the connect/TKEN handshake, login retransmission, automatic
reconnect, rcon, client-side prediction (antiping), and the master-server
browser.

```go
import "github.com/jxsl13/twclient/client"
```

## Minimal dependencies

The library imports only [`twmap`](https://github.com/jxsl13/twmap),
[`teeworlds-go/huffman`](https://github.com/teeworlds-go/huffman), and
[`teeworlds-go/varint`](https://github.com/teeworlds-go/varint) — **no cgo, no
OpenGL, no ML framework**. The example bots under `cmd/` (an ebiten GUI + a
gorgonia ML trainer) live in a **separate module** (`cmd/go.mod`) so their heavy
dependencies never reach library consumers.

## Quickstart

```go
package main

import (
	"context"
	"fmt"

	"github.com/jxsl13/twclient/client"
	"github.com/jxsl13/twclient/packet"
)

func main() {
	c := client.New("127.0.0.1:8303",
		client.WithPlayerInfo("nameless tee", "", "default", -1),
		client.WithVersion(packet.Version06), // or packet.Version07
	)

	c.OnChat(func(_ *client.Client, e packet.EventChat) {
		fmt.Printf("[chat] %s\n", e.Msg)
	})

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil { // handshake + login + map
		panic(err)
	}
	defer c.Close()

	_ = c.SendChat("hello world")
	// read game state any time: c.Character(), c.RaceTime(), c.LastSnapTick() …
	select {}
}
```

## Per-tick observation

The client runs a 50 Hz tick loop. Plug an `Observer` (view-only) or a single
`Controller` (view + action) to receive a `TickState` each tick. For a hot,
zero-allocation read of every player, use `RangePlayers`:

```go
c.RangePlayers(func(id int, ch client.CharacterState) bool {
	fmt.Printf("player %d at (%d,%d)\n", id, ch.X, ch.Y)
	return true // false to stop early
})
```

Enable client-side prediction with `client.WithPrediction(true)` (or
`WithAntiping(true)` to predict every entity).

## Server browser

Fetch the master list and query a single server connless (no play session):

```go
m := master.New() // DDNet masters, fastest-validated policy (DDNet CChooseMaster)
servers, _ := m.FetchServerList(ctx)
info, _ := m.QueryServerInfo(ctx, packet.Version06, "127.0.0.1:8303")
fmt.Println(info.Name, len(info.Clients), "players")
```

## Packages

| package | role |
|---|---|
| `client` | high-level API: connect, prediction, callbacks, tick-driven consumers |
| `master` | master-server list (HTTP/JSON) + connless server-info query |
| `net6` / `net7` | protocol 0.6 / 0.7 sessions, builders, parsers |
| `packet` | version-agnostic types: events, snapshots, inputs, server info |
| `packer` | varint + NUL-string wire codec |
| `physics` | DDNet-faithful character physics (for prediction) |
| `network` | raw UDP transport |

## Configuration

All knobs are explicit options (`With…`) with exported defaults (`Default…`);
the library reads **no environment variables**. Examples: client
`WithSnapStorageSize`/`WithReconnectPolicy`/`WithReadBufferSize`, network
`WithReadTimeout`, master `WithRequestPolicy`.

## Docs

Full API reference + runnable examples on
[pkg.go.dev](https://pkg.go.dev/github.com/jxsl13/twclient), or `go doc ./client`.
