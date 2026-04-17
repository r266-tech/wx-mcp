package wcdb

import (
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	SQLITE_OK            = 0
	SQLITE_ROW           = 100
	SQLITE_DONE          = 101
	SQLITE_OPEN_READONLY = 0x00000001

	COL_INT    = 1
	COL_FLOAT  = 2
	COL_TEXT   = 3
	COL_BLOB   = 4
	COL_NULL   = 5
)

var (
	mu     sync.Mutex
	loaded bool

	sqlite3_open_v2      func(filename string, ppDb *uintptr, flags int32, vfs *byte) int32
	sqlite3_close_v2     func(db uintptr) int32
	sqlite3_key_v2       func(db uintptr, zDbName string, pKey unsafe.Pointer, nKey int32) int32
	sqlite3_exec         func(db uintptr, sql string, cb uintptr, arg uintptr, errmsg *uintptr) int32
	sqlite3_prepare_v2   func(db uintptr, sql string, nByte int32, stmt *uintptr, tail *uintptr) int32
	sqlite3_step         func(stmt uintptr) int32
	sqlite3_finalize     func(stmt uintptr) int32
	sqlite3_column_count func(stmt uintptr) int32
	sqlite3_column_name  func(stmt uintptr, i int32) uintptr
	sqlite3_column_text  func(stmt uintptr, i int32) uintptr
	sqlite3_column_int64 func(stmt uintptr, i int32) int64
	sqlite3_column_bytes func(stmt uintptr, i int32) int32
	sqlite3_column_blob  func(stmt uintptr, i int32) uintptr
	sqlite3_column_type  func(stmt uintptr, i int32) int32
	sqlite3_bind_text    func(stmt uintptr, i int32, s string, n int32, destructor uintptr) int32
	sqlite3_bind_int64   func(stmt uintptr, i int32, v int64) int32
	sqlite3_errmsg       func(db uintptr) uintptr
)

// Bootstrap loads the WCDB dylib from the given absolute path.
func Bootstrap(dylibPath string) error {
	mu.Lock()
	defer mu.Unlock()
	if loaded {
		return nil
	}
	h, err := purego.Dlopen(dylibPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("dlopen %s: %w", dylibPath, err)
	}
	for _, reg := range []struct {
		fn   any
		name string
	}{
		{&sqlite3_open_v2, "sqlite3_open_v2"},
		{&sqlite3_close_v2, "sqlite3_close_v2"},
		{&sqlite3_key_v2, "sqlite3_key_v2"},
		{&sqlite3_exec, "sqlite3_exec"},
		{&sqlite3_prepare_v2, "sqlite3_prepare_v2"},
		{&sqlite3_step, "sqlite3_step"},
		{&sqlite3_finalize, "sqlite3_finalize"},
		{&sqlite3_column_count, "sqlite3_column_count"},
		{&sqlite3_column_name, "sqlite3_column_name"},
		{&sqlite3_column_text, "sqlite3_column_text"},
		{&sqlite3_column_int64, "sqlite3_column_int64"},
		{&sqlite3_column_bytes, "sqlite3_column_bytes"},
		{&sqlite3_column_blob, "sqlite3_column_blob"},
		{&sqlite3_column_type, "sqlite3_column_type"},
		{&sqlite3_bind_text, "sqlite3_bind_text"},
		{&sqlite3_bind_int64, "sqlite3_bind_int64"},
		{&sqlite3_errmsg, "sqlite3_errmsg"},
	} {
		purego.RegisterLibFunc(reg.fn, h, reg.name)
	}
	loaded = true
	return nil
}

type DB struct {
	handle uintptr
	path   string
}

// Open opens a WCDB-encrypted SQLite file with the legacy "master password"
// path: hexKey is the 64-hex 32-byte password originally fetched from
// WeFlow's safeStorage. SQLCipher runs PBKDF2-HMAC-SHA512 (256000 rounds) on
// every open to derive the per-DB enc_key — slow but compatible with old
// configs that lack a per-salt key map.
func Open(dbPath string, hexKey string) (*DB, error) {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex key: %w", err)
	}
	if len(keyBytes) == 0 {
		return nil, fmt.Errorf("empty key")
	}
	return openWithKeyBlob(dbPath, keyBytes)
}

// OpenWithEncKey opens a WCDB-encrypted SQLite file using the raw-key path:
// encKeyHex is the 64-hex post-PBKDF2 enc_key (as harvested by `wxkey scan`)
// and saltHex is the 32-hex SQLCipher salt taken from the file's first 16
// bytes. Skips PBKDF2 entirely; opening cost drops to a couple of HMACs.
func OpenWithEncKey(dbPath, encKeyHex, saltHex string) (*DB, error) {
	if len(encKeyHex) != 64 {
		return nil, fmt.Errorf("OpenWithEncKey: enc_key must be 64 hex (got %d)", len(encKeyHex))
	}
	if len(saltHex) != 32 {
		return nil, fmt.Errorf("OpenWithEncKey: salt must be 32 hex (got %d)", len(saltHex))
	}
	// Build "x'<64hex><32hex>'" — SQLCipher's raw-key SQL literal form.
	blob := []byte("x'" + encKeyHex + saltHex + "'")
	return openWithKeyBlob(dbPath, blob)
}

// OpenWithKeyMap opens dbPath after reading the SQLCipher salt from the file
// header (first 16 bytes) and looking up the matching enc_key in keys
// (salt-hex → enc_key-hex). If the salt isn't in the map and fallbackHexKey
// is non-empty, falls back to the schema-1 master-password path (slow:
// triggers WCDB's internal 256000-round PBKDF2 on each open). This handles
// message-shard DBs whose enc_keys never landed in WeChat's heap because
// WeChat hasn't touched that shard this session.
//
// fallbackHexKey may be the empty string to disable the fallback.
func OpenWithKeyMap(dbPath string, keys map[string]string, fallbackHexKey string) (*DB, error) {
	salt, err := readDBSalt(dbPath)
	if err != nil {
		return nil, err
	}
	saltHex := hex.EncodeToString(salt)
	if encKeyHex, ok := keys[saltHex]; ok {
		return OpenWithEncKey(dbPath, encKeyHex, saltHex)
	}
	if fallbackHexKey == "" {
		return nil, fmt.Errorf("no enc_key for salt %s in %s and no fallback master password — re-run `wxkey setup` after touching this DB in WeChat", saltHex, dbPath)
	}
	return Open(dbPath, fallbackHexKey)
}

func openWithKeyBlob(dbPath string, blob []byte) (*DB, error) {
	var h uintptr
	if rc := sqlite3_open_v2(dbPath, &h, SQLITE_OPEN_READONLY, nil); rc != SQLITE_OK {
		return nil, fmt.Errorf("sqlite3_open_v2(%s) rc=%d: %s", dbPath, rc, errmsg(h))
	}
	if rc := sqlite3_key_v2(h, "main", unsafe.Pointer(&blob[0]), int32(len(blob))); rc != SQLITE_OK {
		sqlite3_close_v2(h)
		return nil, fmt.Errorf("sqlite3_key_v2 rc=%d", rc)
	}
	return &DB{handle: h, path: dbPath}, nil
}

// readDBSalt reads the first 16 bytes of dbPath — these are the SQLCipher
// salt and uniquely identify which enc_key opens the file.
func readDBSalt(dbPath string) ([]byte, error) {
	f, err := os.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer f.Close()
	salt := make([]byte, 16)
	n, err := f.Read(salt)
	if err != nil || n != 16 {
		return nil, fmt.Errorf("read salt %s: %w (n=%d)", dbPath, err, n)
	}
	return salt, nil
}

func (d *DB) Close() {
	if d == nil || d.handle == 0 {
		return
	}
	sqlite3_close_v2(d.handle)
	d.handle = 0
}

func (d *DB) Exec(sql string) error {
	var errPtr uintptr
	if rc := sqlite3_exec(d.handle, sql, 0, 0, &errPtr); rc != SQLITE_OK {
		return fmt.Errorf("exec rc=%d: %s", rc, readCString(errPtr))
	}
	return nil
}

type Row map[string]any

func (d *DB) Query(sql string, args ...any) ([]Row, error) {
	var stmt uintptr
	if rc := sqlite3_prepare_v2(d.handle, sql, -1, &stmt, nil); rc != SQLITE_OK {
		return nil, fmt.Errorf("prepare rc=%d: %s (sql=%s)", rc, errmsg(d.handle), sql)
	}
	defer sqlite3_finalize(stmt)

	for i, a := range args {
		idx := int32(i + 1)
		switch v := a.(type) {
		case string:
			sqlite3_bind_text(stmt, idx, v, int32(len(v)), ^uintptr(0))
		case int:
			sqlite3_bind_int64(stmt, idx, int64(v))
		case int64:
			sqlite3_bind_int64(stmt, idx, v)
		default:
			return nil, fmt.Errorf("unsupported bind type %T at arg %d", a, i)
		}
	}

	ncol := sqlite3_column_count(stmt)
	names := make([]string, ncol)
	for i := int32(0); i < ncol; i++ {
		names[i] = readCString(sqlite3_column_name(stmt, i))
	}

	var rows []Row
	for {
		rc := sqlite3_step(stmt)
		if rc == SQLITE_DONE {
			break
		}
		if rc != SQLITE_ROW {
			return nil, fmt.Errorf("step rc=%d: %s", rc, errmsg(d.handle))
		}
		row := make(Row, ncol)
		for i := int32(0); i < ncol; i++ {
			row[names[i]] = readColumn(stmt, i)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readColumn(stmt uintptr, i int32) any {
	switch sqlite3_column_type(stmt, i) {
	case COL_INT:
		return sqlite3_column_int64(stmt, i)
	case COL_TEXT:
		return readCString(sqlite3_column_text(stmt, i))
	case COL_BLOB:
		n := sqlite3_column_bytes(stmt, i)
		if n == 0 {
			return []byte{}
		}
		p := sqlite3_column_blob(stmt, i)
		b := make([]byte, n)
		copy(b, unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
		return b
	case COL_NULL:
		return nil
	}
	return nil
}

func readCString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for {
		b := *(*byte)(unsafe.Pointer(p + uintptr(n)))
		if b == 0 {
			break
		}
		n++
		if n > 10_000_000 {
			break
		}
	}
	if n == 0 {
		return ""
	}
	return string(unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
}

func errmsg(db uintptr) string {
	if db == 0 {
		return ""
	}
	return readCString(sqlite3_errmsg(db))
}
