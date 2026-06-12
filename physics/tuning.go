package physics

// Tuning holds the DDNet physics constants relevant to character movement.
// Defaults match the vanilla values in src/game/tuning.h. They are exposed
// as a struct so alternative tunings (e.g. modified servers) can be tested.
type Tuning struct {
	Gravity float32

	GroundControlSpeed float32
	GroundControlAccel float32
	GroundFriction     float32

	AirControlSpeed float32
	AirControlAccel float32
	AirFriction     float32

	GroundJumpImpulse float32
	AirJumpImpulse    float32

	HookLength    float32
	HookFireSpeed float32
	HookDragAccel float32
	HookDragSpeed float32

	VelrampStart     float32
	VelrampRange     float32
	VelrampCurvature float32

	GrenadeSpeed      float32
	GrenadeCurvature  float32
	GrenadeLifetime   float32 // seconds
	ExplosionStrength float32

	// Gun/shotgun projectile ballistics, used to predict bullet trajectories
	// (DDNet tuning.h).
	GunSpeed         float32
	GunCurvature     float32
	ShotgunSpeed     float32
	ShotgunCurvature float32
}

// DefaultTuning returns the vanilla DDNet tuning values.
func DefaultTuning() Tuning {
	return Tuning{
		Gravity: 0.5,

		GroundControlSpeed: 10.0,
		GroundControlAccel: 2.0, // 100 / SERVER_TICK_SPEED
		GroundFriction:     0.5,

		AirControlSpeed: 5.0, // 250 / SERVER_TICK_SPEED
		AirControlAccel: 1.5,
		AirFriction:     0.95,

		GroundJumpImpulse: 13.2,
		AirJumpImpulse:    12.0,

		HookLength:    380.0,
		HookFireSpeed: 80.0,
		HookDragAccel: 3.0,
		HookDragSpeed: 15.0,

		VelrampStart:     550.0,
		VelrampRange:     2000.0,
		VelrampCurvature: 1.4,

		GrenadeSpeed:      1000.0,
		GrenadeCurvature:  7.0,
		GrenadeLifetime:   2.0,
		ExplosionStrength: 6.0,

		GunSpeed:         2200.0,
		GunCurvature:     1.25,
		ShotgunSpeed:     2750.0,
		ShotgunCurvature: 1.25,
	}
}

// ProjectilePos computes a projectile's position at the given time (seconds
// since it was fired), mirroring DDNet CalcPos / CProjectile::GetPos. start is
// the launch position, dir the launch direction (unit-ish, from the snapshot
// velocity / 100), and weapon selects the curvature/speed.
func (t Tuning) ProjectilePos(start, dir Vec2, weapon int, time float32) Vec2 {
	var curvature, speed float32
	switch weapon {
	case WeaponGrenade:
		curvature, speed = t.GrenadeCurvature, t.GrenadeSpeed
	case WeaponShotgun:
		curvature, speed = t.ShotgunCurvature, t.ShotgunSpeed
	default: // WeaponGun and others
		curvature, speed = t.GunCurvature, t.GunSpeed
	}
	st := speed * time
	return Vec2{
		X: start.X + dir.X*st,
		Y: start.Y + dir.Y*st + curvature/10000*st*st,
	}
}

// Weapon ids matching the protocol (also defined in package packet); kept here
// so physics has no dependency on packet.
const (
	WeaponGun     = 2
	WeaponShotgun = 3
	WeaponGrenade = 4
)

// Physics-wide constants matching the DDNet engine.
const (
	// TickSpeed is the server simulation rate (ticks per second).
	TickSpeed = 50

	// PhysicalSize is the side length of the tee collision box.
	PhysicalSize float32 = 28.0

	// DefaultJumps is the number of jumps a tee has by default
	// (one ground jump + one air jump).
	DefaultJumps = 2

	// TileSize is the world-unit side length of a map tile.
	TileSize = 32
)

// Hook states, mirroring HOOK_* in DDNet.
const (
	HookRetracted    = -1
	HookIdle         = 0
	HookRetractStart = 1
	HookRetractEnd   = 3
	HookFlying       = 4
	HookGrabbed      = 5
)
