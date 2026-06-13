package physics

// WorldConfig selects which physics subsystems the predicted world simulates,
// mirroring DDNet's CWorldCore::CWorldConfig (gameworld.h). Prediction physics
// differs between vanilla Teeworlds and DDRace: a vanilla server never has
// freeze/tele/DDRace tiles, so predicting them there (or predicting vanilla
// rules on a DDRace server) produces a mismatch against the authoritative
// snapshot. The config is derived from the game type (V10b).
type WorldConfig struct {
	IsVanilla bool
	IsDDRace  bool

	PredictWeapons bool // ballistic projectiles + rocket-jump impulses
	PredictFreeze  bool // freeze/deep-freeze tiles suppress tee control
	PredictTiles   bool // DDRace movement tiles (stoppers, tune, etc.)
	PredictDDRace  bool // DDRace-specific character rules
}

// DefaultWorldConfig is the vanilla configuration: weapons are predicted
// (rocket jumps work on every server), but no DDRace-only tile physics. A
// freshly created Core uses this so existing vanilla behaviour is unchanged.
func DefaultWorldConfig() WorldConfig {
	return WorldConfig{IsVanilla: true, PredictWeapons: true}
}

// DDRaceWorldConfig is the configuration for DDRace/DDNet servers: weapons plus
// freeze, tile, and DDRace character rules are all predicted.
func DDRaceWorldConfig() WorldConfig {
	return WorldConfig{
		IsDDRace:       true,
		PredictWeapons: true,
		PredictFreeze:  true,
		PredictTiles:   true,
		PredictDDRace:  true,
	}
}
