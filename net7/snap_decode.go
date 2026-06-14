package net7

import "github.com/jxsl13/twclient/packet"

// 0.7 PlayerInfo (CNetObj_PlayerInfo.m_PlayerFlags) has no LOCAL bit — the local
// player is identified by the snapshot item id matching the connection's own
// client id, which a stateless decoder does not know, so Player.Local is left
// false here (V113).

// 0.7 pickup type ids (CNetObj_Pickup.m_Type, protocol7 PICKUP_*).
const (
	pickup7Health  = 0
	pickup7Armor   = 1
	pickup7Grenade = 2
	pickup7Shotgun = 3
	pickup7Laser   = 4
	pickup7Ninja   = 5
	pickup7Gun     = 6
	pickup7Hammer  = 7
)

// Canonical (0.6-semantics) pickup Type/Subtype values. packet.Pickup carries
// the 0.6 split: Type is a POWERUP_* kind and Subtype is a WEAPON_* index, the
// SAME representation the net6 decoder emits, so consumers stay protocol-neutral.
const (
	powerupHealth = 0
	powerupArmor  = 1
	powerupWeapon = 2
	powerupNinja  = 3

	weaponHammer  = 0
	weaponGun     = 1
	weaponShotgun = 2
	weaponGrenade = 3
	weaponLaser   = 4
	weaponNinja   = 5
)

// 0.7 spectator modes (CNetObj_SpectatorInfo.m_SpecMode, protocol7 SPEC_*).
const (
	spec7Freeview = 0
)

// specFreeview6 is the canonical (0.6) sentinel SpectatorID for free-view; 0.7
// signals it via m_SpecMode==SPEC_FREEVIEW instead, normalized here (V113).
const specFreeview6 = -1

// pickupType7ToCanonical maps a 0.7 pickup type id to the canonical 0.6
// Type/Subtype split, mirroring DDNet's PickupType_SevenToSix.
func pickupType7ToCanonical(type7 int) (typ, subtype int) {
	switch type7 {
	case pickup7Health:
		return powerupHealth, 0
	case pickup7Armor:
		return powerupArmor, 0
	case pickup7Grenade:
		return powerupWeapon, weaponGrenade
	case pickup7Shotgun:
		return powerupWeapon, weaponShotgun
	case pickup7Laser:
		return powerupWeapon, weaponLaser
	case pickup7Gun:
		return powerupWeapon, weaponGun
	case pickup7Hammer:
		return powerupWeapon, weaponHammer
	case pickup7Ninja:
		return powerupNinja, weaponNinja
	default:
		return powerupWeapon, 0
	}
}

// DecodeSnap translates a raw 0.7 snapshot into the canonical, protocol-neutral
// object set (packet.SnapObjects, V112/V113). The 0.7 wire layout — net7 Obj*
// type ids with positional int fields (CNetObj_* member order from the teeworlds
// 0.7 generated protocol) — is resolved here so consumers read only the shared
// packet types, never a net7 id or field index. This is the 0.7 sibling of
// net6.DecodeSnap and the snapshot analogue of the protocol-unified message
// events. Field assignments mirror DDNet's sixup_translate_snapshot.cpp.
//
// Every field read is guarded by the matching net7.Size* field-count constant so
// a short or malformed item is skipped rather than panicking (V70). Unknown,
// event, or 0.7-only/ext type ids are ignored.
//
// 0.7 specifics: PlayerInfo carries the client id as the snapshot ITEM ID (not a
// field) and has no Team (delivered via Sv_Team) → Team stays 0. GameData is
// split into GameData (game state → GameInfo), GameDataTeam (team scores) and
// GameDataFlag (flag carriers), merged here. Character has no PlayerFlags on the
// wire (its 7th tail field is m_TriggeredEvents) → Character.PlayerFlags stays 0.
func DecodeSnap(snap *packet.Snapshot) packet.SnapObjects {
	out := packet.SnapObjects{
		Tick:       snap.Tick,
		Characters: make(map[int]packet.Character),
		Players:    make(map[int]packet.Player),
	}

	for _, it := range snap.Items {
		f := it.Fields
		switch it.TypeID {
		case ObjCharacter:
			if len(f) < SizeCharacter {
				continue
			}
			// CharacterCore (15 ints) is identical to 0.6 and maps positionally;
			// the 0.7 tail is Health, Armor, AmmoCount, Weapon, Emote, AttackTick,
			// TriggeredEvents (f[21], ignored). 0.7 Character has NO PlayerFlags
			// field, so PlayerFlags stays 0 (V113).
			out.Characters[it.ID] = packet.Character{
				Tick:         f[0],
				X:            f[1],
				Y:            f[2],
				VelX:         f[3],
				VelY:         f[4],
				Angle:        f[5],
				Direction:    f[6],
				Jumped:       f[7],
				HookedPlayer: f[8],
				HookState:    f[9],
				HookTick:     f[10],
				HookX:        f[11],
				HookY:        f[12],
				HookDx:       f[13],
				HookDy:       f[14],
				Health:       f[15],
				Armor:        f[16],
				AmmoCount:    f[17],
				Weapon:       f[18],
				Emote:        f[19],
				AttackTick:   f[20],
			}

		case ObjPlayerInfo:
			if len(f) < SizePlayerInfo {
				continue
			}
			// 0.7 PlayerInfo = {m_PlayerFlags, m_Score, m_Latency}; the client id
			// is the snapshot item id. Team is not in 0.7 PlayerInfo (Sv_Team) →
			// 0. Local cannot be derived statelessly → false (V113).
			out.Players[it.ID] = packet.Player{
				ClientID:    it.ID,
				Local:       false,
				Team:        0,
				Score:       f[1],
				Latency:     f[2],
				PlayerFlags: f[0], // 0.7 keeps PLAYERFLAG_* here, not in Character
			}

		case ObjProjectile:
			if len(f) < SizeProjectile {
				continue
			}
			out.Projectiles = append(out.Projectiles, packet.Projectile{
				ID:        it.ID,
				X:         f[0],
				Y:         f[1],
				VelX:      f[2],
				VelY:      f[3],
				Type:      packet.Weapon(f[4]),
				StartTick: f[5],
			})

		case ObjLaser:
			if len(f) < SizeLaser {
				continue
			}
			out.Lasers = append(out.Lasers, packet.Laser{
				ID:        it.ID,
				X:         f[0],
				Y:         f[1],
				FromX:     f[2],
				FromY:     f[3],
				StartTick: f[4],
			})

		case ObjPickup:
			if len(f) < SizePickup {
				continue
			}
			// 0.7 Pickup = {X, Y, Type}; the 0.6/canonical split needs Type +
			// Subtype, derived here (DDNet PickupType_SevenToSix).
			typ, subtype := pickupType7ToCanonical(f[2])
			out.Pickups = append(out.Pickups, packet.Pickup{
				X:       f[0],
				Y:       f[1],
				Type:    typ,
				Subtype: subtype,
			})

		case ObjFlag:
			if len(f) < SizeFlag {
				continue
			}
			out.Flags = append(out.Flags, packet.Flag{
				X:    f[0],
				Y:    f[1],
				Team: f[2],
			})

		case ObjGameData:
			if len(f) < SizeGameData {
				continue
			}
			// 0.7 GameData = {m_RoundStartTick, m_GameStateFlags, m_GameStateEndTick}
			// → canonical GameInfo game-state fields. GameFlags/ScoreLimit/TimeLimit
			// arrive separately (Sv_GameInfo / De_GameInfo) → left 0 (V113).
			out.GameInfo = packet.GameInfo{
				RoundStartTick:   f[0],
				GameStateFlags:   f[1],
				GameStateEndTick: f[2],
			}
			out.HasGameInfo = true

		case ObjGameDataTeam:
			if len(f) < SizeGameDataTeam {
				continue
			}
			// 0.7 splits team scores (here) and flag carriers (GameDataFlag) into
			// two objects; both merge into the single canonical GameData.
			out.GameData.TeamscoreRed = f[0]
			out.GameData.TeamscoreBlue = f[1]

		case ObjGameDataFlag:
			if len(f) < SizeGameDataFlag {
				continue
			}
			// {m_FlagCarrierRed, m_FlagCarrierBlue, m_FlagDropTickRed,
			// m_FlagDropTickBlue}; drop ticks have no canonical field.
			out.GameData.FlagCarrierRed = f[0]
			out.GameData.FlagCarrierBlue = f[1]
			out.HasGameData = true

		case ObjSpectatorInfo:
			if len(f) < SizeSpectatorInfo {
				continue
			}
			// 0.7 SpectatorInfo = {m_SpecMode, m_SpectatorID, m_X, m_Y}. Canonical
			// free-view is signalled by SpectatorID==-1 (0.6 SPEC_FREEVIEW); 0.7
			// signals it via m_SpecMode==SPEC_FREEVIEW, normalized here (V113).
			specID := f[1]
			if f[0] == spec7Freeview {
				specID = specFreeview6
			}
			out.Spectator = packet.SpectatorInfo{
				SpectatorID: specID,
				X:           f[2],
				Y:           f[3],
			}
			out.HasSpectator = true

		// Transient one-tick events (0.7 CNetEvent_* extend Common{X,Y}) →
		// protocol-unified packet.Event values (V115), same as net6.
		case ObjExplosion:
			if len(f) >= SizeExplosion {
				out.Events = append(out.Events, packet.EventExplosion{X: f[0], Y: f[1]})
			}
		case ObjSpawn:
			if len(f) >= SizeSpawn {
				out.Events = append(out.Events, packet.EventSpawn{X: f[0], Y: f[1]})
			}
		case ObjHammerHit:
			if len(f) >= SizeHammerHit {
				out.Events = append(out.Events, packet.EventHammerHit{X: f[0], Y: f[1]})
			}
		case ObjDeath:
			if len(f) >= SizeDeath {
				out.Events = append(out.Events, packet.EventDeath{X: f[0], Y: f[1], ClientID: f[2]})
			}
		case ObjSoundWorld:
			if len(f) >= SizeSoundWorld {
				out.Events = append(out.Events, packet.EventSoundWorld{X: f[0], Y: f[1], SoundID: f[2]})
			}
		case ObjDamage:
			// 0.7 CNetEvent_Damage = Common{X,Y} + {ClientId, Angle, Health,
			// Armor, Self}; map to the canonical DamageInd (X,Y,Angle).
			if len(f) >= SizeDamage {
				out.Events = append(out.Events, packet.EventDamageInd{X: f[0], Y: f[1], Angle: f[3]})
			}

		default:
			// Unknown / 0.7-only / ext-object type ids are ignored.
		}
	}

	return out
}
