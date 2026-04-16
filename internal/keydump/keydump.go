package keydump

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultTimeoutMs = 90_000

type result struct {
	Success bool   `json:"success"`
	Result  string `json:"result"`
	Key     string `json:"key"`
}

func FindWeChatPID() (int, error) {
	out, err := exec.Command("pgrep", "-x", "WeChat").Output()
	if err != nil {
		return 0, errors.New("微信进程未运行 — 请先启动并登录微信")
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if line == "" {
		return 0, errors.New("微信进程未找到")
	}
	return strconv.Atoi(line)
}

func HelperPath(libDir string) string {
	return filepath.Join(libDir, "xkey_helper")
}

func ActivateWeChat() {
	_ = exec.Command("/usr/bin/osascript", "-e", `tell application "WeChat" to activate`).Run()
}

type Status struct{ Stage string }

func Dump(helperPath string, pid, timeoutMs int, status func(Status)) (string, error) {
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if status != nil {
		status(Status{Stage: "需要管理员权限, 可能弹出密码框 (若最近输过密码则跳过)..."})
	}
	return dumpElevated(helperPath, pid, timeoutMs, status)
}

func dumpDirect(helperPath string, pid, timeoutMs int, status func(Status)) (string, error) {
	cmd := exec.Command(helperPath, strconv.Itoa(pid), strconv.Itoa(timeoutMs))
	return runWithProgress(cmd, status)
}

func dumpElevated(helperPath string, pid, timeoutMs int, status func(Status)) (string, error) {
	shellCmd := fmt.Sprintf("%s %d %d 2>&1", shellQuote(helperPath), pid, timeoutMs)
	script := fmt.Sprintf(`do shell script %s with administrator privileges`, appleQuote(shellCmd))
	cmd := exec.Command("/usr/bin/osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("osascript elevated 调用失败: %w\n%s", err, string(out))
	}
	return parseResult(out)
}

func runWithProgress(cmd *exec.Cmd, status func(Status)) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if status == nil {
				continue
			}
			if strings.Contains(line, "attach") && strings.Contains(line, "ok") {
				status(Status{Stage: "已附加微信进程, 准备安装 hook..."})
			} else if strings.Contains(line, "Hook installed") || strings.Contains(line, "breakpoint set") {
				status(Status{Stage: "Hook 已安装, 请在微信里点开一个聊天并滚动历史消息..."})
			} else if strings.Contains(line, "breakpoint hit") || strings.Contains(line, "HOOK_TRIGGERED") {
				status(Status{Stage: "触发成功, 正在读取密钥..."})
			}
		}
	}()

	stdoutBytes, _ := io.ReadAll(stdout)
	waitErr := cmd.Wait()
	<-done

	if waitErr != nil {
		if len(bytes.TrimSpace(stdoutBytes)) == 0 {
			return "", fmt.Errorf("xkey_helper 异常退出: %w", waitErr)
		}
	}
	return parseResult(stdoutBytes)
}

func parseResult(raw []byte) (string, error) {
	idx := bytes.LastIndex(raw, []byte(`{"success"`))
	if idx < 0 {
		return "", fmt.Errorf("xkey_helper 未返回有效 JSON (raw: %s)", truncate(raw, 500))
	}
	end := bytes.IndexByte(raw[idx:], '}')
	if end < 0 {
		return "", fmt.Errorf("xkey_helper JSON 不完整 (raw: %s)", truncate(raw[idx:], 300))
	}
	lastJSON := raw[idx : idx+end+1]
	var r result
	if err := json.Unmarshal(lastJSON, &r); err != nil {
		return "", fmt.Errorf("解析 xkey_helper 输出失败: %w", err)
	}
	if !r.Success {
		msg := r.Result
		if msg == "" {
			msg = string(lastJSON)
		}
		return "", helperError(msg)
	}
	for _, cand := range []string{r.Key, r.Result} {
		cand = strings.TrimSpace(cand)
		if cand != "" && !strings.HasPrefix(cand, "ERROR:") {
			return cand, nil
		}
	}
	return "", errors.New(ErrHookTimeout)
}

const (
	ErrHookTimeout  = "hook_timeout"
	ErrAttachFailed = "attach_failed"
	ErrScanFailed   = "scan_failed"
	ErrUnknown      = "helper_unknown"
)

type HelperErr struct{ Kind, Raw string }

func (e *HelperErr) Error() string { return e.Raw }

func helperError(raw string) error {
	switch {
	case strings.Contains(raw, "HOOK_TIMEOUT"), strings.Contains(raw, "no_breakpoint_hit"):
		return &HelperErr{Kind: ErrHookTimeout, Raw: raw}
	case strings.Contains(raw, "task_for_pid"), strings.Contains(raw, "ATTACH_FAILED"), strings.Contains(raw, "attach_wait_timeout"):
		return &HelperErr{Kind: ErrAttachFailed, Raw: raw}
	case strings.Contains(raw, "SCAN_FAILED"), strings.Contains(raw, "Sink pattern not found"):
		return &HelperErr{Kind: ErrScanFailed, Raw: raw}
	}
	return &HelperErr{Kind: ErrUnknown, Raw: raw}
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var he *HelperErr
	if errors.As(err, &he) {
		return he.Kind == ErrAttachFailed
	}
	if os.IsPermission(err) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "task_for_pid") || strings.Contains(s, "os/kern") || strings.Contains(s, "not permitted")
}

func SudoAvailable() bool {
	return exec.Command("sudo", "-n", "true").Run() == nil
}

func DebugDump(helperPath string, pid, timeoutMs int, mode string) error {
	var cmd *exec.Cmd
	switch mode {
	case "sudo":
		cmd = exec.Command("sudo", "-n", helperPath, strconv.Itoa(pid), strconv.Itoa(timeoutMs))
	case "osascript":
		shellCmd := fmt.Sprintf("%s %d %d 2>&1", shellQuote(helperPath), pid, timeoutMs)
		script := fmt.Sprintf(`do shell script %s with administrator privileges`, appleQuote(shellCmd))
		cmd = exec.Command("/usr/bin/osascript", "-e", script)
	default:
		cmd = exec.Command(helperPath, strconv.Itoa(pid), strconv.Itoa(timeoutMs))
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Fprintln(os.Stderr, "===== BEGIN xkey_helper RAW OUTPUT =====")
	err := cmd.Run()
	fmt.Fprintln(os.Stderr, "\n===== END =====")
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper exit: %v\n", err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
