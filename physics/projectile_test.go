package physics

import "testing"

// ProjectilePos matches the DDNet CalcPos formula for each weapon type.
func TestProjectilePos(t *testing.T) {
	tun := DefaultTuning()

	// Gun bullet, fired horizontally for 1 second.
	// x = speed*t = 2200; y = curvature/10000 * (speed*t)^2 = 1.25/10000*2200^2.
	got := tun.ProjectilePos(Vec2{}, Vec2{X: 1, Y: 0}, WeaponGun, 1)
	wantX := float32(2200)
	wantY := 1.25 / 10000 * 2200 * 2200
	if got.X != wantX {
		t.Errorf("gun x: want %v, got %v", wantX, got.X)
	}
	if got.Y != float32(wantY) {
		t.Errorf("gun y: want %v, got %v", wantY, got.Y)
	}

	// Gravity bends the trajectory downward (positive Y in TW).
	if got.Y <= 0 {
		t.Errorf("expected downward curve, y=%v", got.Y)
	}

	// Grenade curves much more than a gun bullet (curvature 7.0 vs 1.25).
	g := tun.ProjectilePos(Vec2{}, Vec2{X: 1, Y: 0}, WeaponGrenade, 1)
	if g.Y <= got.Y {
		t.Errorf("grenade should curve more than gun: grenade=%v gun=%v", g.Y, got.Y)
	}

	// t=0 returns the start position unchanged.
	z := tun.ProjectilePos(Vec2{X: 5, Y: 9}, Vec2{X: 1, Y: 1}, WeaponGun, 0)
	if z.X != 5 || z.Y != 9 {
		t.Errorf("t=0 should be start: %#v", z)
	}
}
