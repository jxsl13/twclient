package packet

import "fmt"

// Direction represents the horizontal movement direction.
type Direction int

// Direction values for PlayerInput.Direction (DDNet/TW `m_Direction`): move
// left, none, or right.
const (
	DirLeft  Direction = -1
	DirNone  Direction = 0
	DirRight Direction = 1
)

// SetDirection validates and sets a direction from a raw integer.
func (p *PlayerInput) SetDirection(v int) error {
	if v < -1 || v > 1 {
		return fmt.Errorf("direction %d out of range [-1, 1]", v)
	}
	p.Direction = Direction(v)
	return nil
}

// JumpState represents whether the jump key is pressed.
type JumpState int

// JumpState values for PlayerInput.Jump (DDNet/TW `m_Jump`): key released or held.
const (
	JumpOff JumpState = 0
	JumpOn  JumpState = 1
)

// SetJump validates and sets the jump state from a raw integer.
func (p *PlayerInput) SetJump(v int) error {
	if v < 0 || v > 1 {
		return fmt.Errorf("jump %d out of range [0, 1]", v)
	}
	p.Jump = JumpState(v)
	return nil
}

// HookState represents whether the hook key is pressed.
type HookState int

// HookState values for PlayerInput.Hook (DDNet/TW `m_Hook`): key released or held.
const (
	HookOff HookState = 0
	HookOn  HookState = 1
)

// SetHook validates and sets the hook state from a raw integer.
func (p *PlayerInput) SetHook(v int) error {
	if v < 0 || v > 1 {
		return fmt.Errorf("hook %d out of range [0, 1]", v)
	}
	p.Hook = HookState(v)
	return nil
}

// PlayerFlag is a bitmask of player state flags.
type PlayerFlag int

// PlayerFlag bits for PlayerInput.PlayerFlags (DDNet/TW `PLAYERFLAG_*`,
// datasrc/network.py): OR them together (playing, in menu, chatting, scoreboard
// open, aiming).
const (
	PlayerFlagPlaying    PlayerFlag = 1
	PlayerFlagInMenu     PlayerFlag = 2
	PlayerFlagChatting   PlayerFlag = 4
	PlayerFlagScoreboard PlayerFlag = 8
	PlayerFlagAim        PlayerFlag = 16
)

const playerFlagAll = PlayerFlagPlaying | PlayerFlagInMenu | PlayerFlagChatting | PlayerFlagScoreboard | PlayerFlagAim

// SetPlayerFlags validates and sets player flags from a raw integer.
func (p *PlayerInput) SetPlayerFlags(v int) error {
	if v < 0 || PlayerFlag(v) & ^playerFlagAll != 0 {
		return fmt.Errorf("player flags %d contain invalid bits", v)
	}
	p.PlayerFlags = PlayerFlag(v)
	return nil
}

// Weapon represents a weapon selection in the WantedWeapon input field.
// Values are 1-indexed (0 = no weapon change, 1-6 = specific weapon).
type Weapon int

// Weapon values for PlayerInput.WantedWeapon (DDNet/TW `WEAPON_*`): 0 = no
// change, 1-6 select hammer/gun/shotgun/grenade/laser/ninja.
const (
	WeaponNone    Weapon = 0
	WeaponHammer  Weapon = 1
	WeaponGun     Weapon = 2
	WeaponShotgun Weapon = 3
	WeaponGrenade Weapon = 4
	WeaponLaser   Weapon = 5
	WeaponNinja   Weapon = 6
)

// SetWantedWeapon validates and sets the wanted weapon from a raw integer.
func (p *PlayerInput) SetWantedWeapon(v int) error {
	if v < 0 || v > 6 {
		return fmt.Errorf("wanted weapon %d out of range [0, 6]", v)
	}
	p.WantedWeapon = Weapon(v)
	return nil
}

// WeaponDelta represents a weapon switch delta (0 = no switch, 1 = switch).
type WeaponDelta int

// WeaponDelta values for PlayerInput.NextWeapon/PrevWeapon: no switch, or
// step to the adjacent weapon (DDNet/TW `m_NextWeapon`/`m_PrevWeapon`).
const (
	WeaponDeltaNone   WeaponDelta = 0
	WeaponDeltaSwitch WeaponDelta = 1
)

// SetNextWeapon validates and sets the next-weapon delta from a raw integer.
func (p *PlayerInput) SetNextWeapon(v int) error {
	if v < 0 || v > 1 {
		return fmt.Errorf("next weapon %d out of range [0, 1]", v)
	}
	p.NextWeapon = WeaponDelta(v)
	return nil
}

// SetPrevWeapon validates and sets the prev-weapon delta from a raw integer.
func (p *PlayerInput) SetPrevWeapon(v int) error {
	if v < 0 || v > 1 {
		return fmt.Errorf("prev weapon %d out of range [0, 1]", v)
	}
	p.PrevWeapon = WeaponDelta(v)
	return nil
}

// FireCount represents the fire counter (incremented each tick while fire is held).
type FireCount int

// SetFire validates and sets the fire counter from a raw integer.
func (p *PlayerInput) SetFire(v int) error {
	if v < 0 {
		return fmt.Errorf("fire count %d must be non-negative", v)
	}
	p.Fire = FireCount(v)
	return nil
}

// SetTarget sets the aim target coordinates. Any integer values are valid.
func (p *PlayerInput) SetTarget(x, y int) {
	p.TargetX = x
	p.TargetY = y
}

// Emoticon represents an emoticon ID shared by both 0.6 and 0.7 protocols.
// Values range from 0 to 15 (NUM_EMOTICONS-1).
// Names reference the texture atlas entries defined in content.py.
type Emoticon int

// Emoticon values (DDNet/TW `EMOTICON_*`, datasrc/content.py): the 16 emote
// atlas entries shared by 0.6 and 0.7; NumEmoticons is their count.
const (
	EmoticonOop      Emoticon = 0  // "oop" / surprise
	EmoticonExclaim  Emoticon = 1  // exclamation / alert
	EmoticonHearts   Emoticon = 2  // hearts
	EmoticonDrop     Emoticon = 3  // tear drop
	EmoticonDotdot   Emoticon = 4  // "..." / ellipsis
	EmoticonMusic    Emoticon = 5  // music note
	EmoticonSorry    Emoticon = 6  // sorry
	EmoticonGhost    Emoticon = 7  // ghost
	EmoticonSushi    Emoticon = 8  // sushi / annoyed
	EmoticonSplattee Emoticon = 9  // splattee / angry
	EmoticonDeviltee Emoticon = 10 // deviltee
	EmoticonZomg     Emoticon = 11 // zomg / swearing
	EmoticonZzz      Emoticon = 12 // zzZ / sleeping
	EmoticonWtf      Emoticon = 13 // WTF
	EmoticonEyes     Emoticon = 14 // eyes / happy
	EmoticonQuestion Emoticon = 15 // "??" / question

	NumEmoticons = 16
)

// InputFields is the number of int32 fields in CNetObj_PlayerInput.
const InputFields = 10

// EmptyInputSize is the byte size of a zero-valued CNetObj_PlayerInput
// as stored on the server (InputFields × 4 bytes).
const EmptyInputSize = InputFields * 4

// EmptyInput is a pre-built zero-input payload (10 fields encoded as
// varint 0). Used for snap acks when no real input is available.
var EmptyInput = func() []byte {
	// 0-initialized
	p := make([]byte, InputFields)
	return p
}()

// PlayerInput represents the client input sent to the server (CNetObj_PlayerInput).
// All fields use typed values — use the Set* methods to assign from raw integers
// with validation, or set the typed values directly.
type PlayerInput struct {
	Direction    Direction   // DirLeft (-1), DirNone (0), DirRight (1)
	TargetX      int         // Aim target X (relative to player)
	TargetY      int         // Aim target Y (relative to player)
	Jump         JumpState   // JumpOff (0), JumpOn (1)
	Fire         FireCount   // Incremented each tick while fire is held
	Hook         HookState   // HookOff (0), HookOn (1)
	PlayerFlags  PlayerFlag  // Bitmask: PlayerFlagPlaying | PlayerFlagChatting | ...
	WantedWeapon Weapon      // WeaponNone (0) through WeaponNinja (6)
	NextWeapon   WeaponDelta // WeaponDeltaNone (0), WeaponDeltaSwitch (1)
	PrevWeapon   WeaponDelta // WeaponDeltaNone (0), WeaponDeltaSwitch (1)
}

// NewPlayerInputFromRaw creates a PlayerInput from 10 raw integer fields
// in protocol order. Returns an error if any value is out of range.
func NewPlayerInputFromRaw(fields [10]int) (PlayerInput, error) {
	var p PlayerInput
	if err := p.SetDirection(fields[0]); err != nil {
		return PlayerInput{}, err
	}
	p.SetTarget(fields[1], fields[2])
	if err := p.SetJump(fields[3]); err != nil {
		return PlayerInput{}, err
	}
	if err := p.SetFire(fields[4]); err != nil {
		return PlayerInput{}, err
	}
	if err := p.SetHook(fields[5]); err != nil {
		return PlayerInput{}, err
	}
	if err := p.SetPlayerFlags(fields[6]); err != nil {
		return PlayerInput{}, err
	}
	if err := p.SetWantedWeapon(fields[7]); err != nil {
		return PlayerInput{}, err
	}
	if err := p.SetNextWeapon(fields[8]); err != nil {
		return PlayerInput{}, err
	}
	if err := p.SetPrevWeapon(fields[9]); err != nil {
		return PlayerInput{}, err
	}
	return p, nil
}

// UnsafePlayerInputFromRaw creates a PlayerInput from 10 raw integer fields
// without validation. Use only for trusted data (e.g. parsed from recordings).
func UnsafePlayerInputFromRaw(fields [10]int) PlayerInput {
	return PlayerInput{
		Direction:    Direction(fields[0]),
		TargetX:      fields[1],
		TargetY:      fields[2],
		Jump:         JumpState(fields[3]),
		Fire:         FireCount(fields[4]),
		Hook:         HookState(fields[5]),
		PlayerFlags:  PlayerFlag(fields[6]),
		WantedWeapon: Weapon(fields[7]),
		NextWeapon:   WeaponDelta(fields[8]),
		PrevWeapon:   WeaponDelta(fields[9]),
	}
}

// Raw returns the 10 integer fields in protocol order.
func (p *PlayerInput) Raw() [10]int {
	return [10]int{
		int(p.Direction),
		p.TargetX,
		p.TargetY,
		int(p.Jump),
		int(p.Fire),
		int(p.Hook),
		int(p.PlayerFlags),
		int(p.WantedWeapon),
		int(p.NextWeapon),
		int(p.PrevWeapon),
	}
}
