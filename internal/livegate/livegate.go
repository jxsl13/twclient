// Package livegate is a TEST-ONLY, process-global serializer for connects to
// Teeworlds 0.7 servers in the e2e/live suite.
//
// Vanilla teeworlds 0.7 flood-bans repeated connects from one source IP and
// caps clients-per-IP, so a dense table-driven live run (many subtests, one
// runner IP) trips the ban and the later connects get refused — they then skip
// (dialClientOrSkip), gutting live coverage in CI (B17/B18, V120/V121). Enter
// serializes ALL 0.7 connects across the test binary and spaces them beyond the
// ban window — the "space connects beyond the ban window" remedy V120 calls
// for. 0.6 connects pass straight through (DDNet 0.6 tolerates the rate).
//
// This is a harness aid only, never imported by the shipped client.
package livegate

import (
	"sync"
	"time"

	"github.com/jxsl13/twclient/packet"
)

// Spacing is the minimum gap between consecutive 0.7 connects — wide enough to
// clear the vanilla teeworlds flood-ban window (B17/V120). Tests may override it
// before connecting (e.g. shrink it in the package's own unit test).
var Spacing = 1500 * time.Millisecond

var (
	mu   sync.Mutex
	last time.Time
)

// Enter serializes a connect to a 0.7 server process-wide: it blocks until any
// in-flight 0.7 connect finishes, waits out Spacing since the previous one
// completed, then returns a release func to call when THIS connect attempt is
// done. For any non-0.7 version it is a no-op. Hold the gate only across the
// Connect call, not the whole test:
//
//	release := livegate.Enter(version)
//	err := c.Connect(ctx)
//	release()
func Enter(v packet.Version) (release func()) {
	if v != packet.Version07 {
		return func() {}
	}
	mu.Lock()
	if d := Spacing - time.Since(last); d > 0 {
		time.Sleep(d)
	}
	return func() {
		last = time.Now()
		mu.Unlock()
	}
}
