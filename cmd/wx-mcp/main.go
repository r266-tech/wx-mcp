package main

import (
	"bufio"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/r266-tech/wxcli/internal/config"
	"github.com/r266-tech/wxcli/internal/wcdb"
	"github.com/r266-tech/wxcli/internal/weflow"
)

// ──────────────────── MCP protocol types ────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"` // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string   `json:"jsonrpc"`
	ID      any      `json:"id"`
	Result  any      `json:"result,omitempty"`
	Error   *rpcErr  `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ──────────────────── server state ────────────────────

type server struct {
	cfg      *config.Config
	wcdbPath string
	ok       bool
}

// findWCDB locates libWCDB.dylib. Prefers WeFlow's bundled copy
// (required anyway for initial key import), falls back to a
// dylib placed next to the binary.
func findWCDB() (string, error) {
	candidates := []string{
		"/Applications/WeFlow.app/Contents/Resources/resources/wcdb/macos/universal/libWCDB.dylib",
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			candidates = append(candidates,
				filepath.Join(dir, "libWCDB.dylib"),
				filepath.Join(dir, "lib", "libWCDB.dylib"),
				filepath.Join(dir, "lib", "WCDB.framework", "Versions", "2.1.15", "WCDB"),
			)
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("libWCDB.dylib 未找到 (请确保 WeFlow 已安装)")
}

func (s *server) ensure() error {
	if s.ok {
		return nil
	}
	wcdbPath, err := findWCDB()
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DBRoot == "" {
		root, wxid, err := config.AutoDetectDBRoot()
		if err != nil {
			return fmt.Errorf("未找到微信数据目录 (微信已登录?): %w", err)
		}
		cfg.DBRoot = root
		if cfg.Wxid == "" {
			cfg.Wxid = wxid
		}
		_ = config.Save(cfg)
	}
	if cfg.Key == "" {
		if !weflow.Available() {
			return fmt.Errorf("需要先安装 WeFlow 并连接微信 (https://weflow.cc)")
		}
		fmt.Fprintln(os.Stderr, "[wx-mcp]auto-importing key from WeFlow...")
		imp, err := weflow.ImportKey()
		if err != nil {
			return fmt.Errorf("WeFlow 密钥导入失败: %w", err)
		}
		cfg.Key = imp.HexKey
		cfg.Wxid = imp.Wxid
		cfg.DBRoot = imp.DBRoot
		cfg.KeyEpoch = time.Now().Unix()
		_ = config.Save(cfg)
		fmt.Fprintln(os.Stderr, "[wx-mcp]key imported OK")
	}
	s.cfg = cfg
	s.wcdbPath = wcdbPath
	s.ok = true
	return nil
}

func (s *server) openDB(subdir, file string) (*wcdb.DB, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if err := wcdb.Bootstrap(s.wcdbPath); err != nil {
		return nil, err
	}
	return wcdb.Open(filepath.Join(s.cfg.DBRoot, "db_storage", subdir, file), s.cfg.Key)
}

func (s *server) findMsgDB(tableName string) (*wcdb.DB, error) {
	for i := 0; i < 5; i++ {
		db, err := s.openDB("message", fmt.Sprintf("message_%d.db", i))
		if err != nil {
			continue
		}
		rows, err := db.Query("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", tableName)
		if err == nil && len(rows) > 0 {
			return db, nil
		}
		db.Close()
	}
	return nil, fmt.Errorf("table %s not found in message_0..4.db", tableName)
}

// ──────────────────── main loop ────────────────────

func main() {
	srv := &server{}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 4*1024*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		if req.ID == nil { // notification — no response
			continue
		}
		resp := srv.handle(req)
		out, _ := json.Marshal(resp)
		fmt.Fprintf(os.Stdout, "%s\n", out)
	}
}

func (s *server) handle(req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":   map[string]any{"tools": map[string]any{}},
			"serverInfo":     map[string]any{"name": "wx-mcp", "version": "1.0.0"},
		}}
	case "tools/list":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": toolDefs}}
	case "tools/call":
		var p toolCallParams
		json.Unmarshal(req.Params, &p)
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: s.callTool(p)}
	default:
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcErr{Code: -32601, Message: "unknown method"}}
	}
}

func (s *server) callTool(p toolCallParams) toolResult {
	handlers := map[string]func(map[string]any) (any, error){
		"sessions":               s.toolSessions,
		"contacts":               s.toolContacts,
		"messages":               s.toolMessages,
		"group_members":          s.toolGroupMembers,
		"sns":                    s.toolSns,
		"search":                 s.toolSearch,
		"sql":                    s.toolSQL,
		"transfers":              s.toolTransfers,
		"red_packets":            s.toolRedPackets,
		"favorites":              s.toolFavorites,
		"chatroom_announcements": s.toolChatroomAnnouncements,
		"forward_history":        s.toolForwardHistory,
	}
	fn, ok := handlers[p.Name]
	if !ok {
		return errResult("unknown tool: " + p.Name)
	}
	result, err := fn(p.Arguments)
	if err != nil {
		return errResult(err.Error())
	}
	b, _ := json.Marshal(result)
	return toolResult{Content: []contentBlock{{Type: "text", Text: string(b)}}}
}

func errResult(msg string) toolResult {
	return toolResult{IsError: true, Content: []contentBlock{{Type: "text", Text: msg}}}
}

// ──────────────────── tool definitions ────────────────────

var toolDefs = []toolDef{
	{
		Name:        "sessions",
		Description: "列出微信聊天会话, 按最新消息时间倒序",
		InputSchema: jsonSchema(props{
			"limit":   intProp("返回条数 (默认 50)"),
			"keyword": strProp("按摘要/用户名模糊搜索"),
		}, nil),
	},
	{
		Name:        "contacts",
		Description: "搜索微信联系人或群. 不传 keyword 则列出全部",
		InputSchema: jsonSchema(props{
			"keyword":      strProp("模糊搜索 (匹配 wxid/昵称/备注/alias/拼音首字母)"),
			"limit":        intProp("返回条数 (默认 50)"),
			"groups_only":  boolProp("仅返回群"),
			"friends_only": boolProp("仅返回好友 (排除群和公众号)"),
		}, nil),
	},
	{
		Name: "messages",
		Description: "拉取某个会话的消息. talker 是 wxid 或 xxx@chatroom. " +
			"返回字段含: local_id (会话内顺序 id), server_id (跨表 join key — " +
			"匹配 transfers/red_packets/favorites.message_server_id), local_type (raw packed int64), " +
			"base_kind (int32, 低 32 bit: 1文本/3图/34语音/43视频/47表情/49app/10000系统), " +
			"subtype (int32, 高 32 bit — base_kind=49 时 57=引用回复), " +
			"kind_name (base_kind 的人读名), message_content (raw 文本/XML), " +
			"message_content_parsed (可解则含 structured fields, 不可解则缺席, raw 始终保留). " +
			"群消息 sender_wxid 已解析. after/before 支持 unix秒或 2006-01-02 (本地时区).",
		InputSchema: jsonSchema(props{
			"talker":  strProp("会话对象 (wxid 或 xxx@chatroom)"),
			"limit":   intProp("返回条数 (默认 50)"),
			"offset":  intProp("跳过条数 (默认 0)"),
			"after":   strProp("起始时间 (unix秒 或 2006-01-02, 本地时区)"),
			"before":  strProp("截止时间 (unix秒 或 2006-01-02, 本地时区)"),
			"keyword": strProp("消息内容关键词"),
		}, []string{"talker"}),
	},
	{
		Name:        "group_members",
		Description: "列出群成员. 可选附带每人发言数统计",
		InputSchema: jsonSchema(props{
			"chatroom_id": strProp("群 ID (xxx@chatroom)"),
			"stats":       boolProp("附带每人发言条数 (扫消息表, 较慢)"),
		}, []string{"chatroom_id"}),
	},
	{
		Name:        "sns",
		Description: "朋友圈 timeline, 含点赞和评论",
		InputSchema: jsonSchema(props{
			"keyword": strProp("正文关键词"),
			"user":    strProp("按发布者 wxid 过滤"),
			"after":   strProp("起始时间 (unix秒 或 2006-01-02)"),
			"before":  strProp("截止时间 (unix秒 或 2006-01-02)"),
			"limit":   intProp("返回条数 (默认 20)"),
			"offset":  intProp("跳过条数 (默认 0)"),
		}, nil),
	},
	{
		Name: "search",
		Description: "跨会话搜索消息 (4 个 FTS content 分区 UNION ALL + 全局时间倒序). 返回 content/local_id/session_id/talker/create_time. 如果要完整 message_content_parsed, 按 talker + local_id 再调 messages.",
		InputSchema: jsonSchema(props{
			"keyword": strProp("搜索关键词"),
			"limit":   intProp("返回条数 (默认 20)"),
		}, []string{"keyword"}),
	},
	{
		Name:        "sql",
		Description: "在指定 db 上跑任意只读 SQL. subdir/file 指定数据库位置, 如 session/session.db, contact/contact.db, message/message_0.db, sns/sns.db, general/general.db, favorite/favorite.db, hardlink/hardlink.db",
		InputSchema: jsonSchema(props{
			"query":  strProp("SQL 语句"),
			"subdir": strProp("db_storage 下的子目录 (默认 session)"),
			"file":   strProp("数据库文件名 (默认 session.db)"),
		}, []string{"query"}),
	},
	{
		Name: "transfers",
		Description: "微信转账记录. message_server_id 可 join 到 messages.server_id 拿对应消息 XML (转账金额藏在那, 不在本表).",
		InputSchema: jsonSchema(props{
			"limit": intProp("返回条数 (默认 50)"),
		}, nil),
	},
	{
		Name: "red_packets",
		Description: "微信红包记录. message_server_id 可 join 到 messages.server_id 拿对应消息 XML.",
		InputSchema: jsonSchema(props{
			"limit": intProp("返回条数 (默认 50)"),
		}, nil),
	},
	{
		Name: "favorites",
		Description: "微信收藏列表. message_server_id 可 join 到 messages.server_id 拿对应消息 XML.",
		InputSchema: jsonSchema(props{
			"limit": intProp("返回条数 (默认 50)"),
		}, nil),
	},
	{
		Name:        "chatroom_announcements",
		Description: "群公告",
		InputSchema: jsonSchema(props{
			"chatroom_id": strProp("群 ID (xxx@chatroom), 不传则返回所有群公告"),
		}, nil),
	},
	{
		Name:        "forward_history",
		Description: "最近转发记录 (转发给谁, 何时)",
		InputSchema: jsonSchema(props{
			"limit": intProp("返回条数 (默认 50)"),
		}, nil),
	},
}

// ──────────────────── tool handlers ────────────────────

func (s *server) toolSessions(a map[string]any) (any, error) {
	db, err := s.openDB("session", "session.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var where []string
	var args []any
	where = append(where, "COALESCE(is_hidden, 0) = 0")
	if kw := getStr(a, "keyword"); kw != "" {
		where = append(where, "(username LIKE ? OR summary LIKE ?)")
		like := "%" + kw + "%"
		args = append(args, like, like)
	}
	args = append(args, getInt(a, "limit", 50))
	return db.Query(fmt.Sprintf(`SELECT username, type, unread_count, summary,
		last_timestamp, sort_timestamp,
		last_msg_sender, last_sender_display_name, last_msg_type
		FROM SessionTable
		WHERE %s
		ORDER BY sort_timestamp DESC
		LIMIT ?`, strings.Join(where, " AND ")), args...)
}

func (s *server) toolContacts(a map[string]any) (any, error) {
	db, err := s.openDB("contact", "contact.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var where []string
	var args []any
	if getBool(a, "groups_only") {
		where = append(where, "username LIKE '%@chatroom'")
	}
	if getBool(a, "friends_only") {
		where = append(where, "username NOT LIKE '%@chatroom' AND username NOT LIKE 'gh_%' AND username NOT LIKE '%@openim'")
	}
	if kw := getStr(a, "keyword"); kw != "" {
		where = append(where, "(username LIKE ? OR nick_name LIKE ? OR remark LIKE ? OR alias LIKE ? OR pin_yin_initial LIKE ?)")
		like := "%" + kw + "%"
		args = append(args, like, like, like, like, like)
	}
	wc := ""
	if len(where) > 0 {
		wc = "WHERE " + strings.Join(where, " AND ")
	}
	return db.Query(fmt.Sprintf(`SELECT username, alias, remark, nick_name,
		pin_yin_initial, big_head_url, small_head_url, description,
		is_in_chat_room, chat_room_type, local_type, verify_flag
		FROM contact %s
		ORDER BY is_in_chat_room DESC, nick_name
		LIMIT %d`, wc, getInt(a, "limit", 50)), args...)
}

func (s *server) toolMessages(a map[string]any) (any, error) {
	talker := getStr(a, "talker")
	if talker == "" {
		return nil, fmt.Errorf("talker is required")
	}
	tableName := "Msg_" + talkerHash(talker)
	db, err := s.findMsgDB(tableName)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var where []string
	var args []any

	if s := getStr(a, "after"); s != "" {
		ts, err := parseTS(s)
		if err != nil {
			return nil, err
		}
		where = append(where, "create_time >= ?")
		args = append(args, ts)
	}
	if s := getStr(a, "before"); s != "" {
		ts, err := parseTS(s)
		if err != nil {
			return nil, err
		}
		where = append(where, "create_time < ?")
		args = append(args, ts)
	}
	if kw := getStr(a, "keyword"); kw != "" {
		where = append(where, "message_content LIKE ?")
		args = append(args, "%"+kw+"%")
	}

	wc := ""
	if len(where) > 0 {
		wc = "WHERE " + strings.Join(where, " AND ")
	}
	limit := getInt(a, "limit", 50)
	offset := getInt(a, "offset", 0)
	args = append(args, limit, offset)

	rows, err := db.Query(fmt.Sprintf(`SELECT local_id, server_id, local_type, sort_seq,
		real_sender_id, create_time, status, message_content, source
		FROM %s %s
		ORDER BY sort_seq DESC
		LIMIT ? OFFSET ?`, tableName, wc), args...)
	if err != nil {
		return nil, err
	}
	if m, _ := loadName2Id(db); m != nil {
		rows = resolveSenders(rows, m)
	}
	return enrichMessages(decodeFields(rows, "message_content", "source")), nil
}

func (s *server) toolGroupMembers(a map[string]any) (any, error) {
	target := getStr(a, "chatroom_id")
	if target == "" {
		return nil, fmt.Errorf("chatroom_id is required")
	}
	db, err := s.openDB("contact", "contact.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT c.username, c.alias, c.remark, c.nick_name, c.big_head_url,
		CASE WHEN cr.owner = c.username THEN 1 ELSE 0 END AS is_owner
		FROM chat_room cr
		JOIN chatroom_member cm ON cm.room_id = cr.id
		JOIN contact c ON c.id = cm.member_id
		WHERE cr.username = ?
		ORDER BY COALESCE(NULLIF(c.remark, ''), c.nick_name, c.username)`, target)
	if err != nil {
		return nil, err
	}
	if !getBool(a, "stats") {
		return rows, nil
	}
	tableName := "Msg_" + talkerHash(target)
	msgDB, err := s.findMsgDB(tableName)
	if err != nil {
		return rows, nil // return without stats on error
	}
	defer msgDB.Close()
	n2i, _ := loadName2Id(msgDB)
	countRows, _ := msgDB.Query(fmt.Sprintf(
		"SELECT real_sender_id, COUNT(*) AS cnt FROM %s GROUP BY real_sender_id", tableName))
	counts := make(map[string]int64)
	for _, r := range countRows {
		id, _ := r["real_sender_id"].(int64)
		cnt, _ := r["cnt"].(int64)
		if w, ok := n2i[id]; ok {
			counts[w] = cnt
		}
	}
	for _, row := range rows {
		if u, ok := row["username"].(string); ok {
			row["msg_count"] = counts[u]
		}
	}
	return rows, nil
}

func (s *server) toolSns(a map[string]any) (any, error) {
	db, err := s.openDB("sns", "sns.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	limit := getInt(a, "limit", 20)
	offset := getInt(a, "offset", 0)
	afterTS, err := parseTS(getStr(a, "after"))
	if err != nil {
		return nil, err
	}
	beforeTS, err := parseTS(getStr(a, "before"))
	if err != nil {
		return nil, err
	}

	var where []string
	var args []any
	if u := getStr(a, "user"); u != "" {
		where = append(where, "user_name = ?")
		args = append(args, u)
	}
	if kw := getStr(a, "keyword"); kw != "" {
		where = append(where, "content LIKE ?")
		args = append(args, "%"+kw+"%")
	}
	wc := ""
	if len(where) > 0 {
		wc = "WHERE " + strings.Join(where, " AND ")
	}
	fetchLimit := limit + offset
	if afterTS > 0 || beforeTS > 0 {
		fetchLimit *= 4
	}
	if fetchLimit > 2000 {
		fetchLimit = 2000
	}

	rows, err := db.Query(
		fmt.Sprintf("SELECT tid, user_name, content FROM SnsTimeLine %s ORDER BY tid DESC LIMIT %d", wc, fetchLimit),
		args...)
	if err != nil {
		return nil, err
	}

	var posts []*snsPost
	var tids []int64
	skip := offset
	for _, r := range rows {
		raw, _ := r["content"].(string)
		p := parseSnsXML(raw)
		if p == nil {
			continue
		}
		if afterTS > 0 && p.CreateTime < afterTS {
			continue
		}
		if beforeTS > 0 && p.CreateTime >= beforeTS {
			continue
		}
		if skip > 0 {
			skip--
			continue
		}
		tid, _ := r["tid"].(int64)
		tids = append(tids, tid)
		posts = append(posts, p)
		if len(posts) >= limit {
			break
		}
	}

	if len(posts) > 0 {
		likes, comments := loadSnsInteractions(db, tids)
		for i, tid := range tids {
			posts[i].Likes = likes[tid]
			posts[i].Comments = comments[tid]
		}
	}
	return posts, nil
}

func (s *server) toolSearch(a map[string]any) (any, error) {
	kw := getStr(a, "keyword")
	if kw == "" {
		return nil, fmt.Errorf("keyword is required")
	}
	limit := getInt(a, "limit", 20)
	like := "%" + kw + "%"

	// Use FTS content tables (85万条索引, single DB) — much faster than scanning Msg_* tables.
	db, err := s.openDB("message", "message_fts.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Build session_id → talker mapping from FTS name2id.
	idToTalker := make(map[int64]string)
	if n2iRows, err := db.Query("SELECT rowid AS rid, username FROM name2id"); err == nil {
		for _, r := range n2iRows {
			if rid, ok := r["rid"].(int64); ok {
				if u, ok := r["username"].(string); ok {
					idToTalker[rid] = u
				}
			}
		}
	}

	// UNION ALL across 4 FTS content partitions then global ORDER BY.
	// Previous impl looped 0..3 and early-stopped when len(results) >= limit,
	// which could miss newer messages living in later partitions.
	// c0=text, c1=local_id, c2=sort_seq, c4=session_id, c6=create_time
	query := `SELECT * FROM (
		SELECT c0 AS content, c1 AS local_id, c4 AS session_id, c6 AS create_time FROM message_fts_v4_0_content WHERE c0 LIKE ?
		UNION ALL
		SELECT c0 AS content, c1 AS local_id, c4 AS session_id, c6 AS create_time FROM message_fts_v4_1_content WHERE c0 LIKE ?
		UNION ALL
		SELECT c0 AS content, c1 AS local_id, c4 AS session_id, c6 AS create_time FROM message_fts_v4_2_content WHERE c0 LIKE ?
		UNION ALL
		SELECT c0 AS content, c1 AS local_id, c4 AS session_id, c6 AS create_time FROM message_fts_v4_3_content WHERE c0 LIKE ?
	) ORDER BY create_time DESC LIMIT ?`
	rows, err := db.Query(query, like, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		sid, _ := r["session_id"].(int64)
		r["talker"] = idToTalker[sid]
	}
	return rows, nil
}

func (s *server) toolSQL(a map[string]any) (any, error) {
	q := getStr(a, "query")
	if q == "" {
		return nil, fmt.Errorf("query is required")
	}
	subdir := getStr(a, "subdir")
	if subdir == "" {
		subdir = "session"
	}
	file := getStr(a, "file")
	if file == "" {
		file = "session.db"
	}
	db, err := s.openDB(subdir, file)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Query(q)
}

func (s *server) toolTransfers(a map[string]any) (any, error) {
	db, err := s.openDB("general", "general.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Query(`SELECT transfer_id, transcation_id, session_name,
		pay_payer, pay_receiver, pay_sub_type,
		begin_transfer_time, invalid_time, last_modified_time,
		message_server_id
		FROM transferTable
		ORDER BY begin_transfer_time DESC
		LIMIT ?`, getInt(a, "limit", 50))
}

func (s *server) toolRedPackets(a map[string]any) (any, error) {
	db, err := s.openDB("general", "general.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Query(`SELECT send_id, sender_user_name, session_name,
		hb_type, hb_status, receive_status, scene_id,
		message_server_id
		FROM redEnvelopeTable
		ORDER BY rowid DESC
		LIMIT ?`, getInt(a, "limit", 50))
}

func (s *server) toolFavorites(a map[string]any) (any, error) {
	db, err := s.openDB("favorite", "favorite.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Query(`SELECT local_id, server_id, type, update_seq, flag,
		update_time, source_id, fromusr
		FROM fav_db_item
		ORDER BY update_seq DESC
		LIMIT ?`, getInt(a, "limit", 50))
}

func (s *server) toolChatroomAnnouncements(a map[string]any) (any, error) {
	db, err := s.openDB("contact", "contact.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if cid := getStr(a, "chatroom_id"); cid != "" {
		return db.Query(`SELECT username_, announcement_, announcement_editor_,
			announcement_publish_time_, chat_room_status_
			FROM chat_room_info_detail WHERE username_ = ?`, cid)
	}
	return db.Query(`SELECT username_ AS chatroom_id, announcement_, announcement_editor_,
		announcement_publish_time_, chat_room_status_
		FROM chat_room_info_detail
		WHERE announcement_ IS NOT NULL AND announcement_ != ''
		ORDER BY announcement_publish_time_ DESC`)
}

func (s *server) toolForwardHistory(a map[string]any) (any, error) {
	db, err := s.openDB("general", "general.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Query(`SELECT username, forward_time
		FROM ForwardRecent
		ORDER BY forward_time DESC
		LIMIT ?`, getInt(a, "limit", 50))
}

// ──────────────────── helpers ────────────────────

func talkerHash(talker string) string {
	h := md5.Sum([]byte(talker))
	return hex.EncodeToString(h[:])
}

// arg helpers
type props = map[string]any

func strProp(desc string) any  { return map[string]any{"type": "string", "description": desc} }
func intProp(desc string) any  { return map[string]any{"type": "integer", "description": desc} }
func boolProp(desc string) any { return map[string]any{"type": "boolean", "description": desc} }

func jsonSchema(properties props, required []string) any {
	s := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func getStr(a map[string]any, k string) string {
	if v, ok := a[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt(a map[string]any, k string, def int) int {
	if v, ok := a[k]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
	}
	return def
}

func getBool(a map[string]any, k string) bool {
	if v, ok := a[k]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// parseTS accepts unix seconds or local-timezone date/datetime strings.
// Empty input returns (0, nil). Invalid non-empty input returns an error
// rather than silently falling back to 0 — that would surprise the caller
// into returning unfiltered results.
func parseTS(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, fmt.Errorf("无法解析时间: %s (支持 unix秒 / 2006-01-02 / 2006-01-02T15:04:05, 本地时区)", s)
}

// ──────────────────── zstd / field decode ────────────────────

var zstdDec *zstd.Decoder

func init() {
	d, _ := zstd.NewReader(nil)
	zstdDec = d
}

func tryDecodeField(v any) any {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "KLUv/") {
			if raw, err := base64.StdEncoding.DecodeString(x); err == nil {
				if zstdDec != nil && len(raw) >= 4 && raw[0] == 0x28 && raw[1] == 0xb5 && raw[2] == 0x2f && raw[3] == 0xfd {
					if out, err := zstdDec.DecodeAll(raw, nil); err == nil {
						return string(out)
					}
				}
			}
		}
	case []byte:
		if zstdDec != nil && len(x) >= 4 && x[0] == 0x28 && x[1] == 0xb5 && x[2] == 0x2f && x[3] == 0xfd {
			if out, err := zstdDec.DecodeAll(x, nil); err == nil {
				return string(out)
			}
		}
	}
	return v
}

func decodeFields(rows []wcdb.Row, fields ...string) []wcdb.Row {
	for _, row := range rows {
		for _, f := range fields {
			if v, ok := row[f]; ok {
				row[f] = tryDecodeField(v)
			}
		}
	}
	return rows
}

func loadName2Id(db *wcdb.DB) (map[int64]string, error) {
	rows, err := db.Query("SELECT rowid AS rid, user_name FROM Name2Id")
	if err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(rows))
	for _, r := range rows {
		if id, ok := r["rid"].(int64); ok {
			if u, ok := r["user_name"].(string); ok {
				m[id] = u
			}
		}
	}
	return m, nil
}

func resolveSenders(rows []wcdb.Row, senderMap map[int64]string) []wcdb.Row {
	for _, row := range rows {
		if id, ok := row["real_sender_id"].(int64); ok {
			if wxid, ok := senderMap[id]; ok {
				row["sender_wxid"] = wxid
			}
		}
	}
	return rows
}

// ──────────────────── SNS XML parsing ────────────────────

type xmlSnsDataItem struct {
	XMLName  xml.Name      `xml:"SnsDataItem"`
	Timeline xmlTimeline   `xml:"TimelineObject"`
	Local    xmlLocalExtra `xml:"LocalExtraInfo"`
}
type xmlTimeline struct {
	ID          string     `xml:"id"`
	Username    string     `xml:"username"`
	CreateTime  int64      `xml:"createTime"`
	ContentDesc string     `xml:"contentDesc"`
	Private     int        `xml:"private"`
	Location    xmlLoc     `xml:"location"`
	Content     xmlContent `xml:"ContentObject"`
}
type xmlLoc struct {
	Lat  string `xml:"latitude,attr"`
	Lon  string `xml:"longitude,attr"`
	Name string `xml:"poiName,attr"`
}
type xmlContent struct {
	Type      int          `xml:"type"`
	MediaList xmlMediaList `xml:"mediaList"`
}
type xmlMediaList struct {
	Items []xmlMedia `xml:"media"`
}
type xmlMedia struct {
	Type  int      `xml:"type"`
	URL   string   `xml:"url"`
	Thumb string   `xml:"thumb"`
	Size  xmlMSize `xml:"size"`
}
type xmlMSize struct {
	Width  int `xml:"width,attr"`
	Height int `xml:"height,attr"`
	Total  int `xml:"totalSize,attr"`
}
type xmlLocalExtra struct {
	Nickname string `xml:"nickname"`
	LikeFlag int    `xml:"like_flag"`
}

type snsPost struct {
	TID        string     `json:"tid"`
	Username   string     `json:"username"`
	Nickname   string     `json:"nickname"`
	CreateTime int64      `json:"create_time"`
	Content    string     `json:"content"`
	Type       int        `json:"type"`
	Private    bool       `json:"private,omitempty"`
	LikedByMe bool       `json:"liked_by_me,omitempty"`
	Media      []snsMedia `json:"media,omitempty"`
	Location   *snsLoc    `json:"location,omitempty"`
	Likes      []snsReact `json:"likes,omitempty"`
	Comments   []snsCmt   `json:"comments,omitempty"`
}
type snsMedia struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	Thumb  string `json:"thumb,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}
type snsLoc struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}
type snsReact struct {
	Username string `json:"username"`
	Nickname string `json:"nickname"`
}
type snsCmt struct {
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	Content     string `json:"content"`
	CreateTime  int64  `json:"create_time"`
	ReplyTo     string `json:"reply_to,omitempty"`
	ReplyToNick string `json:"reply_to_nick,omitempty"`
}

func parseSnsXML(raw string) *snsPost {
	var item xmlSnsDataItem
	if xml.Unmarshal([]byte(raw), &item) != nil {
		return nil
	}
	t := item.Timeline
	p := &snsPost{
		TID: t.ID, Username: t.Username, Nickname: item.Local.Nickname,
		CreateTime: t.CreateTime, Content: t.ContentDesc, Type: t.Content.Type,
		Private: t.Private != 0, LikedByMe: item.Local.LikeFlag != 0,
	}
	for _, m := range t.Content.MediaList.Items {
		mt := "image"
		if m.Type != 2 {
			mt = "video"
		}
		p.Media = append(p.Media, snsMedia{
			Type: mt, URL: m.URL, Thumb: m.Thumb,
			Width: m.Size.Width, Height: m.Size.Height,
		})
	}
	lat, _ := strconv.ParseFloat(t.Location.Lat, 64)
	lon, _ := strconv.ParseFloat(t.Location.Lon, 64)
	if lat != 0 || lon != 0 || t.Location.Name != "" {
		p.Location = &snsLoc{Name: t.Location.Name, Lat: lat, Lon: lon}
	}
	return p
}

func loadSnsInteractions(db *wcdb.DB, tids []int64) (map[int64][]snsReact, map[int64][]snsCmt) {
	if len(tids) == 0 {
		return nil, nil
	}
	ph := make([]string, len(tids))
	args := make([]any, len(tids))
	for i, t := range tids {
		ph[i] = "?"
		args[i] = t
	}
	likes := make(map[int64][]snsReact)
	comments := make(map[int64][]snsCmt)
	rows, err := db.Query(
		fmt.Sprintf("SELECT feed_id, type, from_username, from_nickname, to_username, to_nickname, content, create_time FROM SnsMessage_tmp3 WHERE feed_id IN (%s) ORDER BY create_time", strings.Join(ph, ",")),
		args...)
	if err != nil {
		return likes, comments
	}
	for _, r := range rows {
		fid, _ := r["feed_id"].(int64)
		typ, _ := r["type"].(int64)
		fu, _ := r["from_username"].(string)
		fn, _ := r["from_nickname"].(string)
		switch typ {
		case 1:
			likes[fid] = append(likes[fid], snsReact{Username: fu, Nickname: fn})
		case 2:
			tu, _ := r["to_username"].(string)
			tn, _ := r["to_nickname"].(string)
			ct, _ := r["content"].(string)
			ts, _ := r["create_time"].(int64)
			c := snsCmt{Username: fu, Nickname: fn, Content: ct, CreateTime: ts}
			if tu != "" {
				c.ReplyTo = tu
				c.ReplyToNick = tn
			}
			comments[fid] = append(comments[fid], c)
		}
	}
	return likes, comments
}

// ──────────────────── message_content enrichment ────────────────────

// local_type is a packed int64: (subtype << 32) | base_kind.
// base_kind names: 1 text, 3 image, 34 voice, 43 video, 47 sticker, 49 app, 10000 system.
// subtype is meaningful for base_kind=49 (app messages) — e.g. 57=quote-reply (<refermsg>).
var baseKindNames = map[int32]string{
	1:     "text",
	3:     "image",
	34:    "voice",
	43:    "video",
	47:    "sticker",
	49:    "app",
	10000: "system",
}

func unpackLocalType(lt int64) (baseKind, subtype int32, name string) {
	baseKind = int32(lt & 0xFFFFFFFF)
	subtype = int32(lt >> 32)
	if n, ok := baseKindNames[baseKind]; ok {
		name = n
	} else {
		name = "unknown"
	}
	return
}

type xmlMsgImg struct {
	XMLName xml.Name `xml:"msg"`
	Img     struct {
		AesKey       string `xml:"aeskey,attr"`
		Length       int64  `xml:"length,attr"`
		HdLength     int64  `xml:"hdlength,attr"`
		MD5          string `xml:"md5,attr"`
		CdnMidURL    string `xml:"cdnmidimgurl,attr"`
		CdnBigURL    string `xml:"cdnbigimgurl,attr"`
		CdnThumbURL  string `xml:"cdnthumburl,attr"`
		CdnHdHeight  int    `xml:"cdnhdheight,attr"`
		CdnHdWidth   int    `xml:"cdnhdwidth,attr"`
		CdnMidHeight int    `xml:"cdnmidheight,attr"`
		CdnMidWidth  int    `xml:"cdnmidwidth,attr"`
	} `xml:"img"`
}

type xmlMsgEmoji struct {
	XMLName xml.Name `xml:"msg"`
	Emoji   struct {
		AesKey     string `xml:"aeskey,attr"`
		MD5        string `xml:"md5,attr"`
		CdnURL     string `xml:"cdnurl,attr"`
		EncryptURL string `xml:"encrypturl,attr"`
		Width      int    `xml:"width,attr"`
		Height     int    `xml:"height,attr"`
		Type       int    `xml:"type,attr"`
	} `xml:"emoji"`
}

type xmlMsgAppmsg struct {
	XMLName xml.Name `xml:"msg"`
	AppMsg  struct {
		Title    string          `xml:"title"`
		Des      string          `xml:"des"`
		URL      string          `xml:"url"`
		Type     int             `xml:"type"`
		ReferMsg *xmlMsgReferMsg `xml:"refermsg"`
	} `xml:"appmsg"`
}

type xmlMsgReferMsg struct {
	ChatUsr     string `xml:"chatusr"`
	Type        int    `xml:"type"`
	CreateTime  int64  `xml:"createtime"`
	DisplayName string `xml:"displayname"`
	SvrID       string `xml:"svrid"`
	FromUsr     string `xml:"fromusr"`
	Content     string `xml:"content"`
}

// stripMsgPrefix trims the "wxid_xxx:\n" sender prefix WeChat prepends to
// group message content so xml.Unmarshal sees a clean XML document.
func stripMsgPrefix(raw string) string {
	if idx := strings.Index(raw, "<"); idx > 0 {
		return raw[idx:]
	}
	return raw
}

// parseMessageContent returns a structured JSON-serializable value for supported
// (base_kind, subtype). Returns nil for unsupported kinds or parse failures —
// raw message_content is always retained in the row so no information is lost.
// Depth bounds recursion for nested refermsg content.
func parseMessageContent(baseKind, subtype int32, raw string, depth int) any {
	if depth <= 0 || raw == "" {
		return nil
	}
	xmlStr := stripMsgPrefix(raw)
	switch baseKind {
	case 3:
		var m xmlMsgImg
		if xml.Unmarshal([]byte(xmlStr), &m) != nil {
			return nil
		}
		return map[string]any{
			"md5":           m.Img.MD5,
			"length":        m.Img.Length,
			"hd_length":     m.Img.HdLength,
			"aeskey":        m.Img.AesKey,
			"cdn_mid_url":   m.Img.CdnMidURL,
			"cdn_big_url":   m.Img.CdnBigURL,
			"cdn_thumb_url": m.Img.CdnThumbURL,
			"hd_width":      m.Img.CdnHdWidth,
			"hd_height":     m.Img.CdnHdHeight,
			"mid_width":     m.Img.CdnMidWidth,
			"mid_height":    m.Img.CdnMidHeight,
		}
	case 47:
		var m xmlMsgEmoji
		if xml.Unmarshal([]byte(xmlStr), &m) != nil {
			return nil
		}
		return map[string]any{
			"aeskey":      m.Emoji.AesKey,
			"md5":         m.Emoji.MD5,
			"cdn_url":     m.Emoji.CdnURL,
			"encrypt_url": m.Emoji.EncryptURL,
			"width":       m.Emoji.Width,
			"height":      m.Emoji.Height,
			"type":        m.Emoji.Type,
		}
	case 49:
		var m xmlMsgAppmsg
		if xml.Unmarshal([]byte(xmlStr), &m) != nil {
			return nil
		}
		out := map[string]any{
			"app_subtype": m.AppMsg.Type,
			"title":       m.AppMsg.Title,
			"des":         m.AppMsg.Des,
			"url":         m.AppMsg.URL,
		}
		if m.AppMsg.ReferMsg != nil {
			r := m.AppMsg.ReferMsg
			refer := map[string]any{
				"chatusr":     r.ChatUsr,
				"type":        r.Type,
				"createtime":  r.CreateTime,
				"displayname": r.DisplayName,
				"svrid":       r.SvrID,
				"fromusr":     r.FromUsr,
				"content_raw": r.Content,
			}
			if parsed := parseMessageContent(int32(r.Type), 0, r.Content, depth-1); parsed != nil {
				refer["content_parsed"] = parsed
			}
			out["refermsg"] = refer
		}
		return out
	}
	return nil
}

// enrichMessages augments raw message rows with packed-type decoding and a
// structured message_content_parsed sibling field. Raw local_type and
// message_content are always preserved.
func enrichMessages(rows []wcdb.Row) []wcdb.Row {
	for _, row := range rows {
		lt, ok := row["local_type"].(int64)
		if !ok {
			continue
		}
		baseKind, subtype, name := unpackLocalType(lt)
		row["base_kind"] = baseKind
		row["subtype"] = subtype
		row["kind_name"] = name
		if content, ok := row["message_content"].(string); ok {
			if parsed := parseMessageContent(baseKind, subtype, content, 3); parsed != nil {
				row["message_content_parsed"] = parsed
			}
		}
	}
	return rows
}
