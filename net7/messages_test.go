package net7

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
)

func sysMsgID(t *testing.T, data []byte) (id int, sys bool) {
	t.Helper()
	raw, err := packer.NewUnpacker(data).GetInt()
	if err != nil {
		t.Fatalf("decode msg id: %v", err)
	}
	return raw >> 1, raw&1 != 0
}

// V43: net7 rcon auth/cmd messages carry the right system message id and embed
// their argument — same surface as net6 (protocol-unified rcon).
func TestSysRconMessages(t *testing.T) {
	auth := SysRconAuth("hunter2")
	if id, sys := sysMsgID(t, auth); !sys || id != MsgSysRconAuth {
		t.Errorf("rcon auth msg id = %d sys=%v, want %d/true", id, sys, MsgSysRconAuth)
	}
	if len(SysRconAuth("hunter2")) <= len(SysRconAuth("")) {
		t.Error("password should enlarge rcon auth payload")
	}

	cmd := SysRconCmd("status")
	if id, sys := sysMsgID(t, cmd); !sys || id != MsgSysRconCmd {
		t.Errorf("rcon cmd msg id = %d sys=%v, want %d/true", id, sys, MsgSysRconCmd)
	}
	if len(SysRconCmd("status")) <= len(SysRconCmd("")) {
		t.Error("command should enlarge rcon cmd payload")
	}
}
