package packet

// Server capability flags advertised by DDNet servers in the
// capabilities@ddnet.tw NETMSG_EX (src/engine/shared/protocol_ex.h).
const (
	ServerCapFlagDDNet           = 1 << 0
	ServerCapFlagChatTimeoutCode = 1 << 1
	ServerCapFlagAnyPlayerFlag   = 1 << 2
	ServerCapFlagPingEx          = 1 << 3
	ServerCapFlagAllowDummy      = 1 << 4
	ServerCapFlagSyncWeaponInput = 1 << 5
)

// ServerCapabilities is the decoded DDNet server capability set. A server that
// never sends the capabilities message (vanilla / 0.7 / old DDNet) yields the
// zero value (all features false).
type ServerCapabilities struct {
	Version         int
	DDNet           bool
	ChatTimeoutCode bool // server accepts the /timeout <code> reclaim command
	AnyPlayerFlag   bool
	PingEx          bool
	AllowDummy      bool
	SyncWeaponInput bool
}

// ParseServerCapabilities builds a ServerCapabilities from the wire Version and
// Flags fields, mirroring DDNet's GetServerCapabilities.
func ParseServerCapabilities(version, flags int) ServerCapabilities {
	return ServerCapabilities{
		Version:         version,
		DDNet:           flags&ServerCapFlagDDNet != 0,
		ChatTimeoutCode: flags&ServerCapFlagChatTimeoutCode != 0,
		AnyPlayerFlag:   flags&ServerCapFlagAnyPlayerFlag != 0,
		PingEx:          flags&ServerCapFlagPingEx != 0,
		AllowDummy:      flags&ServerCapFlagAllowDummy != 0,
		SyncWeaponInput: flags&ServerCapFlagSyncWeaponInput != 0,
	}
}

// EventServerCapabilities is delivered when the server announces its
// capabilities (DDNet capabilities@ddnet.tw NETMSG_EX).
type EventServerCapabilities struct {
	Caps ServerCapabilities
}

func (EventServerCapabilities) eventTag() {}
