package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Wxid     string `json:"wxid"`
	Key      string `json:"key"`
	DBRoot   string `json:"db_root"`
	KeyPID   int    `json:"key_pid,omitempty"`
	KeyEpoch int64  `json:"key_epoch,omitempty"`
}

func dir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(h, ".config", "wxcli")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

func Path() (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func Save(c *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func DefaultWeChatBase() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "Library", "Containers", "com.tencent.xinWeChat", "Data", "Documents", "xwechat_files"), nil
}

func AutoDetectDBRoot() (string, string, error) {
	base, err := DefaultWeChatBase()
	if err != nil {
		return "", "", err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", "", fmt.Errorf("WeChat data dir not found at %s (is WeChat 4.x installed and logged in?): %w", base, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		switch name {
		case "all_users", "applet", "backup", "wmpf":
			continue
		}
		full := filepath.Join(base, name)
		if _, err := os.Stat(filepath.Join(full, "db_storage")); err == nil {
			wxid := name
			if idx := lastIndex(name, "_"); idx > 0 {
				wxid = name[:idx]
			}
			return full, wxid, nil
		}
	}
	return "", "", fmt.Errorf("no account directory with db_storage found under %s", base)
}

func lastIndex(s, sep string) int {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
