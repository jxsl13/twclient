// Package replay defines a common interface for providing pre-recorded
// player inputs from various Teeworlds/DDNet file formats (ghost files,
// demo files, teehistorian files).
//
// The primary interface is InputProvider, which yields (tick, packet.PlayerInput)
// pairs sequentially. Training code can use this to replay real player
// inputs instead of random actions.
package replay

import (
	"math"

	"github.com/jxsl13/twclient/packet"
)

// CharWeapon represents a weapon in character snapshot state (0-indexed).
type CharWeapon int

const (
	CharWeaponHammer  CharWeapon = 0
	CharWeaponGun     CharWeapon = 1
	CharWeaponShotgun CharWeapon = 2
	CharWeaponGrenade CharWeapon = 3
	CharWeaponLaser   CharWeapon = 4
	CharWeaponNinja   CharWeapon = 5
)

// ToInputWeapon converts a 0-indexed character weapon to the 1-indexed
// input WantedWeapon value.
func (w CharWeapon) ToInputWeapon() packet.Weapon {
	return packet.Weapon(w + 1)
}

// CharHookState represents the hook state in character snapshot data.
type CharHookState int

const (
	CharHookRetracted    CharHookState = -1
	CharHookIdle         CharHookState = 0
	CharHookRetractStart CharHookState = 1
	CharHookRetractEnd   CharHookState = 3
	CharHookFlying       CharHookState = 4
	CharHookGrabbed      CharHookState = 5
)

// Active reports whether the hook is in an active state (not idle or retracted).
func (h CharHookState) Active() bool {
	return h >= CharHookRetractStart
}

// InputFrame is a single frame of recorded player input with its tick.
// For ghost-derived frames, TargetX/TargetY store the expected world
// position at this tick (used for drift correction during replay).
type InputFrame struct {
	Tick  int
	Input packet.PlayerInput

	// Expected world position at this tick (from ghost CharacterFrame).
	// HasPos is false for formats that don't provide positions (e.g. pure input recordings).
	ExpectedX, ExpectedY int
	HasPos               bool

	// Raw ghost snapshot data for debug output and diagnostics.
	GhostVelX, GhostVelY   int
	GhostAngle             int
	GhostDirection         int
	GhostWeapon            CharWeapon
	GhostHookState         CharHookState
	GhostHookX, GhostHookY int
	GhostAttackTick        int
}

// CharacterFrame is a frame of character state from a ghost or demo snapshot.
type CharacterFrame struct {
	Tick       int
	X, Y       int
	VelX       int
	VelY       int
	Angle      int // atan2(TargetY, TargetX) * 256
	Direction  int
	Weapon     CharWeapon
	HookState  CharHookState
	HookX      int
	HookY      int
	AttackTick int
}

// InputProvider yields pre-recorded player inputs one tick at a time.
// Implementations wrap different file formats (ghost, demo, teehistorian).
//
// Call NextInput in a loop until it returns io.EOF.
type InputProvider interface {
	// NextInput returns the next recorded input frame.
	// Returns io.EOF when the recording is exhausted.
	NextInput() (InputFrame, error)

	// Info returns metadata about the recording.
	Info() RecordingInfo

	// Close releases resources.
	Close() error
}

// CharacterProvider yields character state frames (position, velocity, etc.).
// Ghost files store character state rather than raw inputs, so they implement
// this interface.
type CharacterProvider interface {
	// NextCharacter returns the next recorded character frame.
	// Returns io.EOF when the recording is exhausted.
	NextCharacter() (CharacterFrame, error)

	// Info returns metadata about the recording.
	Info() RecordingInfo

	// Close releases resources.
	Close() error
}

// RecordingFormat identifies the source file format of a recording.
type RecordingFormat string

const (
	FormatGhost        RecordingFormat = "ghost"
	FormatDemo         RecordingFormat = "demo"
	FormatTeehistorian RecordingFormat = "teehistorian"
)

// RecordingInfo contains metadata common to all recording formats.
type RecordingInfo struct {
	// Format describes the source format.
	Format RecordingFormat

	// Map is the map name this recording was made on.
	Map string

	// Player is the player name (if available).
	Player string

	// NumTicks is the total number of ticks (if known, 0 otherwise).
	NumTicks int

	// TimeCentis is the total time in centiseconds (if known, 0 otherwise).
	TimeCentis int

	// SelectedCID is the client ID selected for replay (server demos).
	// -1 for client demos and ghosts.
	SelectedCID int
}

// CharacterToInputAdapter converts a CharacterProvider into an InputProvider
// by computing input deltas from consecutive character frames.
//
// Ghost files store character state (position, hook, weapon, etc.) rather
// than raw inputs. This adapter derives plausible inputs by examining
// frame-to-frame deltas. Key derivation rules:
//
//   - Direction: sign of X position delta
//   - Jump: detected via sudden upward Y movement (ghost VelY is always 0,
//     so we use position deltas instead of velocity)
//   - Hook: active when HookState >= RETRACT_START; aim overridden toward hook pos
//   - Fire: tracked via a parity counter that flips when AttackTick changes
//   - Weapon: directly from the character Weapon field (0-indexed → 1-indexed)
//   - Aim: from the Angle field converted to (TargetX, TargetY) unit vector
type CharacterToInputAdapter struct {
	cp   CharacterProvider
	prev *CharacterFrame

	// fireCounter tracks the fire parity counter. The server uses
	// CountInput to detect new shots: bit 0 = fire held, upper bits
	// count state changes. We increment on each AttackTick change.
	fireCounter int

	// lastJump tracks whether we sent Jump=1 last frame. The server
	// requires a 0→1 transition to trigger a new jump, so we must
	// release between consecutive jump frames.
	lastJump bool
}

// NewCharacterToInputAdapter wraps a CharacterProvider with input derivation.
func NewCharacterToInputAdapter(cp CharacterProvider) *CharacterToInputAdapter {
	return &CharacterToInputAdapter{cp: cp}
}

func (a *CharacterToInputAdapter) NextInput() (InputFrame, error) {
	cur, err := a.cp.NextCharacter()
	if err != nil {
		return InputFrame{}, err
	}

	var input packet.PlayerInput

	// Convert ghost angle (atan2*256) to aim target vector.
	rads := float64(cur.Angle) / 256.0
	aimX := int(math.Round(math.Cos(rads) * 256))
	aimY := int(math.Round(math.Sin(rads) * 256))
	input.TargetX = aimX
	input.TargetY = aimY

	// --- Direction: use the ghost's stored Direction directly ---
	// The ghost file records m_Input.m_Direction from the server snapshot
	// (see DDNet SnapCharacter: pCharacter->m_Direction = m_Input.m_Direction).
	// This is the exact input the original player pressed. Using position
	// deltas instead would lose information when the tee is against a wall
	// (dx=0 but player is pressing into the wall) or being pushed by a hook.
	input.Direction = packet.Direction(cur.Direction)

	if a.prev != nil {
		// --- Jump detection via Y position delta ---
		// Ghost VelY is always 0 (DDNet design decision), so we cannot
		// use velocity. Instead we detect jumps from sudden upward Y
		// movement (negative Y delta = upward in Teeworlds coordinates).
		//
		// Ground jump impulse is ~13.2 units/tick, air jump ~12.0.
		// We use a threshold of 5 to catch jumps while avoiding noise
		// from normal falling/landing.
		dy := cur.Y - a.prev.Y
		wantsJump := dy < -5

		// The server requires a 0→1 transition on m_Jump to trigger a
		// new jump (see CCharacterCore::Tick). If we sent Jump=1 last
		// frame, we must send Jump=0 first to allow another jump.
		if wantsJump && !a.lastJump {
			input.Jump = packet.JumpOn
			a.lastJump = true
		} else if wantsJump && a.lastJump {
			// Still moving up — keep holding jump (server won't re-trigger
			// because bit 0 is already set, which is correct behavior
			// for holding through a single jump arc).
			input.Jump = packet.JumpOn
		} else {
			// Not jumping or descending — release so next jump can trigger
			input.Jump = packet.JumpOff
			a.lastJump = false
		}

		// --- Hook state — override aim toward hook target when active ---
		if cur.HookState.Active() {
			input.Hook = packet.HookOn
			input.TargetX = cur.HookX - cur.X
			input.TargetY = cur.HookY - cur.Y
		}

		// --- Fire detection via attack tick parity counter ---
		// The server detects new shots via CountInput which compares
		// the previous and current fire counter values. We maintain a
		// monotonically increasing counter and set bit 0 to indicate
		// fire-held state.
		if cur.AttackTick != a.prev.AttackTick {
			// New shot detected — increment counter and set fire bit
			a.fireCounter = (a.fireCounter + 1) | 1 // odd = fire pressed
			input.Fire = packet.FireCount(a.fireCounter)
		} else if a.fireCounter&1 != 0 {
			// No new shot — release fire (clear bit 0)
			a.fireCounter = (a.fireCounter + 1) & ^1 // even = fire released
			input.Fire = packet.FireCount(a.fireCounter)
		}
	}

	input.PlayerFlags = packet.PlayerFlagPlaying
	input.WantedWeapon = cur.Weapon.ToInputWeapon()

	frame := InputFrame{
		Tick:            cur.Tick,
		Input:           input,
		ExpectedX:       cur.X,
		ExpectedY:       cur.Y,
		HasPos:          true,
		GhostVelX:       cur.VelX,
		GhostVelY:       cur.VelY,
		GhostAngle:      cur.Angle,
		GhostDirection:  cur.Direction,
		GhostWeapon:     cur.Weapon,
		GhostHookState:  cur.HookState,
		GhostHookX:      cur.HookX,
		GhostHookY:      cur.HookY,
		GhostAttackTick: cur.AttackTick,
	}
	prev := cur
	a.prev = &prev
	return frame, nil
}

func (a *CharacterToInputAdapter) Info() RecordingInfo {
	return a.cp.Info()
}

func (a *CharacterToInputAdapter) Close() error {
	return a.cp.Close()
}

var _ InputProvider = (*CharacterToInputAdapter)(nil)
