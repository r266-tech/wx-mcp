package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseTS_UnixSeconds(t *testing.T) {
	got, err := parseTS("1776330000")
	if err != nil || got != 1776330000 {
		t.Errorf("parseTS('1776330000') = (%d,%v), want (1776330000,nil)", got, err)
	}
}

func TestParseTS_DateOnly(t *testing.T) {
	got, err := parseTS("2026-04-16")
	if err != nil {
		t.Fatalf("parseTS error: %v", err)
	}
	want := time.Date(2026, 4, 16, 0, 0, 0, 0, time.Local).Unix()
	if got != want {
		t.Errorf("parseTS('2026-04-16') = %d, want %d", got, want)
	}
}

func TestParseTS_DateTime(t *testing.T) {
	got, err := parseTS("2026-04-16T12:30:45")
	if err != nil {
		t.Fatalf("parseTS error: %v", err)
	}
	want := time.Date(2026, 4, 16, 12, 30, 45, 0, time.Local).Unix()
	if got != want {
		t.Errorf("parseTS = %d, want %d", got, want)
	}
}

func TestParseTS_Empty(t *testing.T) {
	got, err := parseTS("")
	if err != nil || got != 0 {
		t.Errorf("parseTS('') = (%d,%v), want (0,nil)", got, err)
	}
}

func TestParseTS_Bad(t *testing.T) {
	_, err := parseTS("not-a-time")
	if err == nil {
		t.Error("parseTS('not-a-time') should error")
	}
}

func TestTalkerHash(t *testing.T) {
	// md5("wxid_testtalker0001") known value (verified via python hashlib).
	got := talkerHash("wxid_testtalker0001")
	want := "b2ed09282c82cadc5646d5a6c462c429"
	if got != want {
		t.Errorf("talkerHash = %q, want %q", got, want)
	}
}

func TestSenderPrefixRe(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"wxid_puf:\nhello", "hello"},
		{"abc-123:\r\nworld", "world"},
		{"  wxid_x:\nbody", "body"},     // leading whitespace
		{"plain text no prefix", "plain text no prefix"},
		{"https://example.com\nstuff", "https://example.com\nstuff"}, // URL not stripped (':' followed by '/')
		{"wxid_x: still text", "wxid_x: still text"},                 // ':' not followed by newline
	}
	for _, c := range cases {
		got := senderPrefixRe.ReplaceAllString(c.in, "")
		if got != c.want {
			t.Errorf("strip(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestContentSummary_Text(t *testing.T) {
	got := contentSummary(1, 0, "wxid_x:\nhello world", nil)
	if got != "hello world" {
		t.Errorf("text = %q, want 'hello world'", got)
	}
}

func TestContentSummary_Image(t *testing.T) {
	got := contentSummary(3, 0, "<msg>...", nil)
	if got != "[图片]" {
		t.Errorf("image = %q, want '[图片]'", got)
	}
}

func TestContentSummary_AppNoTitle(t *testing.T) {
	got := contentSummary(49, 0, "<msg>...", nil)
	if got != "[应用消息]" {
		t.Errorf("app no parsed = %q, want '[应用消息]'", got)
	}
}

func TestContentSummary_AppWithTitle(t *testing.T) {
	parsed := map[string]any{"title": "微信转账"}
	got := contentSummary(49, 0, "<msg>...", parsed)
	if got != "微信转账" {
		t.Errorf("app with title = %q, want '微信转账'", got)
	}
}

func TestContentSummary_Quote(t *testing.T) {
	parsed := map[string]any{
		"title": "好的",
		"refermsg": map[string]any{
			"type":         int(1),
			"content_raw":  "原话",
		},
	}
	got := contentSummary(49, 57, "<msg>...", parsed)
	if !strings.HasPrefix(got, "[引用: ") {
		t.Errorf("quote should start with '[引用: ', got %q", got)
	}
	if !strings.Contains(got, "好的") {
		t.Errorf("quote should include reply title '好的', got %q", got)
	}
}

func TestContentSummary_System(t *testing.T) {
	got := contentSummary(10000, 0, "对方撤回了一条消息", nil)
	if got != "对方撤回了一条消息" {
		t.Errorf("system = %q", got)
	}
}
