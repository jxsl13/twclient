//go:build e2e

// Package e2e holds the Docker-backed end-to-end test harness for the twclient
// (SPEC T132 / V114). The Go tests drive the high-level client + net6/net7
// Session against the bot-populated game servers in docker-compose.yml.
// Everything is behind the `e2e` build tag and gated at runtime by TW_E2E=1 so
// it never runs in the normal hermetic suite (V118). See README.md.
//
// # Live-test parity audit & mock keep-list (T155 / V119)
//
// CONVERTED TO LIVE — driven against the real dockerized servers, TABLE-DRIVEN
// over ddnet-0.6, ddnet-0.7 (sixup) and vanilla teeworlds 0.7 (V119/V107). Each
// behavior is observable on a real server, so a mock is no longer the primary
// coverage:
//
//	login → map download → decoded snapshot   TestLiveLoginSnapshot   (T150) all 3
//	actions (chat/kill/emote/team/spectate)   TestLiveActions         (T151) all 3
//	client rcon (auth + command + line)       TestLiveRcon            (T152) all 3
//	auto-reconnect / resume                    TestLiveReconnect       (T153) all 3
//	server capabilities                        TestLiveCapabilities    (T153) ddnet-0.6
//	wrong-password reject (CTRL_CLOSE)         TestLiveWrongPassword   (T154) both pw
//	error states: kick / ban / unreachable /   TestLiveErrorStates     (T156)
//	  ctx-cancel (econ-provoked drops)                                  kick all 3
//	0.6≡0.7 shared-object decode parity        TestE2EParity           (T137)
//
// PARITY EXCEPTIONS (DDNet-only, documented per V119/V107) — skipped/limited on
// vanilla 0.7 because the feature genuinely does not exist there:
//
//	server capabilities (V47)   asserted only on ddnet-0.6; ddnet-0.7-over-sixup
//	                            is a known net7 caps-parse gap (B20); vanilla = none
//	timeout-code resume (V32)   DDNet-0.6 refinement; vanilla just rejoins (T153)
//	DDNet ext snap objects      DDNet-only; not on vanilla
//
// MOCK RETAINED — a real server cannot deterministically produce the condition,
// so the hand-rolled mock / stub stays (NOT replaced by a live test):
//
//	packet loss / login retransmission   net6+net7 login_retransmit_test  (B6/T59)
//	  — a lossy conn dropping the first N datagrams of each step; a real server
//	    will not drop on demand.
//	malformed / hostile wire             net6+net7+client hostile_test    (V70)
//	  — feeds garbage/truncated bytes; a real server never sends those.
//	shared-buffer data race              net7 huffbuf_race/regression     (B7/T79)
//	  — concurrent-receiver timing under -race; not server-observable.
//	pure-unit client logic               reconnect backoff sequence/abort (T28),
//	  DisconnectReason classification of reasons a server won't readily emit
//	  (TimedOut), option plumbing/clamps, Action→Send mapping (stubSession).
//	  — no server interaction needed.
//
// MASTER-server tests are EXCLUDED from this rework (they already hit the real
// DDNet master, opt-in via TW_LIVE per V118) — not a Docker mock.
package e2e
