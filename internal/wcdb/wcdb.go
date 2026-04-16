package wcdb

import (
	"encoding/hex"
	"fmt"
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

func Open(dbPath string, hexKey string) (*DB, error) {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex key: %w", err)
	}
	if len(keyBytes) == 0 {
		return nil, fmt.Errorf("empty key")
	}

	var h uintptr
	if rc := sqlite3_open_v2(dbPath, &h, SQLITE_OPEN_READONLY, nil); rc != SQLITE_OK {
		return nil, fmt.Errorf("sqlite3_open_v2(%s) rc=%d: %s", dbPath, rc, errmsg(h))
	}

	if rc := sqlite3_key_v2(h, "main", unsafe.Pointer(&keyBytes[0]), int32(len(keyBytes))); rc != SQLITE_OK {
		sqlite3_close_v2(h)
		return nil, fmt.Errorf("sqlite3_key_v2 rc=%d", rc)
	}

	return &DB{handle: h, path: dbPath}, nil
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
