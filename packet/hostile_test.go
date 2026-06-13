package packet

import "testing"

// V70: garbage/truncated/nil payloads to the public parse + chunk surface must
// never panic — they return an error or an empty/zero result.
func TestHostileInputNoPanic(t *testing.T) {
	garbage := [][]byte{
		nil,
		{},
		{0x00},
		{0xff, 0xff, 0xff},
		{0x40, 0x01, 0x02}, // chunk-header-ish prefix, then nothing
		make([]byte, 2048),
	}
	for _, b := range garbage {
		_, _ = ParseMapChangePayload(b)
		_ = UnpackChunks(b, Split06())
		_ = UnpackChunks(b, Split07())
		_ = CountVitalChunks(b, 4, 0, Split06())
		_ = ContainsSysMsg(b, 2, Split06())
		_ = ExtractSysMsgPayload(b, 2, Split06())
		_ = ExtractAllSysMsgPayloads(b, 2, Split06())
		// delta decode against an empty base must not panic on garbage
		_, _ = applyDelta(&Snapshot{}, 1, b, nil)
	}

	// Out-of-range option values are clamped, not panicked.
	_ = ParseServerCapabilities(-1, -1)
	_ = ApplyLoginOptions(nil)
}

// Split06/Split07 mirror the per-protocol chunk split params used in tests
// without importing net6/net7 (avoids a cycle).
func Split06() int { return 4 }
func Split07() int { return 6 }
