package weflow

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	safePrefix    = "safe:"
	inspectorAddr = "127.0.0.1:9229"
)

type Config struct {
	DecryptKey string `json:"decryptKey"`
	MyWxid     string `json:"myWxid"`
	DbPath     string `json:"dbPath"`
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "WeFlow", "WeFlow-config.json"), nil
}

func Available() bool {
	p, err := configPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func readConfig() (*Config, error) {
	p, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("WeFlow 未装或未运行过 (%s 不存在): %w", p, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 WeFlow config 失败: %w", err)
	}
	return &cfg, nil
}

// findWeFlowPID finds the main WeFlow Electron process (not helpers).
func findWeFlowPID() (int, error) {
	out, err := exec.Command("pgrep", "-x", "WeFlow").Output()
	if err != nil {
		return 0, errors.New("WeFlow 未运行 — 请先启动 WeFlow 桌面端")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			return pid, nil
		}
	}
	return 0, errors.New("WeFlow 未运行 — 请先启动 WeFlow 桌面端")
}

// inspectorWsURL checks if the V8 inspector is listening and returns the WS URL.
func inspectorWsURL() (string, error) {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + inspectorAddr + "/json/list")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var targets []struct {
		WsURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", err
	}
	if len(targets) == 0 {
		return "", errors.New("no inspector targets")
	}
	return targets[0].WsURL, nil
}

// decryptSafeStorage decrypts a "safe:..." value by evaluating
// safeStorage.decryptString() inside the running WeFlow process
// via the V8 inspector (SIGUSR1 → CDP WebSocket → Runtime.evaluate).
func decryptSafeStorage(encWithPrefix string) (string, error) {
	if !strings.HasPrefix(encWithPrefix, safePrefix) {
		return "", fmt.Errorf("decryptKey 无 '%s' 前缀", safePrefix)
	}

	pid, err := findWeFlowPID()
	if err != nil {
		return "", err
	}

	// Check if inspector is already active.
	wsURL, err := inspectorWsURL()
	weToggled := false
	if err != nil {
		// Activate inspector via SIGUSR1.
		if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
			return "", fmt.Errorf("SIGUSR1 发送失败: %w", err)
		}
		weToggled = true

		// Poll until ready (up to 3 s).
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
			wsURL, err = inspectorWsURL()
			if err == nil {
				break
			}
		}
		if err != nil {
			return "", fmt.Errorf("V8 inspector 启动超时: %w", err)
		}
	}

	// Turn inspector back off when done (if we turned it on).
	if weToggled {
		defer syscall.Kill(pid, syscall.SIGUSR1)
	}

	b64Part := strings.TrimPrefix(encWithPrefix, safePrefix)
	jsExpr := fmt.Sprintf(
		`(function(){try{var e=process.mainModule?process.mainModule.require('electron'):require('electron');var b=Buffer.from('%s','base64');return e.safeStorage.decryptString(b)}catch(x){return'\x00ERR:'+x.message}})()`,
		b64Part,
	)

	result, err := cdpEval(wsURL, jsExpr)
	if err != nil {
		return "", fmt.Errorf("CDP eval 失败: %w", err)
	}

	if strings.HasPrefix(result, "\x00ERR:") {
		return "", fmt.Errorf("safeStorage.decryptString 失败: %s", result[5:])
	}

	return result, nil
}

type Import struct {
	HexKey string
	Wxid   string
	DBRoot string
}

func ImportKey() (*Import, error) {
	cfg, err := readConfig()
	if err != nil {
		return nil, err
	}
	if cfg.DecryptKey == "" {
		return nil, errors.New("WeFlow 还未抓到 key — 请先打开 WeFlow, 让它成功连接一次微信, 然后再跑 wxcli setup")
	}
	if cfg.MyWxid == "" {
		return nil, errors.New("WeFlow config 缺 myWxid")
	}

	plaintext, err := decryptSafeStorage(cfg.DecryptKey)
	if err != nil {
		return nil, err
	}

	dbPath := cfg.DbPath
	if dbPath == "" {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat", "Data", "Documents", "xwechat_files")
	}

	return &Import{
		HexKey: plaintext,
		Wxid:   cfg.MyWxid,
		DBRoot: filepath.Join(dbPath, cfg.MyWxid),
	}, nil
}

// --------------- minimal CDP-over-WebSocket client ---------------

// cdpEval connects to the V8 inspector WebSocket, sends a
// Runtime.evaluate command, and returns the string result.
func cdpEval(wsURL, expr string) (string, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return "", err
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":9229"
	}

	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	// --- WebSocket upgrade handshake ---
	nonce := make([]byte, 16)
	rand.Read(nonce)
	wsKey := base64.StdEncoding.EncodeToString(nonce)

	reqLine := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		u.RequestURI(), u.Host, wsKey)
	if _, err := io.WriteString(conn, reqLine); err != nil {
		return "", err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return "", fmt.Errorf("WebSocket 握手失败: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 101 {
		return "", fmt.Errorf("WebSocket 握手状态码 %d", resp.StatusCode)
	}

	// --- Send Runtime.evaluate ---
	exprJSON, _ := json.Marshal(expr)
	cdpMsg := fmt.Sprintf(`{"id":1,"method":"Runtime.evaluate","params":{"expression":%s,"returnByValue":true}}`, exprJSON)
	if err := wsWrite(conn, []byte(cdpMsg)); err != nil {
		return "", err
	}

	// --- Read response ---
	payload, err := wsRead(br)
	if err != nil {
		return "", err
	}

	var cdpResp struct {
		ID     int `json:"id"`
		Result struct {
			Result struct {
				Type  string `json:"type"`
				Value any    `json:"value"`
			} `json:"result"`
			ExceptionDetails *struct {
				Text string `json:"text"`
			} `json:"exceptionDetails"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &cdpResp); err != nil {
		return "", fmt.Errorf("CDP 响应解析失败: %w", err)
	}
	if ed := cdpResp.Result.ExceptionDetails; ed != nil {
		return "", fmt.Errorf("JS 异常: %s", ed.Text)
	}
	switch v := cdpResp.Result.Result.Value.(type) {
	case string:
		return v, nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// wsWrite sends a masked text frame.
func wsWrite(w io.Writer, data []byte) error {
	var hdr []byte
	length := len(data)
	if length < 126 {
		hdr = []byte{0x81, byte(0x80 | length)}
	} else if length < 65536 {
		hdr = []byte{0x81, 0x80 | 126, byte(length >> 8), byte(length & 0xff)}
	} else {
		hdr = make([]byte, 10)
		hdr[0] = 0x81
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(length))
	}

	mask := make([]byte, 4)
	rand.Read(mask)
	hdr = append(hdr, mask...)

	masked := make([]byte, length)
	for i := range data {
		masked[i] = data[i] ^ mask[i%4]
	}

	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(masked)
	return err
}

// wsRead reads one unmasked frame (server-to-client).
func wsRead(r *bufio.Reader) ([]byte, error) {
	b0, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	b1, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	_ = b0 // opcode in low nibble, FIN in high bit — we just need payload
	length := uint64(b1 & 0x7f)

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}

	if length > 4*1024*1024 {
		return nil, fmt.Errorf("WebSocket 帧过大: %d bytes", length)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
