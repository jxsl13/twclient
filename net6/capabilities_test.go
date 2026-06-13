package net6

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// V47: a capabilities@ddnet.tw NETMSG_EX is decoded, stored on the session, and
// emitted as an event.
func TestProcessExCapabilities(t *testing.T) {
	s := &Session{}
	s.reader.eventCh = make(chan packet.Event, 4)

	// body = Version, Flags (after the 16-byte UUID, already stripped by processEx).
	body := append(packer.PackInt(1), packer.PackInt(packet.ServerCapFlagDDNet|packet.ServerCapFlagChatTimeoutCode)...)

	// Dispatch through processEx with the UUID prefix to exercise the routing.
	msg := append(uuidCapabilities[:], body...)
	s.processEx(msg)

	caps := s.Capabilities()
	if !caps.DDNet || !caps.ChatTimeoutCode {
		t.Fatalf("caps not stored on session: %+v", caps)
	}

	ev := <-s.reader.eventCh
	ce, ok := ev.(packet.EventServerCapabilities)
	if !ok {
		t.Fatalf("want EventServerCapabilities, got %T", ev)
	}
	if !ce.Caps.ChatTimeoutCode {
		t.Errorf("event caps wrong: %+v", ce.Caps)
	}
}
