package net6

import "testing"

// V42: the server password is embedded in the NETMSG_INFO payload. A non-empty
// password produces a larger payload than an empty one.
func TestSysInfoCarriesPassword(t *testing.T) {
	withPw := SysInfo(NetVersion, "hunter2")
	withoutPw := SysInfo(NetVersion, "")
	if len(withPw) <= len(withoutPw) {
		t.Fatalf("password should enlarge INFO payload: with=%d without=%d", len(withPw), len(withoutPw))
	}
}
