# wx-mcp

微信 4.x 本地数据 MCP server (macOS). 13 个 tool, 自包含解密.

agent 友好: 每个字段语义清晰 (raw int 全 resolve), 命名一致, 无内部噪音.

## 位置

源码 + 二进制都在 `~/cc-workspace/mcp-servers/wx-mcp/` (工作区原地开发).

## 运行前提

- macOS arm64
- **SIP 关闭** (`csrutil disable` in Recovery Mode) — wxkey 走 task_for_pid + mach_vm_read 扫微信进程内存拿 WCDB master key, 内核要求 SIP-disabled + admin grant
- 微信 4.x 开着且登录过, 至少打开过一个会话 (让 DB 加载进内存, key 才会出现在 heap 里)
- key 拿到后写 `~/.config/wxcli/config.json`, 之后微信可关
- **分发包自带 `libWCDB.dylib` + `wxkey` CLI** — 解密路径完全自包含, 一个 zip 解开即用

## MCP 注册

```bash
claude mcp add --scope user wx-mcp ~/cc-workspace/mcp-servers/wx-mcp/wx-mcp
```

## 开发 / 更新

```bash
cd ~/cc-workspace/mcp-servers/wx-mcp
# 改源码 (cmd/ internal/) 后:
go build -o wx-mcp ./cmd/wx-mcp
# MCP 下次启动生效 (或 claude mcp restart wx-mcp)

# 跑测试 (helpers + XML parsers, ~30 case 不依赖 db/dylib):
go test ./...
```

## 打分发包 (给朋友)

```bash
./scripts/package.sh 1.3.0
# 产出 dist/wx-mcp-v1.3.0-darwin-arm64.zip (含 wx-mcp + libWCDB.dylib + README)
```

朋友解压后, 一个命令就能用: `claude mcp add wx-mcp /path/to/wx-mcp`.
前提: SIP 已关 + 微信 4.x 登录态 + 至少开过一个会话 (首次 key scan 用).

## Tools (13 个)

所有时间字段接 unix秒 或 `2006-01-02` (本地时区).

| Tool | 说明 |
|------|------|
| `sessions` | 会话列表 (按 sort_timestamp DESC). 字段: username / display_name / unread_count / summary / sort_timestamp / last_timestamp / last_sender_wxid / last_sender_display_name / last_msg_type / last_msg_sub_type / last_msg_kind_name. 支持 type_filter (group/friend/official_account/bot) + keyword 模糊搜索 |
| `contacts` | 联系人/群搜索. 字段: username / display_name / nick_name / remark (omitempty) / alias (omitempty) / description (omitempty) / type (friend/group/official_account/corp_im/clawbot/stranger/other) / is_verified (bool, 公众号/服务号/认证账号) |
| `messages` | 消息. fields=lite (默认) 返回核心 10 字段; fields=full 加 subtype + raw message_content + message_content_parsed (XML 结构化, 引用递归 depth=3). content_summary 已剥群聊 sender prefix. **keyword 在 zstd 解压后做 in-memory filter** (能命中 app 类消息) |
| `group_members` | 群成员. is_owner / is_friend 是 bool. stats=true 附 msg_count (扫消息表较慢) |
| `sns` | 朋友圈 + 点赞/评论. 字段: tid / username / nickname / avatar_url / create_time / content / type / private / liked_by_me / media / location / likes / comments |
| `search` | 跨会话全文搜索 (4 FTS 分区 UNION ALL). 字段: content (剥前缀) / talker / talker_display_name / sender_wxid / sender_display_name / base_kind / kind_name / local_id / create_time. FTS 索引可能落后实时几分钟 |
| `sql` | 任意只读 SQL. OS 级 readonly (SQLITE_OPEN_READONLY) — DDL/DML 直接报错. CTE/subquery/temp view/EXPLAIN 都安全 |
| `transfers` | 转账. 字段: transfer_id / transcation_id / payer_wxid / receiver_wxid / session_username / pay_sub_type / begin_transfer_time / **amount** ("￥5.00") / **description** ("收到转账5.00元") / memo (omitempty). amount/description/memo 是 batch join messages.server_id 解 XML 提取 |
| `red_packets` | 红包. 字段: send_id / sender_wxid / session_username / native_url / **wishing** ("恭喜发财大吉大利") / scene_text. 红包金额随机仅领取后可见, 不在本地数据中 |
| `favorites` | 收藏. 字段: server_id / favorite_type (link/text/image/voice/video/file/chat_history/miniprogram/...) / from_wxid / source_chat_username (omitempty) / update_time / **title** / **description** / **url** (从 content XML 提取) / source_id / content (XML raw) |
| `chatroom_announcements` | 群公告. 字段: chatroom_id / chatroom_display_name / announcement / editor_wxid / editor_display_name / publish_time |
| `forward_history` | **最近转发目标列表** (用于快捷转发, 非"被转发的消息历史"). 字段: username / display_name / forward_time |
| `schema` | WCDB 数据库结构. 不传参列所有 db 子目录 + 表名; 传 subdir+file 返回每张表 DDL |

## 关键概念

### kind_name 解码

`local_type` 是 packed int64: `(subtype << 32) | base_kind`. messages tool 已拆出 `base_kind` / `subtype` / `kind_name`, lite mode 隐藏 raw `local_type`.

- `base_kind`: 1=text / 3=image / 34=voice / 42=card / 43=video / 47=sticker / 48=location / 49=app / 50=voip / 10000=system
- `kind_name` 在 `base_kind=49` 时按 subtype 细化: 3=music / 5=link / 6,8,24=file / 19=forward_chat / 33,36=miniprogram / 49=link / 51=channel_video / 57=quote / 62=pat / 87=announcement / 2000=transfer / 2001=red_packet
- 引用消息 (subtype=57) 时 `message_content_parsed.refermsg` 含完整 quote 上下文 + 可递归 decode 的 content_parsed (depth≤3)

### 跨表 join key

- `server_id` (messages) ⇄ `message_server_id` (transfers/red_packets/favorites): int64, 跨 re-import 稳定. transfers/red_packets 已自动 batch join 解 XML, 不需要 agent 自己再调 messages
- search 命中行通过 `(talker, local_id)` 路由回 `Msg_<hash>(talker)` 拿 sender + base_kind/kind_name

### 错误处理

主路径错误 fail-loud (db 打不开 / SQL 失败立即 error).
batch enrichment (transfers amount, search sender) 是 best-effort: 单 talker 路由失败时该字段缺失 (其他行不受影响, agent 看到字段不存在就知道没拿到).

## 架构

```
~/cc-workspace/mcp-servers/wx-mcp/
├── cmd/wx-mcp/
│   ├── main.go            MCP server + tool handlers + 复杂 enrich pipeline
│   └── main_test.go       parseTS / talkerHash / contentSummary 等测试
├── internal/
│   ├── wcdb/              WCDB dylib FFI (sqlite3_key_v2 解密)
│   ├── config/            ~/.config/wxcli/config.json 管理
│   ├── wxkind/            base_kind / app subtype / fav type / username 分类映射
│   └── wxparse/           transfer / red-packet / favorite XML 解析
├── scripts/package.sh     打分发 zip
├── go.mod / go.sum
├── wx-mcp                 编译产物 (.gitignore)
└── README.md
```

运行时 `dlopen` 旁边的 `libWCDB.dylib` (分发包自带).

首次 key 获取 (v1.3.1+): wx-mcp 检测到 config 缺 key 时自动 spawn 同目录的 `wxkey setup` →
osascript 弹 admin 提权 → task_for_pid + mach_vm_read 扫微信 heap → 找到 `x'<hex>'` 字面量 →
SQLCipher 4 page-1 HMAC 验证 → 64 位 hex AES key → 存 `~/.config/wxcli/config.json`.
**SIP 必须关闭** (内核硬要求, 跟 root 无关).

分发 zip 结构:
```
wx-mcp-v1.4.0-darwin-arm64/
├── wx-mcp              (~10MB Go binary)
├── wxkey               (~3MB key 提取 CLI, 同目录被 wx-mcp spawn)
├── libWCDB.dylib       (~5MB Tencent WCDB, 随 binary 同目录加载)
└── README.md
```

## Changelog

### v1.3.1 (2026-04-16)
- **messages** 支持公众号/服务号 — `findMsgDB` 以前只扫 `message_0..4.db`, 漏了 `biz_message_0..1.db` (公众号消息实际存那边), 导致所有 `gh_*` 拉不到消息. 现在 glob 扫 `(message|biz_message)_<n>.db` 全族, shard 数也不再 hardcode
- **favorites** 剥 raw `type_id` (= raw int 重复 `favorite_type`) — 违反"raw int 全 resolve"原则
- **sessions.last_sender_wxid** 剥订阅号合集 sender 前缀 — 以前返回 `_$_CUSTOM_USERNAME_PREFIX_$_<aggId>:<realId>`, 现在只保留 `<realId>` (通常是 `gh_xxx`)
- **messages** 对聚合 session (`brandsessionholder` / `brandservicesessionholder`) 给明确错误 "本身无消息表, 按具体 gh_<id> 查", 替换 cryptic "table not found"
- **schema** 按 prefix 分族列 db — 以前把 `biz_message_*` / `message_fts` 误折成 `message_0..4` 的 shard, 现在 `message`/`biz_message`/`message_fts`/`message_resource` 各占一条, `shard_count` 按族算

### v1.3.0 (2026-04-16)
- **messages.keyword** 修 zstd bug — 原本 SQL LIKE 在压缩字节上 match 失败, 现在拉宽 SQL 后在解压内容上 in-memory filter, 能命中 app 类消息 (转账/链接/小程序/...)
- **transfers** 加 amount / description / memo (batch join messages 解 XML); 字段 rename: payer_wxid / receiver_wxid / session_username
- **red_packets** drop 4 个语义不明 raw int (hb_status/hb_type/receive_status/scene_id), 加 wishing / scene_text / native_url
- **search** 补 sender_wxid / sender_display_name / base_kind / kind_name (join 回 Msg_<hash> 路由), drop FTS 内部 session_id, content 剥群聊 sender prefix
- **chatroom_announcements** 字段下划线后缀清理 (announcement_/editor_/publish_time_ → announcement/editor_wxid/publish_time)
- **favorites** 加 favorite_type resolve, 加 title / description / url (从 content XML 提取), drop local_id/update_seq/flag, rename fromusr → from_wxid
- **group_members** drop big_head_url, is_owner / is_friend → bool
- **schema** 修 P0 panic (全局调用 nil deref); 单 db 加载失败现在归并 error 字段而非 silent skip
- 模块化重构: kind/parse helpers 抽到 internal/wxkind + internal/wxparse, ~30 个 unit test 覆盖
- search / schema 的 silent error swallow → fail loud

### v1.2.0
- schema tool, cross-db keyword search, is_from_me, create_time_human, description sweep

### v1.1.0
- agent-friendly display_name across all 12 tools

### v1.0.0
- 初始 12 个 tool
