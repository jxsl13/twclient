package net6

import (
	"github.com/jxsl13/twclient/packer"
	"github.com/jxsl13/twclient/packet"
)

// DDNet UUID-based extended messages (NETMSG_EX). Wire format after the EX
// system message id: 16-byte UUID identifying the message, then its payload.
// UUIDs are computed from the registered names in DDNet datasrc/network.py.

var (
	uuidSvTeamsState         = packer.CalculateUUID("teamsstate@netmsg.ddnet.tw")
	uuidSvDDRaceTime         = packer.CalculateUUID("ddrace-time@netmsg.ddnet.tw")
	uuidSvRecord             = packer.CalculateUUID("record@netmsg.ddnet.tw")
	uuidSvKillMsgTeam        = packer.CalculateUUID("killmsgteam@netmsg.ddnet.tw")
	uuidSvYourVote           = packer.CalculateUUID("yourvote@netmsg.ddnet.org")
	uuidSvRaceFinish         = packer.CalculateUUID("racefinish@netmsg.ddnet.org")
	uuidSvCommandInfo        = packer.CalculateUUID("commandinfo@netmsg.ddnet.org")
	uuidSvCommandInfoRemove  = packer.CalculateUUID("commandinfo-remove@netmsg.ddnet.org")
	uuidSvChangeInfoCooldown = packer.CalculateUUID("change-info-cooldown@netmsg.ddnet.org")
	uuidSvMapSoundGlobal     = packer.CalculateUUID("map-sound-global@netmsg.ddnet.org")
)

// processEx decodes a NETMSG_EX payload: a 16-byte UUID followed by the inner
// message. Unknown UUIDs are ignored.
func (s *Session) processEx(data []byte) {
	if len(data) < 16 {
		return
	}
	var uuid [16]byte
	copy(uuid[:], data[:16])
	body := data[16:]

	switch uuid {
	case uuidSvTeamsState:
		s.processExTeamsState(body)
	case uuidSvDDRaceTime:
		s.processDDRaceTime(body)
	case uuidSvRecord:
		s.processRecord(body)
	case uuidSvKillMsgTeam:
		s.processExKillMsgTeam(body)
	case uuidSvYourVote:
		s.processExYourVote(body)
	case uuidSvRaceFinish:
		s.processExRaceFinish(body)
	case uuidSvCommandInfo:
		s.processExCommandInfo(body)
	case uuidSvCommandInfoRemove:
		s.processExCommandInfoRemove(body)
	case uuidSvChangeInfoCooldown:
		s.processExChangeInfoCooldown(body)
	case uuidSvMapSoundGlobal:
		s.processExMapSoundGlobal(body)
	}
}

// processExTeamsState reads one ddrace-team value per client (raw ints) and
// reports the non-zero memberships.
func (s *Session) processExTeamsState(data []byte) {
	u := packer.NewUnpacker(data)
	team := make(map[int]int)
	for cid := 0; ; cid++ {
		v, err := u.GetInt()
		if err != nil {
			break
		}
		if v != 0 {
			team[cid] = v
		}
	}
	s.emit(packet.EventTeamsState{Team: team})
}

func (s *Session) processExKillMsgTeam(data []byte) {
	u := packer.NewUnpacker(data)
	team, err := u.GetInt()
	if err != nil {
		return
	}
	first, err := u.GetInt()
	if err != nil {
		return
	}
	s.emit(packet.EventKillMsgTeam{Team: team, First: first})
}

func (s *Session) processExYourVote(data []byte) {
	u := packer.NewUnpacker(data)
	voted, err := u.GetInt()
	if err != nil {
		return
	}
	s.emit(packet.EventYourVote{Voted: voted})
}

func (s *Session) processExRaceFinish(data []byte) {
	u := packer.NewUnpacker(data)
	if _, err := u.GetInt(); err != nil { // m_ClientId
		return
	}
	timeCentis, err := u.GetInt()
	if err != nil {
		return
	}
	s.emit(packet.EventRaceFinish{TimeCentis: timeCentis, Finish: true})
}

func (s *Session) processExCommandInfo(data []byte) {
	u := packer.NewUnpacker(data)
	name, err := u.GetString()
	if err != nil {
		return
	}
	args, err := u.GetString()
	if err != nil {
		return
	}
	help, err := u.GetString()
	if err != nil {
		return
	}
	s.emit(packet.EventCommandInfo{Name: name, ArgsFmt: args, Help: help})
}

func (s *Session) processExCommandInfoRemove(data []byte) {
	u := packer.NewUnpacker(data)
	name, err := u.GetString()
	if err != nil {
		return
	}
	s.emit(packet.EventCommandInfoRemove{Name: name})
}

func (s *Session) processExChangeInfoCooldown(data []byte) {
	u := packer.NewUnpacker(data)
	waitUntil, err := u.GetInt()
	if err != nil {
		return
	}
	s.emit(packet.EventChangeInfoCooldown{WaitUntilTick: waitUntil})
}

func (s *Session) processExMapSoundGlobal(data []byte) {
	u := packer.NewUnpacker(data)
	soundID, err := u.GetInt()
	if err != nil {
		return
	}
	s.emit(packet.EventMapSoundGlobal{SoundID: soundID})
}
