// Package net6 defines constants and message types for the Teeworlds 0.6 protocol.
package net6

const (
	// MaxClients is the maximum player slots a 0.6 server exposes.
	MaxClients = 16
	// NetVersion is the 0.6 network version string sent in NETMSG_INFO; it must
	// match the server's expected version (DDNet `GAME_NETVERSION`).
	NetVersion = "0.6 626fce9a778df4d4"

	// Split is the chunk header split parameter for 0.6.
	// Size uses 6+4=10 bits, sequence uses 4+8=12 bits (only 10 used).
	Split = 4
)

// Control message IDs.
// Note: ACCEPT(3) is DDNet-specific — vanilla 0.6 goes CONNECT(1), CONNECTACCEPT(2), CLOSE(4).
const (
	MsgCtrlKeepAlive     = 0x00
	MsgCtrlConnect       = 0x01
	MsgCtrlConnectAccept = 0x02
	MsgCtrlAccept        = 0x03 // DDNet token handshake step 3
	MsgCtrlClose         = 0x04
)

// DDNet extended message constants.
const (
	// MsgSysEx is the system message ID used for UUID-based extended messages.
	// Wire format: PackMsgID(0, system=true) → varint(1), followed by 16-byte UUID.
	MsgSysEx = 0
)

var (
	// DDNetVersion is the DDNet client version we advertise.
	// 19070 = DDNet 19.0.7
	DDNetVersion = 19070
	// DDNetVersionStr is the human-readable client version sent in the DDNet
	// CLIENTVER handshake message.
	DDNetVersionStr = "DDNet 19.0.7 (tw-protocol/go)"
)

// System message IDs.
const (
	MsgSysInfo           = 1
	MsgSysMapChange      = 2
	MsgSysMapData        = 3
	MsgSysConReady       = 4
	MsgSysSnap           = 5
	MsgSysSnapEmpty      = 6
	MsgSysSnapSingle     = 7
	MsgSysSnapSmall      = 8
	MsgSysInputTiming    = 9
	MsgSysRconAuthStatus = 10
	MsgSysRconLine       = 11
	MsgSysAuthChallenge  = 12
	MsgSysAuthResult     = 13
	MsgSysReady          = 14
	MsgSysEnterGame      = 15
	MsgSysInput          = 16
	MsgSysRconCmd        = 17
	MsgSysRconAuth       = 18
	MsgSysRequestMapData = 19
	MsgSysAuthStart      = 20
	MsgSysAuthResponse   = 21
	MsgSysPing           = 22
	MsgSysPingReply      = 23
	MsgSysError          = 24
	MsgSysRconCmdAdd     = 25
	MsgSysRconCmdRem     = 26
)

// Game message IDs.
const (
	MsgGameSvMotd              = 1
	MsgGameSvBroadcast         = 2
	MsgGameSvChat              = 3
	MsgGameSvKillMsg           = 4
	MsgGameSvSoundGlobal       = 5
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
	MsgGameClSay               = 17
	MsgGameClSetTeam           = 18
	MsgGameClSetSpectatorMode  = 19
	MsgGameClStartInfo         = 20
	MsgGameClChangeInfo        = 21
	MsgGameClKill              = 22
	MsgGameClEmoticon          = 23
	MsgGameClVote              = 24
	MsgGameClCallVote          = 25

	// DDNet/DDRace legacy game message IDs (appended after vanilla 0.6 messages).
	MsgGameSvDDRaceTimeLegacy = 26
	MsgGameSvRecordLegacy     = 27
)

// Sv_Chat m_Team values. DDNet uses Enum("TEAM", ..., -2): ALL=-2,
// SPECTATORS=-1, RED=0, BLUE=1, WHISPER_SEND=2, WHISPER_RECV=3.
const (
	TeamWhisperSend = 2
	TeamWhisperRecv = 3
)

// Snap object type IDs (0.6).
const (
	ObjPlayerInput     = 1
	ObjProjectile      = 2
	ObjLaser           = 3
	ObjPickup          = 4
	ObjFlag            = 5
	ObjGameInfo        = 6
	ObjGameData        = 7
	ObjCharacterCore   = 8
	ObjCharacter       = 9
	ObjPlayerInfo      = 10
	ObjClientInfo      = 11
	ObjSpectatorInfo   = 12
	ObjCommon          = 13
	ObjExplosion       = 14
	ObjSpawn           = 15
	ObjHammerHit       = 16
	ObjDeath           = 17
	ObjSoundGlobal     = 18
	ObjSoundWorld      = 19
	ObjDamageIndicator = 20
)
