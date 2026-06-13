package network

import "testing"

// V54: WithReadBufferSize sets the UDP receive-buffer size; unset keeps the
// default (2MB), <=0 falls back to default.
func TestWithReadBufferSize(t *testing.T) {
	cases := []struct {
		name string
		opts []DialOption
		want int
	}{
		{"default", nil, defaultReadBufferSize},
		{"set", []DialOption{WithReadBufferSize(4 * 1024 * 1024)}, 4 * 1024 * 1024},
		{"zero-default", []DialOption{WithReadBufferSize(0)}, defaultReadBufferSize},
		{"neg-default", []DialOption{WithReadBufferSize(-1)}, defaultReadBufferSize},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, err := Dial("127.0.0.1:34999", c.opts...)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			defer conn.Close()
			if conn.readBufferSize != c.want {
				t.Errorf("readBufferSize = %d, want %d", conn.readBufferSize, c.want)
			}
		})
	}
}
