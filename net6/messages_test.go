package net6

import (
	"testing"

	"github.com/jxsl13/twclient/packer"
)

// V42: the server password is embedded in the NETMSG_INFO payload. A non-empty
// password produces a larger payload than an empty one.
func TestSysInfoCarriesPassword(t *testing.T) {
	withPw := SysInfo(NetVersion, "hunter2")
	withoutPw := SysInfo(NetVersion, "")
	if len(withPw) <= len(withoutPw) {
		t.Fatalf("password should enlarge INFO payload: with=%d without=%d", len(withPw), len(withoutPw))
	}
}

// sysMsgID decodes the leading system message id of a chunk payload.
func sysMsgID(t *testing.T, data []byte) (id int, sys bool) {
	t.Helper()
	raw, err := packer.NewUnpacker(data).NextInt()
	if err != nil {
		t.Fatalf("decode msg id: %v", err)
	}
	return raw >> 1, raw&1 != 0
}

// V43: net6 rcon auth/cmd messages carry the right system message id and embed
// their argument.
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
