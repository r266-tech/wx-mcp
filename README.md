# wx-mcp

微信 4.x 本地数据 MCP server (macOS). 12 个 tools — 超越 WeFlow HTTP API.

## 位置

源码 + 二进制都在 `~/cc-workspace/mcp-servers/wx-mcp/` (工作区原地开发).

## 运行前提

- macOS arm64
- 装了 WeFlow 且**首次运行时 WeFlow 需要打开** (通过 V8 inspector 获取 key)
- 拿到 key 后存在 `~/.config/wxcli/config.json`, 之后 WeFlow 可关 / 可卸
- **分发包自带 `libWCDB.dylib`** — 朋友只需要有 WeFlow 跑过一次以注入 key, 不要求 WeFlow 持续在线

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
```

## 打分发包 (给朋友)

```bash
./scripts/package.sh 1.0.0
# 产出 dist/wx-mcp-v1.0.0-darwin-arm64.zip (含 wx-mcp + libWCDB.dylib + README)
```

朋友解压后, 一个命令就能用: `claude mcp add wx-mcp /path/to/wx-mcp`.
前提他已装 WeFlow 并连过微信 (为了首次 key 导入).

## Tools (12 个)

| Tool | 说明 |
|------|------|
| `sessions` | 会话列表 (支持 keyword) |
| `contacts` | 联系人/群搜索 |
| `messages` | 消息拉取 (支持 keyword/时间范围). 返回含 `base_kind`/`subtype`/`kind_name`/`message_content_parsed` |
| `group_members` | 群成员 + 可选发言数 |
| `sns` | 朋友圈 + 点赞/评论 |
| `search` | 跨会话全文搜索 (4 分区 UNION ALL + 全局时间倒序, 无 per-partition early-stop) |
| `sql` | 任意只读 SQL |
| `transfers` | 转账记录 (`message_server_id` join 到 `messages.server_id` 拿金额 XML) |
| `red_packets` | 红包记录 (同上 join 方式) |
| `favorites` | 收藏列表 (同上 join 方式) |
| `chatroom_announcements` | 群公告 |
| `forward_history` | 转发历史 |

## 输出 shape 要点 (v1.0.0)

- `local_type` 是 **packed int64**: `(subtype << 32) | base_kind`. messages tool 已拆出 `base_kind` / `subtype` / `kind_name`, raw `local_type` 保留
- `base_kind`: 1=text / 3=image / 34=voice / 43=video / 47=sticker / 49=app / 10000=system
- `subtype` 仅 `base_kind=49` 时有意义, e.g. `57`=引用回复 (`<refermsg>`)
- `message_content_parsed`: 按 `(base_kind, subtype)` 分派结构化, 失败缺席; `message_content` raw 始终保留
- `subtype=57` 时保留完整 `refermsg` 含 `content_raw` + (可递归 decode 的) `content_parsed`, depth≤3
- `server_id` (messages) ⇄ `message_server_id` (transfers/red_packets/favorites) 是跨表 join key, int64, 跨 re-import 稳定
- 时间参数 `after`/`before` 接 unix秒或 `2006-01-02`, 本地时区

## 架构

单文件 MCP server. 运行时 `dlopen` 旁边的 `libWCDB.dylib` (分发包自带; dev 环境 fallback 到 WeFlow bundled 路径).

首次 key 获取: `kill -USR1 $(pgrep -x WeFlow)` 激活 V8 inspector → WebSocket CDP
→ `safeStorage.decryptString(...)` → 64 位 hex AES key → 存 `~/.config/wxcli/config.json`.

## 目录结构

```
~/cc-workspace/mcp-servers/wx-mcp/
├── cmd/wx-mcp/main.go          MCP server + tool handlers + XML parsers
├── internal/
│   ├── wcdb/wcdb.go            WCDB dylib 的 FFI 包装 (sqlite3_key_v2 解密)
│   ├── weflow/weflow.go        从 WeFlow 读 key (V8 inspector + CDP)
│   └── config/config.go        ~/.config/wxcli/config.json 管理
├── scripts/package.sh          打分发 zip (含 dylib)
├── go.mod / go.sum
├── wx-mcp                      编译产物 (.gitignore)
└── README.md                   本文件
```

分发 zip 结构:
```
wx-mcp-v1.0.0-darwin-arm64/
├── wx-mcp              (9MB Go binary)
├── libWCDB.dylib       (5MB Tencent WCDB, 随 binary 同目录加载)
└── README.md
```
