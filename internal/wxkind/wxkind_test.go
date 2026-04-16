package wxkind

import "testing"

func TestResolve(t *testing.T) {
	cases := []struct {
		bk, st int32
		want   string
	}{
		{1, 0, "text"},
		{3, 0, "image"},
		{34, 0, "voice"},
		{43, 0, "video"},
		{47, 0, "sticker"},
		{49, 0, "app"},        // base_kind=49 with unknown subtype falls back to "app"
		{49, 57, "quote"},     // refermsg
		{49, 2000, "transfer"},
		{49, 2001, "red_packet"},
		{49, 5, "link"},
		{49, 19, "forward_chat"},
		{49, 33, "miniprogram"},
		{49, 36, "miniprogram"},
		{49, 87, "announcement"},
		{49, 62, "pat"},
		{42, 0, "card"},
		{48, 0, "location"},
		{50, 0, "voip"},
		{10000, 0, "system"},
		{999, 0, "unknown"},   // unknown base_kind
	}
	for _, c := range cases {
		got := Resolve(c.bk, c.st)
		if got != c.want {
			t.Errorf("Resolve(%d,%d) = %q, want %q", c.bk, c.st, got, c.want)
		}
	}
}

func TestUnpack(t *testing.T) {
	// local_type = (subtype << 32) | base_kind
	// 244813135921 = (57 << 32) | 49 → quote
	bk, st, name := Unpack(244813135921)
	if bk != 49 || st != 57 || name != "quote" {
		t.Errorf("Unpack(244813135921) = (%d,%d,%q), want (49,57,quote)", bk, st, name)
	}
	// plain text: local_type = 1
	bk, st, name = Unpack(1)
	if bk != 1 || st != 0 || name != "text" {
		t.Errorf("Unpack(1) = (%d,%d,%q), want (1,0,text)", bk, st, name)
	}
	// 8589934592049 = (2000 << 32) | 49 → transfer
	bk, st, name = Unpack(8589934592049)
	if bk != 49 || st != 2000 || name != "transfer" {
		t.Errorf("Unpack(8589934592049) = (%d,%d,%q), want (49,2000,transfer)", bk, st, name)
	}
}

func TestFavKind(t *testing.T) {
	cases := []struct {
		t    int64
		want string
	}{
		{1, "text"},
		{2, "image"},
		{5, "link"},
		{14, "chat_history"},
		{18, "miniprogram"},
		{20, "unknown"}, // observed but undocumented
		{99, "unknown"},
	}
	for _, c := range cases {
		got := FavKind(c.t)
		if got != c.want {
			t.Errorf("FavKind(%d) = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestClassifyUsername(t *testing.T) {
	cases := []struct {
		u, want string
	}{
		{"12345678901@chatroom", "group"},
		{"gh_examplebiz", "official_account"},
		{"99999999999999@openim", "corp_im"},
		{"abc@weclaw", "clawbot"},
		{"foo@stranger", "stranger"},
		{"wxid_someone1234", "friend"},
		{"randomthing", "other"},
		{"", "other"},
	}
	for _, c := range cases {
		got := ClassifyUsername(c.u)
		if got != c.want {
			t.Errorf("ClassifyUsername(%q) = %q, want %q", c.u, got, c.want)
		}
	}
}
