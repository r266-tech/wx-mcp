# wx-mcp

微信 4.x 本地数据 MCP server (macOS). 12 个 tools — 超越 WeFlow HTTP API.

## 位置

源码 + 二进制都在 `~/cc-workspace/mcp-servers/wx-mcp/` (工作区原地开发).

## 运行前提

- macOS arm64
- 装了 WeFlow 且**首次运行时 WeFlow 需要打开** (通过 V8 inspector 获取 key)
- 拿到 key 后存在 `~/.config/wxcli/config.json`, 之后 WeFlow 可关 / 可卸

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
./scripts/package.sh 0.4.0
# 产出 dist/wx-mcp-v0.4.0-darwin-arm64.zip
```
朋友解压后, 一个命令就能用: `claude mcp add wx-mcp /path/to/wx-mcp`.
前提他已装 WeFlow 并连过微信 (为了首次 key 导入).

## Tools (12 个)

| Tool | 说明 |
|------|------|
| `sessions` | 会话列表 (支持 keyword) |
| `contacts` | 联系人/群搜索 |
| `messages` | 消息拉取 (支持 keyword/时间范围) |
| `group_members` | 群成员 + 可选发言数 |
| `sns` | 朋友圈 + 点赞/评论 |
| `search` | 跨会话全文搜索 (FTS 85 万条索引) |
| `sql` | 任意只读 SQL |
| `transfers` | 转账记录 |
| `red_packets` | 红包记录 |
| `favorites` | 收藏列表 |
| `chatroom_announcements` | 群公告 |
| `forward_history` | 转发历史 |

## 架构

单文件, 运行时 dlopen WeFlow 自带的 `libWCDB.dylib`
(`/Applications/WeFlow.app/Contents/Resources/resources/wcdb/macos/universal/libWCDB.dylib`).

首次 key 获取: `kill -USR1 $(pgrep -x WeFlow)` 激活 V8 inspector → WebSocket CDP
→ `safeStorage.decryptString(...)` → 64 位 hex AES key → 存 `~/.config/wxcli/config.json`.

## 目录结构

```
~/cc-workspace/mcp-servers/wx-mcp/
├── cmd/wx-mcp/main.go          MCP server + tool handlers + XML parser
├── internal/
│   ├── wcdb/wcdb.go            WCDB dylib 的 FFI 包装 (sqlite3_key_v2 解密)
│   ├── weflow/weflow.go        从 WeFlow 读 key (V8 inspector + CDP)
│   └── config/config.go        ~/.config/wxcli/config.json 管理
├── scripts/package.sh          打分发 zip
├── go.mod / go.sum
├── wx-mcp                      编译产物 (.gitignore)
└── README.md                   本文件
```
