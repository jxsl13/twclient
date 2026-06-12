package client

import (
	"testing"

	"github.com/jxsl13/twclient/packet"
)

// V2/V17: server events flow through handleEvent to registered callbacks,
// independent of which protocol reader produced them (the event types are
// shared). handleEvent has no per-type case for these, so they exercise the
// generic dispatch tail.
func TestHandleEventDispatchesServerEvents(t *testing.T) {
	c := &Client{}

	var chat packet.EventChat
	var whisper packet.EventWhisper
	var kill packet.EventKill
	chatFired, whisperFired, killFired := false, false, false

	c.OnChat(func(_ *Client, e packet.EventChat) { chatFired = true; chat = e })
	c.OnWhisper(func(_ *Client, e packet.EventWhisper) { whisperFired = true; whisper = e })
	c.OnKill(func(_ *Client, e packet.EventKill) { killFired = true; kill = e })

	c.handleEvent(packet.EventChat{ClientID: 9, Msg: "hi"})
	c.handleEvent(packet.EventWhisper{FromID: 3, Msg: "psst"})
	c.handleEvent(packet.EventKill{Killer: 1, Victim: 2, Weapon: packet.WeaponGun})

	if !chatFired || chat.ClientID != 9 || chat.Msg != "hi" {
		t.Errorf("chat not dispatched correctly: fired=%v %#v", chatFired, chat)
	}
	if !whisperFired || whisper.FromID != 3 {
		t.Errorf("whisper not dispatched correctly: fired=%v %#v", whisperFired, whisper)
	}
	if !killFired || kill.Weapon != packet.WeaponGun {
		t.Errorf("kill not dispatched correctly: fired=%v %#v", killFired, kill)
	}
}

// V17: a handler registered once fires for the same event type regardless of
// the "source" — there is a single shared struct, so a 0.6-produced and a
// 0.7-produced event are indistinguishable to the consumer.
func TestCrossProtocolSameHandler(t *testing.T) {
	c := &Client{}
	count := 0
	c.OnChat(func(_ *Client, _ packet.EventChat) { count++ })

	// Simulate the same logical event arriving from each protocol reader.
	from06 := packet.EventChat{ClientID: 1, Msg: "x"}
	from07 := packet.EventChat{ClientID: 1, Msg: "x"}
	c.handleEvent(from06)
	c.handleEvent(from07)

	if count != 2 {
		t.Errorf("same handler should fire for both protocols' events, got %d", count)
	}
}

// V7: unregister stops further dispatch through handleEvent.
func TestHandleEventRespectsUnregister(t *testing.T) {
	c := &Client{}
	count := 0
	unreg := c.OnBroadcast(func(_ *Client, _ packet.EventBroadcast) { count++ })

	c.handleEvent(packet.EventBroadcast{Text: "a"})
	unreg()
	c.handleEvent(packet.EventBroadcast{Text: "b"})

	if count != 1 {
		t.Errorf("unregister should stop dispatch: want 1, got %d", count)
	}
}
