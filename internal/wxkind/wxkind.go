// Package wxkind maps wechat raw integer codes to human-readable labels.
// Sources: WeFlow electron services + observed WeChat 4.x schema.
package wxkind

import "strings"

// BaseKindNames maps wechat top-level message kind ints to a label.
// For base_kind=49 the subtype refines the meaning (see AppSubtypeNames).
var BaseKindNames = map[int32]string{
	1:     "text",
	3:     "image",
	34:    "voice",
	42:    "card",
	43:    "video",
	47:    "sticker",
	48:    "location",
	49:    "app",
	50:    "voip",
	10000: "system",
}

// AppSubtypeNames maps the subtype of an app-message (base_kind=49) to a
// precise human-readable kind. Falls back to "app" when subtype is unknown.
var AppSubtypeNames = map[int32]string{
	3:    "music",
	5:    "link",
	6:    "file",
	8:    "file",
	19:   "forward_chat",
	24:   "file",
	33:   "miniprogram",
	36:   "miniprogram",
	49:   "link",
	51:   "channel_video",
	57:   "quote",
	62:   "pat",
	87:   "announcement",
	2000: "transfer",
	2001: "red_packet",
}

// FavTypeNames maps wechat favorite kind ints to a human-readable label.
// Source: WeChat client schema (CDataItem types). Undocumented values
// (e.g. 20) fall through to "unknown".
var FavTypeNames = map[int64]string{
	1:  "text",
	2:  "image",
	3:  "voice",
	4:  "video",
	5:  "link",
	6:  "location",
	8:  "file",
	14: "chat_history",
	18: "miniprogram",
}

// Resolve returns the most specific human-readable label for (baseKind, subtype).
// For base_kind=49 it consults AppSubtypeNames first; otherwise BaseKindNames.
// Returns "unknown" if neither matches.
func Resolve(baseKind, subtype int32) string {
	if baseKind == 49 {
		if n, ok := AppSubtypeNames[subtype]; ok {
			return n
		}
	}
	if n, ok := BaseKindNames[baseKind]; ok {
		return n
	}
	return "unknown"
}

// Unpack splits a packed local_type int64 into (baseKind, subtype, name).
// local_type encoding: (subtype << 32) | baseKind.
func Unpack(lt int64) (baseKind, subtype int32, name string) {
	baseKind = int32(lt & 0xFFFFFFFF)
	subtype = int32(lt >> 32)
	name = Resolve(baseKind, subtype)
	return
}

// FavKind resolves a favorite type_id to a human-readable label, or "unknown".
func FavKind(t int64) string {
	if n, ok := FavTypeNames[t]; ok {
		return n
	}
	return "unknown"
}

// ClassifyUsername maps a raw username to a human-readable type by well-known
// patterns (suffix/prefix). Stable across schema changes since it depends only
// on the username string shape, not contact.local_type which varies by version.
func ClassifyUsername(u string) string {
	switch {
	case strings.HasSuffix(u, "@chatroom"):
		return "group"
	case strings.HasPrefix(u, "gh_"):
		return "official_account"
	case strings.HasSuffix(u, "@openim"):
		return "corp_im"
	case strings.HasSuffix(u, "@weclaw"):
		return "clawbot"
	case strings.HasSuffix(u, "@stranger"):
		return "stranger"
	case strings.HasPrefix(u, "wxid_"):
		return "friend"
	}
	return "other"
}
