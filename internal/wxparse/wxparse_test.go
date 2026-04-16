package wxparse

import "testing"

func TestStripMsgPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"wxid_abc:\n<msg>x</msg>", "<msg>x</msg>"},
		{"<msg>x</msg>", "<msg>x</msg>"},
		{"hello", "hello"}, // no '<' → returned as-is
		{"", ""},
	}
	for _, c := range cases {
		got := StripMsgPrefix(c.in)
		if got != c.want {
			t.Errorf("StripMsgPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

const transferSample = `wxid_abc:
<?xml version="1.0"?>
<msg>
	<appmsg appid="" sdkver="">
		<title><![CDATA[微信转账]]></title>
		<des><![CDATA[收到转账5.00元。如需收钱，请点此升级至最新版本]]></des>
		<type>2000</type>
		<wcpayinfo>
			<paysubtype>3</paysubtype>
			<feedesc><![CDATA[￥5.00]]></feedesc>
			<pay_memo><![CDATA[周末聚餐 AA]]></pay_memo>
		</wcpayinfo>
	</appmsg>
</msg>`

func TestTransferInfo(t *testing.T) {
	amount, des, memo := TransferInfo(transferSample)
	if amount != "￥5.00" {
		t.Errorf("amount = %q, want ￥5.00", amount)
	}
	if des != "收到转账5.00元。如需收钱，请点此升级至最新版本" {
		t.Errorf("des = %q", des)
	}
	if memo != "周末聚餐 AA" {
		t.Errorf("memo = %q, want '周末聚餐 AA'", memo)
	}
}

func TestTransferInfo_Malformed(t *testing.T) {
	amount, des, memo := TransferInfo("not xml at all")
	if amount != "" || des != "" || memo != "" {
		t.Errorf("malformed should return empty, got (%q,%q,%q)", amount, des, memo)
	}
}

const redPacketSample = `<msg>
	<appmsg appid="" sdkver="">
		<wcpayinfo>
			<sendertitle>恭喜发财，大吉大利</sendertitle>
			<scenetext>微信红包</scenetext>
			<nativeurl>wxpay://c2cbizmessagehandler/hongbao/receivehongbao?msgtype=1</nativeurl>
		</wcpayinfo>
	</appmsg>
</msg>`

func TestRedPacketInfo(t *testing.T) {
	wishing, sceneText := RedPacketInfo(redPacketSample)
	if wishing != "恭喜发财，大吉大利" {
		t.Errorf("wishing = %q", wishing)
	}
	if sceneText != "微信红包" {
		t.Errorf("sceneText = %q", sceneText)
	}
}

const favLinkSample = `<favitem type="5">
	<source sourcetype="3" sourceid="__biz=...">
		<fromusr>wxid_abc</fromusr>
		<link>https://mp.weixin.qq.com/s/test</link>
	</source>
	<weburlitem>
		<pagetitle>Hermes Agent深度解析</pagetitle>
		<pagedesc>开源 AI Agent 框架</pagedesc>
		<clean_url>https://mp.weixin.qq.com/s/test</clean_url>
	</weburlitem>
</favitem>`

const favNoteSample = `<favitem type="1">
	<noteitem>
		<title>会议纪要</title>
		<description>讨论 OKR 进度</description>
	</noteitem>
</favitem>`

func TestFavoriteInfo(t *testing.T) {
	title, desc, url := FavoriteInfo(favLinkSample)
	if title != "Hermes Agent深度解析" {
		t.Errorf("link title = %q", title)
	}
	if desc != "开源 AI Agent 框架" {
		t.Errorf("link desc = %q", desc)
	}
	if url != "https://mp.weixin.qq.com/s/test" {
		t.Errorf("link url = %q", url)
	}

	title, desc, url = FavoriteInfo(favNoteSample)
	if title != "会议纪要" || desc != "讨论 OKR 进度" || url != "" {
		t.Errorf("note = (%q,%q,%q), want (会议纪要,讨论 OKR 进度,)", title, desc, url)
	}
}

func TestFavoriteInfo_Malformed(t *testing.T) {
	title, desc, url := FavoriteInfo("garbage")
	if title != "" || desc != "" || url != "" {
		t.Errorf("malformed should return empty, got (%q,%q,%q)", title, desc, url)
	}
}
