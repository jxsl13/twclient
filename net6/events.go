package net6

import (
	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// This file decodes vanilla/DDNet 0.6 server game messages into the unified
// packet events (V1, V17). Field layouts follow DDNet datasrc/network.py.

func (s *Session) emit(ev packet.Event) {
	packet.SendEvent(s.reader.eventCh, ev)
}

// processChat decodes Sv_Chat and splits it into chat / server-message /
// whisper events by m_Team and m_ClientId (V15).
func (s *Session) processChat(data []byte) {
	u := packer.NewUnpacker(data)
	team, err := u.NextInt()
	if err != nil {
		return
	}
	cid, err := u.NextInt()
	if err != nil {
		return
	}
	msg, err := u.NextString()
	if err != nil {
		return
	}

	switch {
	case team == TeamWhisperRecv:
		s.emit(packet.EventWhisper{FromID: cid, ToID: -1, Msg: msg})
	case team == TeamWhisperSend:
		s.emit(packet.EventWhisper{FromID: -1, ToID: cid, Msg: msg})
	case cid == -1:
		s.emit(packet.EventServerMsg{Msg: msg})
	default:
		s.emit(packet.EventChat{Team: team, ClientID: cid, Msg: msg})
	}
}

func (s *Session) processBroadcast(data []byte) {
	u := packer.NewUnpacker(data)
	text, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventBroadcast{Text: text})
}

func (s *Session) processMotd(data []byte) {
	u := packer.NewUnpacker(data)
	text, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventMotd{Text: text})
}

func (s *Session) processKillMsg(data []byte) {
	u := packer.NewUnpacker(data)
	killer, err := u.NextInt()
	if err != nil {
		return
	}
	victim, err := u.NextInt()
	if err != nil {
		return
	}
	weapon, err := u.NextInt()
	if err != nil {
		return
	}
	modeSpecial, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventKill{
		Killer:      killer,
		Victim:      victim,
		Weapon:      packet.Weapon(weapon),
		ModeSpecial: modeSpecial,
	})
}

func (s *Session) processSoundGlobal(data []byte) {
	u := packer.NewUnpacker(data)
	soundID, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventSoundGlobal{SoundID: soundID})
}

// processTuneParams reads all tuning values until the buffer is exhausted.
func (s *Session) processTuneParams(data []byte) {
	u := packer.NewUnpacker(data)
	var raw []int32
	for {
		v, err := u.NextInt()
		if err != nil {
			break
		}
		raw = append(raw, int32(v))
	}
	s.emit(packet.EventTuneParams{Raw: raw})
}

func (s *Session) processWeaponPickup(data []byte) {
	u := packer.NewUnpacker(data)
	weapon, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventWeaponPickup{Weapon: packet.Weapon(weapon)})
}

func (s *Session) processEmoticon(data []byte) {
	u := packer.NewUnpacker(data)
	cid, err := u.NextInt()
	if err != nil {
		return
	}
	emoticon, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventEmoticon{ClientID: cid, Emoticon: packet.Emoticon(emoticon)})
}

func (s *Session) processVoteSet(data []byte) {
	u := packer.NewUnpacker(data)
	timeout, err := u.NextInt()
	if err != nil {
		return
	}
	desc, err := u.NextString()
	if err != nil {
		return
	}
	reason, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventVoteSet{Timeout: timeout, Desc: desc, Reason: reason})
}

func (s *Session) processVoteStatus(data []byte) {
	u := packer.NewUnpacker(data)
	yes, err := u.NextInt()
	if err != nil {
		return
	}
	no, err := u.NextInt()
	if err != nil {
		return
	}
	pass, err := u.NextInt()
	if err != nil {
		return
	}
	total, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventVoteStatus{Yes: yes, No: no, Pass: pass, Total: total})
}

func (s *Session) processVoteOptionAdd(data []byte) {
	u := packer.NewUnpacker(data)
	desc, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventVoteOption{Op: packet.VoteOptionAdd, Desc: desc})
}

func (s *Session) processVoteOptionRemove(data []byte) {
	u := packer.NewUnpacker(data)
	desc, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventVoteOption{Op: packet.VoteOptionRemove, Desc: desc})
}

func (s *Session) processVoteClearOptions() {
	s.emit(packet.EventVoteOption{Op: packet.VoteOptionClear})
}

// processVoteOptionListAdd decodes a batch of vote options, emitting one add
// event per description.
func (s *Session) processVoteOptionListAdd(data []byte) {
	u := packer.NewUnpacker(data)
	n, err := u.NextInt()
	if err != nil {
		return
	}
	for range n {
		desc, err := u.NextString()
		if err != nil {
			return
		}
		s.emit(packet.EventVoteOption{Op: packet.VoteOptionAdd, Desc: desc})
	}
}

// --- system messages ---

func (s *Session) processRconLine(data []byte) {
	u := packer.NewUnpacker(data)
	line, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventRconLine{Line: line})
}

// processRconAuthStatus decodes NETMSG_RCON_AUTH_STATUS: m_Authed and an
// optional command-list flag. Level is reported as the authed value.
func (s *Session) processRconAuthStatus(data []byte) {
	u := packer.NewUnpacker(data)
	authed, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventRconAuth{Authed: authed != 0, Level: authed})
}

func (s *Session) processRconCmdAdd(data []byte) {
	u := packer.NewUnpacker(data)
	name, err := u.NextString()
	if err != nil {
		return
	}
	help, err := u.NextString()
	if err != nil {
		return
	}
	params, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventRconCmd{Op: packet.RconCmdAdd, Cmd: name, Help: help, Params: params})
}

func (s *Session) processRconCmdRem(data []byte) {
	u := packer.NewUnpacker(data)
	name, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventRconCmd{Op: packet.RconCmdRemove, Cmd: name})
}

func (s *Session) processServerError(data []byte) {
	u := packer.NewUnpacker(data)
	msg, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventServerError{Msg: msg})
}
