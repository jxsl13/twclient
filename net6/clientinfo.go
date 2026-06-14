package net6

// ObjClientInfo field offsets (DDNet CNetObj_ClientInfo, 17 ints): name[0:4],
// clan[4:7], country[7], skin[8:14], then color fields. Strings are int-packed
// (4 chars per int, each byte offset by +128 on encode) — decode via IntsToStr.
const (
	clientInfoNameInts   = 4 // m_aName[4]  → up to 16 chars
	clientInfoClanInts   = 3 // m_aClan[3]  → up to 12 chars
	clientInfoCountryIdx = 7 // m_Country
	clientInfoSkinInts   = 6 // m_aSkin[6]  → up to 24 chars
	clientInfoNameStart  = 0
	clientInfoClanStart  = 4
	clientInfoSkinStart  = 8
)

// IntsToStr decodes a teeworlds int-packed string (inverse of DDNet's
// StrToInts): each int holds 4 bytes, each byte offset by +128 on encode, so
// decode masks each byte and subtracts 128; the string ends at the first NUL.
// Mirrors DDNet IntsToStr (src/base/system.cpp).
func IntsToStr(ints []int) string {
	buf := make([]byte, 0, len(ints)*4)
	for _, v := range ints {
		for shift := 24; shift >= 0; shift -= 8 {
			ch := ((v >> shift) & 0xff) - 128
			if ch == 0 {
				return string(buf)
			}
			buf = append(buf, byte(ch))
		}
	}
	return string(buf)
}

// ClientInfo holds the decoded identity fields from an ObjClientInfo snapshot
// item (0.6 / DDNet). Score and Team live in ObjPlayerInfo, not here.
type ClientInfo struct {
	Name    string
	Clan    string
	Country int
	Skin    string
}

// DecodeClientInfo decodes an ObjClientInfo item's int fields into name/clan/
// country/skin. fields must hold at least SizeClientInfo (17) ints; a shorter
// slice yields the zero value (no panic).
func DecodeClientInfo(fields []int) ClientInfo {
	if len(fields) < SizeClientInfo {
		return ClientInfo{}
	}
	return ClientInfo{
		Name:    IntsToStr(fields[clientInfoNameStart : clientInfoNameStart+clientInfoNameInts]),
		Clan:    IntsToStr(fields[clientInfoClanStart : clientInfoClanStart+clientInfoClanInts]),
		Country: fields[clientInfoCountryIdx],
		Skin:    IntsToStr(fields[clientInfoSkinStart : clientInfoSkinStart+clientInfoSkinInts]),
	}
}
