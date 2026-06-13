package packer_test

import (
	"fmt"

	"github.com/jxsl13/twclient/packer"
)

// Pack an int with the Teeworlds variable-length encoding, then read it back —
// the wire format used by every game message (cf. teeworlds CVariableInt).
func ExamplePackInt() {
	wire := packer.PackInt(1337)
	n, err := packer.NewUnpacker(wire).NextInt()
	fmt.Println(n, err)
	// Output: 1337 <nil>
}

// Strings are NUL-terminated on the wire; NextString reads one back.
func ExampleUnpacker_NextString() {
	wire := packer.PackStr("hello")
	s, err := packer.NewUnpacker(wire).NextString()
	fmt.Printf("%q %v\n", s, err)
	// Output: "hello" <nil>
}
