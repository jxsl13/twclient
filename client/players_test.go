package client

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// Join makes a player present with its identity fields (V98, V103).
func TestRegistryJoinPresentName(t *testing.T) {
	c := &Client{}
	c.handleEvent(packet.EventPlayerJoin{ClientID: 3, Name: "nameless tee", Clan: "DDNet", Country: 276, Skin: "default"})
	p, ok := c.Player(3)
	if !ok {
		t.Fatal("player 3 absent after join")
	}
	if p.Name != "nameless tee" || p.Clan != "DDNet" || p.Country != 276 || p.Skin != "default" || !p.Present {
		t.Fatalf("bad join state: %+v", p)
	}
}

// Leave removes the player so no stale name lingers (V99).
func TestRegistryLeaveGone(t *testing.T) {
	c := &Client{}
	c.handleEvent(packet.EventPlayerJoin{ClientID: 3, Name: "x"})
	c.handleEvent(packet.EventPlayerLeave{ClientID: 3})
	if _, ok := c.Player(3); ok {
		t.Fatal("player 3 still present after leave")
	}
}

// A score-only update keeps identity fields (V105).
func TestRegistryScoreKeepsName(t *testing.T) {
	c := &Client{}
	c.handleEvent(packet.EventPlayerJoin{ClientID: 1, Name: "keep", Clan: "C"})
	c.handleEvent(packet.EventScoreChange{ClientID: 1, Score: 42})
	p, _ := c.Player(1)
	if p.Name != "keep" || p.Clan != "C" {
		t.Fatalf("score update wiped identity: %+v", p)
	}
	if p.Score != 42 {
		t.Fatalf("score not updated: %+v", p)
	}
}

// A team-only update keeps identity fields (V105).
func TestRegistryTeamKeepsName(t *testing.T) {
	c := &Client{}
	c.handleEvent(packet.EventPlayerJoin{ClientID: 1, Name: "keep"})
	c.handleEvent(packet.EventTeamSet{ClientID: 1, Team: 2})
	p, _ := c.Player(1)
	if p.Name != "keep" || p.Team != 2 {
		t.Fatalf("team update bad: %+v", p)
	}
}

// The local player is flagged Local (V103).
func TestRegistryLocal(t *testing.T) {
	c := &Client{}
	c.snap.localCID = 5
	c.handleEvent(packet.EventPlayerJoin{ClientID: 5, Name: "me"})
	c.handleEvent(packet.EventPlayerJoin{ClientID: 6, Name: "other"})
	if p, _ := c.Player(5); !p.Local {
		t.Fatal("local player not flagged Local")
	}
	if p, _ := c.Player(6); p.Local {
		t.Fatal("non-local flagged Local")
	}
}

// Disconnect clears the registry; ghosts do not carry across sessions (V102).
func TestRegistryClearOnDisconnect(t *testing.T) {
	c := &Client{log: slog.New(slog.DiscardHandler)} // EventClose logs
	c.handleEvent(packet.EventPlayerJoin{ClientID: 1, Name: "x"})
	c.handleEvent(packet.EventClose{Reason: "bye"})
	if len(c.Roster()) != 0 {
		t.Fatalf("registry not cleared on disconnect: %d entries", len(c.Roster()))
	}
}

// Players() yields each registered player once, merging its live character in a
// single pass (V104).
func TestPlayersIteratorMergesCharacter(t *testing.T) {
	c := &Client{}
	c.snap.characters = map[int]CharacterState{7: {X: 100, Y: 200}}
	c.handleEvent(packet.EventPlayerJoin{ClientID: 7, Name: "withchar"})
	c.handleEvent(packet.EventPlayerJoin{ClientID: 8, Name: "nochar"})

	got := map[int]PlayerView{}
	for p := range c.Players() {
		got[p.ClientID] = p
	}
	if len(got) != 2 {
		t.Fatalf("want 2 players, got %d", len(got))
	}
	if !got[7].HasCharacter || got[7].Character.X != 100 || got[7].Name != "withchar" {
		t.Fatalf("char not merged: %+v", got[7])
	}
	if got[8].HasCharacter {
		t.Fatalf("player 8 should have no character: %+v", got[8])
	}
}

// The PlayerState shape is protocol-unified: events emitted by either reader
// (here simulated as identical EventPlayerJoin) produce the same registry (V100).
func TestRegistryProtocolUnified(t *testing.T) {
	ev := packet.EventPlayerJoin{ClientID: 2, Name: "tee", Clan: "clan", Country: 1, Skin: "x", Team: 1}
	c6, c7 := &Client{}, &Client{}
	c6.handleEvent(ev)
	c7.handleEvent(ev)
	p6, _ := c6.Player(2)
	p7, _ := c7.Player(2)
	if p6 != p7 {
		t.Fatalf("registry differs by protocol: %+v vs %+v", p6, p7)
	}
}

// Concurrent reads vs eventLoop writes must be race-free (V101). Run with -race.
func TestRegistryConcurrentRead(t *testing.T) {
	c := &Client{}
	c.snap.characters = map[int]CharacterState{}
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		defer close(done)
		for i := range 5000 {
			c.handleEvent(packet.EventPlayerJoin{ClientID: i % 8, Name: "p"})
			c.handleEvent(packet.EventScoreChange{ClientID: i % 8, Score: i})
			if i%3 == 0 {
				c.handleEvent(packet.EventPlayerLeave{ClientID: i % 8})
			}
		}
	})

	for range 3 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = c.Roster()
				_, _ = c.Player(1)
				for p := range c.Players() {
					_ = p.Name
				}
			}
		})
	}
	wg.Wait()
}

// Ranging a pre-obtained iterator allocates nothing per element (V104).
func BenchmarkPlayersIterator(b *testing.B) {
	c := &Client{}
	c.players = make(map[int]PlayerState, 16)
	c.snap.characters = make(map[int]CharacterState, 16)
	for i := range 16 {
		c.players[i] = PlayerState{ClientID: i, Name: "player", Score: i, Present: true}
		c.snap.characters[i] = CharacterState{X: i, Y: i}
	}
	seq := c.Players()
	b.ReportAllocs()
	for b.Loop() {
		for p := range seq {
			_ = p.Score
		}
	}
}

// Registry update is O(1) per event with no growth-driven alloc once warm.
func BenchmarkRegistryUpdate(b *testing.B) {
	c := &Client{}
	c.players = make(map[int]PlayerState, 16)
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		c.setPlayerScore(i%16, i)
	}
}

// ExampleClient_Players renders a simple scoreboard from the registry, sorting a
// copy of the roster by score.
func ExampleClient_Players() {
	c := &Client{}
	c.handleEvent(packet.EventPlayerJoin{ClientID: 0, Name: "alice", Clan: "A"})
	c.handleEvent(packet.EventPlayerJoin{ClientID: 1, Name: "bob", Clan: "B"})
	c.handleEvent(packet.EventScoreChange{ClientID: 0, Score: 10})
	c.handleEvent(packet.EventScoreChange{ClientID: 1, Score: 25})

	roster := c.Roster()
	sort.Slice(roster, func(i, j int) bool { return roster[i].Score > roster[j].Score })
	for _, p := range roster {
		fmt.Printf("%3d  %-6s %s\n", p.Score, p.Name, p.Clan)
	}
	// Output:
	//  25  bob    B
	//  10  alice  A
}
