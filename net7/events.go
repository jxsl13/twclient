package net7

import (
	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// Decodes Teeworlds 0.7 server game messages into the SAME unified packet
// events as net6 (V17). Field layouts follow Teeworlds datasrc/network.py.
// Several things that are snapshot objects in 0.6 are messages in 0.7
// (ClientInfo, ClientDrop, GameInfo, SkinChange, Team) and are normalized to
// the same events here (V15a).

// 0.7 Sv_Chat m_Mode values (Enum CHAT).
const (
	chatModeNone    = 0
	chatModeAll     = 1
	chatModeTeam    = 2
	chatModeWhisper = 3
)

func (s *Session) emit(ev packet.Event) {
	packet.SendEvent(s.reader.eventCh, ev)
}

func (s *Session) processMotd(data []byte) {
	u := packer.NewUnpacker(data)
	if text, err := u.NextString(); err == nil {
		s.emit(packet.EventMotd{Text: text})
	}
}

func (s *Session) processBroadcast(data []byte) {
	u := packer.NewUnpacker(data)
	if text, err := u.NextString(); err == nil {
		s.emit(packet.EventBroadcast{Text: text})
	}
}

// processChat splits 0.7 Sv_Chat (mode, clientID, targetID, message) into the
// unified chat / server-message / whisper events (V15, V17).
func (s *Session) processChat(data []byte) {
	u := packer.NewUnpacker(data)
	mode, err := u.NextInt()
	if err != nil {
		return
	}
	cid, err := u.NextInt()
	if err != nil {
		return
	}
	target, err := u.NextInt()
	if err != nil {
		return
	}
	msg, err := u.NextString()
	if err != nil {
		return
	}

	switch {
	case mode == chatModeWhisper:
		s.emit(packet.EventWhisper{FromID: cid, ToID: target, Msg: msg})
	case cid < 0 || mode == chatModeNone:
		s.emit(packet.EventServerMsg{Msg: msg})
	default:
		team := 0
		if mode == chatModeTeam {
			team = 1
		}
		s.emit(packet.EventChat{Team: team, ClientID: cid, Msg: msg})
	}
}

func (s *Session) processTeam(data []byte) {
	u := packer.NewUnpacker(data)
	cid, err := u.NextInt()
	if err != nil {
		return
	}
	team, err := u.NextInt()
	if err != nil {
		return
	}
	silent, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventTeamSet{ClientID: cid, Team: team, Silent: silent != 0})
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
	s.emit(packet.EventKill{Killer: killer, Victim: victim, Weapon: packet.Weapon(weapon), ModeSpecial: modeSpecial})
}

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
	if weapon, err := u.NextInt(); err == nil {
		s.emit(packet.EventWeaponPickup{Weapon: packet.Weapon(weapon)})
	}
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

// processVoteSet decodes 0.7 Sv_VoteSet (clientID, type, timeout, desc, reason).
func (s *Session) processVoteSet(data []byte) {
	u := packer.NewUnpacker(data)
	if _, err := u.NextInt(); err != nil { // m_ClientID
		return
	}
	if _, err := u.NextInt(); err != nil { // m_Type
		return
	}
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
	if desc, err := u.NextString(); err == nil {
		s.emit(packet.EventVoteOption{Op: packet.VoteOptionAdd, Desc: desc})
	}
}

func (s *Session) processVoteOptionRemove(data []byte) {
	u := packer.NewUnpacker(data)
	if desc, err := u.NextString(); err == nil {
		s.emit(packet.EventVoteOption{Op: packet.VoteOptionRemove, Desc: desc})
	}
}

func (s *Session) processVoteClearOptions() {
	s.emit(packet.EventVoteOption{Op: packet.VoteOptionClear})
}

func (s *Session) processServerSettings(data []byte) {
	u := packer.NewUnpacker(data)
	kickVote, err := u.NextInt()
	if err != nil {
		return
	}
	kickMin, err := u.NextInt()
	if err != nil {
		return
	}
	specVote, err := u.NextInt()
	if err != nil {
		return
	}
	teamLock, err := u.NextInt()
	if err != nil {
		return
	}
	teamBalance, err := u.NextInt()
	if err != nil {
		return
	}
	playerSlots, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventServerSettings{
		KickVote:    kickVote != 0,
		KickMin:     kickMin,
		SpecVote:    specVote != 0,
		TeamLock:    teamLock != 0,
		TeamBalance: teamBalance != 0,
		PlayerSlots: playerSlots,
	})
}

// processClientInfo decodes 0.7 Sv_ClientInfo into EventPlayerJoin. The skin is
// reported as the body skin-part name (the first of six parts).
func (s *Session) processClientInfo(data []byte) {
	u := packer.NewUnpacker(data)
	cid, err := u.NextInt()
	if err != nil {
		return
	}
	if _, err := u.NextInt(); err != nil { // m_Local
		return
	}
	team, err := u.NextInt()
	if err != nil {
		return
	}
	name, err := u.NextString()
	if err != nil {
		return
	}
	clan, err := u.NextString()
	if err != nil {
		return
	}
	country, err := u.NextInt()
	if err != nil {
		return
	}
	// 6 skin-part names; keep the first (body).
	var skin string
	for i := range 6 {
		part, err := u.NextString()
		if err != nil {
			return
		}
		if i == 0 {
			skin = part
		}
	}
	s.emit(packet.EventPlayerJoin{
		ClientID: cid, Name: name, Clan: clan, Country: country, Skin: skin, Team: team,
	})
}

func (s *Session) processClientDrop(data []byte) {
	u := packer.NewUnpacker(data)
	cid, err := u.NextInt()
	if err != nil {
		return
	}
	reason, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventPlayerLeave{ClientID: cid, Reason: reason})
}

func (s *Session) processGameInfo(data []byte) {
	u := packer.NewUnpacker(data)
	gameFlags, err := u.NextInt()
	if err != nil {
		return
	}
	scoreLimit, err := u.NextInt()
	if err != nil {
		return
	}
	timeLimit, err := u.NextInt()
	if err != nil {
		return
	}
	s.emit(packet.EventGameInfo{GameFlags: gameFlags, ScoreLimit: scoreLimit, TimeLimit: timeLimit})
}

func (s *Session) processGameMsg(data []byte) {
	u := packer.NewUnpacker(data)
	var params []int32
	for {
		v, err := u.NextInt()
		if err != nil {
			break
		}
		params = append(params, int32(v))
	}
	id := 0
	if len(params) > 0 {
		id = int(params[0])
		params = params[1:]
	}
	s.emit(packet.EventGameMsg{GameMsgID: id, Params: params})
}

func (s *Session) processSkinChange(data []byte) {
	u := packer.NewUnpacker(data)
	cid, err := u.NextInt()
	if err != nil {
		return
	}
	skin, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventSkinChange{ClientID: cid, Skin: skin})
}

func (s *Session) processCommandInfo(data []byte) {
	u := packer.NewUnpacker(data)
	name, err := u.NextString()
	if err != nil {
		return
	}
	help, err := u.NextString()
	if err != nil {
		return
	}
	args, err := u.NextString()
	if err != nil {
		return
	}
	s.emit(packet.EventCommandInfo{Name: name, Help: help, ArgsFmt: args})
}

func (s *Session) processCommandInfoRemove(data []byte) {
	u := packer.NewUnpacker(data)
	if name, err := u.NextString(); err == nil {
		s.emit(packet.EventCommandInfoRemove{Name: name})
	}
}
