// Package wxparse extracts structured info from wechat XML payloads stored
// in transfer / red-packet / favorite tables. Standalone parsers — no dep on
// the message_content enrich pipeline.
package wxparse

import (
	"encoding/xml"
	"strings"
)

// StripMsgPrefix trims the "wxid_xxx:\n" sender prefix WeChat prepends to
// group message content so xml.Unmarshal sees a clean XML document.
func StripMsgPrefix(raw string) string {
	if idx := strings.Index(raw, "<"); idx > 0 {
		return raw[idx:]
	}
	return raw
}

// xmlTransferMsg parses wechat transfer (subtype=2000) messages. Amount is in
// wcpayinfo.feedesc ("￥5.00"); appmsg.des is human-readable summary
// ("收到转账5.00元"); pay_memo is sender's note attached to the transfer.
type xmlTransferMsg struct {
	XMLName xml.Name `xml:"msg"`
	AppMsg  struct {
		Des       string `xml:"des"`
		WcPayInfo struct {
			FeeDesc    string `xml:"feedesc"`
			PaySubType int    `xml:"paysubtype"`
			PayMemo    string `xml:"pay_memo"`
		} `xml:"wcpayinfo"`
	} `xml:"appmsg"`
}

// TransferInfo extracts (amount, description, memo) from a transfer message
// XML. Returns empty strings on parse failure.
func TransferInfo(content string) (amount, des, memo string) {
	var t xmlTransferMsg
	if err := xml.Unmarshal([]byte(StripMsgPrefix(content)), &t); err != nil {
		return
	}
	return t.AppMsg.WcPayInfo.FeeDesc, t.AppMsg.Des, t.AppMsg.WcPayInfo.PayMemo
}

// xmlRedPacketMsg parses wechat red-packet (subtype=2001) messages.
// sendertitle is sender-side wishing text; nativeurl carries the deep link;
// scenetext distinguishes 1v1 / group / luck-draw scenarios.
type xmlRedPacketMsg struct {
	XMLName xml.Name `xml:"msg"`
	AppMsg  struct {
		WcPayInfo struct {
			SenderTitle   string `xml:"sendertitle"`
			ReceiverTitle string `xml:"receivertitle"`
			SceneText     string `xml:"scenetext"`
			TemplateID    string `xml:"templateid"`
			InnerType     int    `xml:"innertype"`
			NativeURL     string `xml:"nativeurl"`
		} `xml:"wcpayinfo"`
	} `xml:"appmsg"`
}

// RedPacketInfo extracts (wishing, sceneText) from a red-packet XML.
func RedPacketInfo(content string) (wishing, sceneText string) {
	var r xmlRedPacketMsg
	if err := xml.Unmarshal([]byte(StripMsgPrefix(content)), &r); err != nil {
		return
	}
	return r.AppMsg.WcPayInfo.SenderTitle, r.AppMsg.WcPayInfo.SceneText
}

// xmlFavItem covers the most common favorite XML shapes (link / note / data).
// title/desc/url fields fall through whichever sub-item element is populated.
type xmlFavItem struct {
	XMLName    xml.Name `xml:"favitem"`
	WebURLItem struct {
		PageTitle string `xml:"pagetitle"`
		PageDesc  string `xml:"pagedesc"`
		CleanURL  string `xml:"clean_url"`
	} `xml:"weburlitem"`
	NoteItem struct {
		Title       string `xml:"title"`
		Description string `xml:"description"`
	} `xml:"noteitem"`
	DataItem struct {
		DataTitle string `xml:"datatitle"`
		DataDesc  string `xml:"datadesc"`
	} `xml:"dataitem"`
}

// FavoriteInfo extracts (title, description, url) from a favorite XML.
// Picks whichever inner item shape is populated.
func FavoriteInfo(content string) (title, desc, url string) {
	var f xmlFavItem
	if err := xml.Unmarshal([]byte(content), &f); err != nil {
		return
	}
	switch {
	case f.WebURLItem.PageTitle != "":
		return f.WebURLItem.PageTitle, f.WebURLItem.PageDesc, f.WebURLItem.CleanURL
	case f.NoteItem.Title != "":
		return f.NoteItem.Title, f.NoteItem.Description, ""
	case f.DataItem.DataTitle != "":
		return f.DataItem.DataTitle, f.DataItem.DataDesc, ""
	}
	return
}
