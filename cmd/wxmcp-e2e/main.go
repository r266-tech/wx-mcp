// Standalone test: open every WeChat DB using only the new schema-2 keys map
// from ~/.config/wxcli/config.json + wx-mcp's wcdb package. Proves the
// self-contained decrypt path actually works end-to-end.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/r266-tech/wx-mcp/internal/config"
	"github.com/r266-tech/wx-mcp/internal/wcdb"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fail("config.Load: %v", err)
	}
	pretty, _ := json.MarshalIndent(struct {
		Schema int    `json:"schema_version"`
		WxID   string `json:"wxid"`
		Root   string `json:"db_root"`
		Keys   int    `json:"keys_count"`
		Legacy bool   `json:"legacy_master_password_present"`
	}{cfg.SchemaVersion, cfg.Wxid, cfg.DBRoot, len(cfg.Keys), cfg.Key != ""}, "", "  ")
	fmt.Println("config snapshot:")
	fmt.Println(string(pretty))

	if !cfg.Ready() {
		fail("config not ready (no keys map and no legacy key)")
	}
	if len(cfg.Keys) == 0 {
		fail("schema-2 keys map is empty — schema-1 fallback would still work but isn't what we're testing")
	}

	// libWCDB.dylib path: same resolution as wx-mcp main.go (repo-bundled lib/).
	wcdbPath := "lib/libWCDB.dylib"
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(wcdbPath); err != nil {
			alt := filepath.Join(home, ".config", "wxcli", "lib", "libWCDB.dylib")
			if _, err2 := os.Stat(alt); err2 == nil {
				wcdbPath = alt
			}
		}
	}
	if _, err := os.Stat(wcdbPath); err != nil {
		fail("libWCDB.dylib not found at %s: %v", wcdbPath, err)
	}
	if err := wcdb.Bootstrap(wcdbPath); err != nil {
		fail("wcdb.Bootstrap: %v", err)
	}

	dbStorage := filepath.Join(cfg.DBRoot, "db_storage")
	type result struct {
		Path     string
		Salt     string
		HasKey   bool
		OpenedOK bool
		Rows     int64
		Err      string
		Elapsed  time.Duration
	}
	var results []result

	err = filepath.Walk(dbStorage, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".db") {
			return nil
		}
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") {
			return nil
		}
		rel, _ := filepath.Rel(dbStorage, p)
		r := result{Path: rel}
		start := time.Now()

		f, err := os.Open(p)
		if err != nil {
			r.Err = err.Error()
			r.Elapsed = time.Since(start)
			results = append(results, r)
			return nil
		}
		salt := make([]byte, 16)
		_, _ = f.Read(salt)
		f.Close()
		r.Salt = fmt.Sprintf("%x", salt)
		_, r.HasKey = cfg.Keys[r.Salt]

		db, err := wcdb.OpenWithKeyMap(p, cfg.Keys, cfg.Key)
		if err != nil {
			r.Err = err.Error()
			r.Elapsed = time.Since(start)
			results = append(results, r)
			return nil
		}
		r.OpenedOK = true
		// run a trivial query to prove the key actually decrypts
		rows, qerr := db.Query("SELECT count(*) AS c FROM sqlite_master")
		if qerr != nil {
			r.Err = "query: " + qerr.Error()
		} else if len(rows) > 0 {
			if v, ok := rows[0]["c"].(int64); ok {
				r.Rows = v
			}
		}
		db.Close()
		r.Elapsed = time.Since(start)
		results = append(results, r)
		return nil
	})
	if err != nil {
		fail("walk: %v", err)
	}

	fmt.Println("\nresults:")
	openCount, failCount := 0, 0
	for _, r := range results {
		if r.OpenedOK && r.Err == "" {
			openCount++
			fmt.Printf("  OK    %-50s salt=%s tables=%d (%v)\n",
				r.Path, r.Salt[:8]+"...", r.Rows, r.Elapsed.Round(time.Millisecond))
		} else {
			failCount++
			missing := ""
			if !r.HasKey {
				missing = " [no key in config]"
			}
			fmt.Printf("  FAIL  %-50s salt=%s%s err=%s (%v)\n",
				r.Path, r.Salt[:8]+"...", missing, r.Err, r.Elapsed.Round(time.Millisecond))
		}
	}
	fmt.Printf("\nsummary: %d opened, %d failed (out of %d)\n", openCount, failCount, len(results))
	if failCount > 0 {
		os.Exit(1)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[e2e] FAIL: "+format+"\n", args...)
	os.Exit(1)
}
