// Package net7 defines constants and message types for the Teeworlds 0.7 protocol.
package net7

const (
	// MaxClients is the maximum player slots a 0.7 server exposes.
	MaxClients = 64
	// NetVersion is the 0.7 network version string sent in the INFO message; it
	// must match the server's expected version (teeworlds `GAME_NETVERSION`).
	NetVersion = "0.7 802f1be60a05665f"
	// ClientVersion is the 0.7 client version advertised at login (CLIENT_VERSION7).
	ClientVersion = 0x0705

	// Split is the chunk header split parameter for 0.7.
	// Size uses 6+6=12 bits, sequence uses 2+8=10 bits.
	Split = 6
)

// Control message IDs.
const (
	MsgCtrlKeepAlive = 0x00
	MsgCtrlConnect   = 0x01
	MsgCtrlAccept    = 0x02
	MsgCtrlClose     = 0x04
	MsgCtrlToken     = 0x05
)

// System message IDs.
const (
	// MsgSysEx (NETMSG_EX) carries UUID-based extended messages. DDNet sends them
	// to sixup clients too — incl. the capabilities@ddnet.tw message (B20) — as
	// id 0 + 16-byte UUID + body (CServer::RepackMsg, server.cpp).
	MsgSysEx              = 0
	MsgSysInfo            = 1
	MsgSysMapChange       = 2
	MsgSysMapData         = 3
	MsgSysServerInfo      = 4
	MsgSysConReady        = 5
	MsgSysSnap            = 6
	MsgSysSnapEmpty       = 7
	MsgSysSnapSingle      = 8
	MsgSysSnapSmall       = 9
	MsgSysInputTiming     = 10
	MsgSysRconAuthOn      = 11
	MsgSysRconAuthOff     = 12
	MsgSysRconLine        = 13
	MsgSysRconCmdAdd      = 14
	MsgSysRconCmdRem      = 15
	MsgSysAuthChallenge   = 16
	MsgSysAuthResult      = 17
	MsgSysReady           = 18
	MsgSysEnterGame       = 19
	MsgSysInput           = 20
	MsgSysRconCmd         = 21
	MsgSysRconAuth        = 22
	MsgSysRequestMapData  = 23
	MsgSysAuthStart       = 24
	MsgSysAuthResponse    = 25
	MsgSysPing            = 26
	MsgSysPingReply       = 27
	MsgSysError           = 28
	MsgSysMaplistEntryAdd = 29
	MsgSysMaplistEntryRem = 30
)

// Game message IDs.
const (
	MsgGameSvMotd              = 1
	MsgGameSvBroadcast         = 2
	MsgGameSvChat              = 3
	MsgGameSvTeam              = 4
	MsgGameSvKillMsg           = 5
	MsgGameSvTuneParams        = 6
	MsgGameSvExtraProjectile   = 7
	MsgGameSvReadyToEnter      = 8
	MsgGameSvWeaponPickup      = 9
	MsgGameSvEmoticon          = 10
	MsgGameSvVoteClearOptions  = 11
	MsgGameSvVoteOptionListAdd = 12
	MsgGameSvVoteOptionAdd     = 13
	MsgGameSvVoteOptionRemove  = 14
	MsgGameSvVoteSet           = 15
	MsgGameSvVoteStatus        = 16
	MsgGameSvServerSettings    = 17
	MsgGameSvClientInfo        = 18
	MsgGameSvGameInfo          = 19
	MsgGameSvClientDrop        = 20
	MsgGameSvGameMsg           = 21
	MsgGameDeClientEnter       = 22
	MsgGameDeClientLeave       = 23
	MsgGameClSay               = 24
	MsgGameClSetTeam           = 25
	MsgGameClSetSpectatorMode  = 26
	MsgGameClStartInfo         = 27
	MsgGameClKill              = 28
	MsgGameClReadyChange       = 29
	MsgGameClEmoticon          = 30
	MsgGameClVote              = 31
	MsgGameClCallVote          = 32
	MsgGameSvSkinChange        = 33
	MsgGameClSkinChange        = 34
	MsgGameSvRaceFinish        = 35
	MsgGameSvCheckpoint        = 36
	MsgGameSvCommandInfo       = 37
	MsgGameSvCommandInfoRemove = 38
	MsgGameClCommand           = 39
)

// Snap object type IDs.
const (
	ObjPlayerInput    = 1
	ObjProjectile     = 2
	ObjLaser          = 3
	ObjPickup         = 4
	ObjFlag           = 5
	ObjGameData       = 6
	ObjGameDataTeam   = 7
	ObjGameDataFlag   = 8
	ObjCharacterCore  = 9
	ObjCharacter      = 10
	ObjPlayerInfo     = 11
	ObjSpectatorInfo  = 12
	ObjDeClientInfo   = 13
	ObjDeGameInfo     = 14
	ObjDeTuneParams   = 15
	ObjCommon         = 16
	ObjExplosion      = 17
	ObjSpawn          = 18
	ObjHammerHit      = 19
	ObjDeath          = 20
	ObjSoundWorld     = 21
	ObjDamage         = 22
	ObjPlayerInfoRace = 23
	ObjGameDataRace   = 24
)
