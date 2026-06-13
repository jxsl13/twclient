package packer

import "testing"

// BenchmarkNewUnpacker models the per-inbound-message allocation the reader
// pays today: every message constructs a fresh Unpacker that copies the
// payload (make([]byte,len)+copy). This is the cost T37 targets on the hot
// snap path.
func BenchmarkNewUnpacker(b *testing.B) {
	data := make([]byte, 256)
	b.ReportAllocs()
	for b.Loop() {
		u := NewUnpacker(data)
		_ = u
	}
}

// BenchmarkUnpackerReset measures the steady-state cost of a pooled Unpacker
// reused across messages (the T37 target shape): Reset reuses the grown buffer
// so no per-message allocation after warmup.
func BenchmarkUnpackerReset(b *testing.B) {
	var buf []byte
	for i := range 256 {
		buf = append(buf, PackInt(i*7-100)...)
	}
	u := NewUnpacker(nil)
	b.ReportAllocs()
	for b.Loop() {
		u.Reset(buf)
		for u.RemainingSize() > 0 {
			if _, err := u.GetInt(); err != nil {
				break
			}
		}
	}
}

func BenchmarkUnpackInt(b *testing.B) {
	data := PackInt(123456)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = UnpackInt(data)
	}
}

func BenchmarkGetStringSanitized(b *testing.B) {
	data := append([]byte("a moderately long player chat message hello world"), 0)
	u := NewUnpacker(data)
	b.ReportAllocs()
	for b.Loop() {
		u.Reset(data)
		if _, err := u.GetString(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPackInt(b *testing.B) {
	b.ReportAllocs()
	n := 0
	for b.Loop() {
		_ = PackInt(n)
		n++
	}
}

func BenchmarkPackStr(b *testing.B) {
	const s = "default"
	b.ReportAllocs()
	for b.Loop() {
		_ = PackStr(s)
	}
}

func BenchmarkPackMsgID(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = PackMsgID(20, true)
	}
}

// BenchmarkAppendSysInput models the 50Hz input-send build path using the
// Append* primitives into a single reused buffer (T38): a msg id + 3 ints +
// payload with no per-field allocation.
func BenchmarkAppendSysInput(b *testing.B) {
	payload := make([]byte, 40)
	dst := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		d := dst[:0]
		d = AppendMsgID(d, 16, true)
		d = AppendInt(d, 123456)
		d = AppendInt(d, 123457)
		d = AppendInt(d, 40)
		d = append(d, payload...)
		_ = d
	}
}

// BenchmarkPackSysInput is the pre-T38 shape (one PackInt alloc per field)
// for comparison.
func BenchmarkPackSysInput(b *testing.B) {
	payload := make([]byte, 40)
	b.ReportAllocs()
	for b.Loop() {
		var d []byte
		d = append(d, PackMsgID(16, true)...)
		d = append(d, PackInt(123456)...)
		d = append(d, PackInt(123457)...)
		d = append(d, PackInt(40)...)
		d = append(d, payload...)
		_ = d
	}
}
