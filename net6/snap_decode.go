package net6

import "github.com/jxsl13/twclient/packet"

// DecodeSnap translates a raw 0.6 snapshot into the canonical, protocol-neutral
// object set (packet.SnapObjects, V112/V113). The 0.6 wire layout — net6 Obj*
// type ids with positional int fields — is resolved here so consumers read only
// the shared packet types, never a net6 id or field index (the snapshot analogue
// of the protocol-unified message events).
//
// Every field read is guarded by the matching net6.Size* field-count constant so
// a short or malformed item is skipped rather than panicking (V70). Unknown or
// 0.6-only/ext type ids are ignored.
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
				PlayerFlags:  f[15],
				Health:       f[16],
				Armor:        f[17],
				AmmoCount:    f[18],
				Weapon:       f[19],
				Emote:        f[20],
				AttackTick:   f[21],
			}

		case ObjPlayerInfo:
			if len(f) < SizePlayerInfo {
				continue
			}
			cid := f[1]
			out.Players[cid] = packet.Player{
				ClientID: cid,
				Local:    f[0] != 0,
				Team:     f[2],
				Score:    f[3],
				Latency:  f[4],
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
			out.Pickups = append(out.Pickups, packet.Pickup{
				X:       f[0],
				Y:       f[1],
				Type:    f[2],
				Subtype: f[3],
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

		case ObjGameInfo:
			if len(f) < SizeGameInfo {
				continue
			}
			// 0.6 ObjGameInfo: {GameFlags, GameStateFlags, RoundStartTick,
			// WarmupTimer, ScoreLimit, TimeLimit, RoundNum, RoundCurrent}.
			// GameStateEndTick has no 0.6 equivalent and stays zero (V113).
			out.GameInfo = packet.GameInfo{
				GameFlags:      f[0],
				GameStateFlags: f[1],
				RoundStartTick: f[2],
				WarmupTimer:    f[3],
				ScoreLimit:     f[4],
				TimeLimit:      f[5],
				RoundNum:       f[6],
				RoundCurrent:   f[7],
			}
			out.HasGameInfo = true

		case ObjGameData:
			if len(f) < SizeGameData {
				continue
			}
			out.GameData = packet.GameData{
				TeamscoreRed:    f[0],
				TeamscoreBlue:   f[1],
				FlagCarrierRed:  f[2],
				FlagCarrierBlue: f[3],
			}
			out.HasGameData = true

		case ObjSpectatorInfo:
			if len(f) < SizeSpectatorInfo {
				continue
			}
			out.Spectator = packet.SpectatorInfo{
				SpectatorID: f[0],
				X:           f[1],
				Y:           f[2],
			}
			out.HasSpectator = true

		// Transient one-tick events (stateless → decoded here, not in the
		// client) → protocol-unified packet.Event values (V115).
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
		case ObjDamageIndicator:
			if len(f) >= SizeDamageIndicator {
				out.Events = append(out.Events, packet.EventDamageInd{X: f[0], Y: f[1], Angle: f[2]})
			}

		default:
			// Unknown / 0.6-only / ext-object type ids are ignored.
		}
	}

	return out
}
