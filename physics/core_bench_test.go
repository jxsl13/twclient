package physics

import "testing"

// BenchmarkCoreTick measures one predicted physics step (Tick+Move), the
// per-tick-per-character cost of the prediction re-sim. With antiping the
// predicted world runs this for every visible character every predicted tick.
//
// Grounded horizontal movement keeps velocity at the ground cap (~10 u/tick),
// so MoveBox does a bounded number of substeps — representative of a real
// re-sim (a fresh core advanced a few ticks from the acked snap), unlike an
// airborne hook that runs to the 6000 u/tick clamp and bloats MoveBox.
func BenchmarkCoreTick(b *testing.B) {
	c := standOn(floorCol(10), 10)
	in := Input{Direction: 1}
	b.ReportAllocs()
	for b.Loop() {
		c.Tick(in)
		c.Move()
	}
}
