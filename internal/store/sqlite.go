package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"unsafe"
)

type Store struct {
	db           *C.sqlite3
	accessMu     sync.RWMutex
	beforeCommit func() error // test failure/blocking hook; nil in production
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var db *C.sqlite3
	if rc := C.sqlite3_open_v2(cpath, &db, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_CREATE|C.SQLITE_OPEN_FULLMUTEX, nil); rc != C.SQLITE_OK {
		msg := "sqlite open failed"
		if db != nil {
			msg = C.GoString(C.sqlite3_errmsg(db))
			C.sqlite3_close(db)
		}
		return nil, fmt.Errorf("sqlite: %s", msg)
	}
	s := &Store{db: db}
	for _, q := range []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA busy_timeout=5000;`,
		`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS media (kind TEXT NOT NULL, source_id INTEGER NOT NULL, service_id TEXT NOT NULL DEFAULT '', payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(kind,source_id,service_id));`,
		`CREATE TABLE IF NOT EXISTS torrents (client TEXT NOT NULL, hash TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(client,hash));`,
		`CREATE TABLE IF NOT EXISTS unmanaged_files (path TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS files (path TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS media_files (kind TEXT NOT NULL, source_id INTEGER NOT NULL, source TEXT NOT NULL, source_file_id INTEGER NOT NULL, path TEXT NOT NULL, service_id TEXT NOT NULL DEFAULT '', service_name TEXT NOT NULL DEFAULT '', PRIMARY KEY(kind,source_id,source,source_file_id));`,
		`CREATE INDEX IF NOT EXISTS idx_media_files_media ON media_files(kind,source_id);`,
		`CREATE INDEX IF NOT EXISTS idx_media_files_path ON media_files(path);`,
		`CREATE TABLE IF NOT EXISTS media_file_parts (kind TEXT NOT NULL, source_id INTEGER NOT NULL, source TEXT NOT NULL, source_file_id INTEGER NOT NULL, part_group TEXT NOT NULL DEFAULT '', part_label TEXT NOT NULL DEFAULT '', part_order INTEGER NOT NULL DEFAULT 0, source_part_id INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(kind,source_id,source,source_file_id,source_part_id));`,
		`CREATE INDEX IF NOT EXISTS idx_media_file_parts_file ON media_file_parts(kind,source_id,source,source_file_id);`,
		`CREATE TABLE IF NOT EXISTS torrent_files (client TEXT NOT NULL, hash TEXT NOT NULL, file_index INTEGER NOT NULL, path TEXT NOT NULL, service_id TEXT NOT NULL DEFAULT '', service_name TEXT NOT NULL DEFAULT '', PRIMARY KEY(client,hash,file_index));`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_files_hash ON torrent_files(client,hash);`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_files_path ON torrent_files(path);`,
		`CREATE TABLE IF NOT EXISTS arr_imports (source TEXT NOT NULL, owner_id INTEGER NOT NULL, sub_id INTEGER NOT NULL DEFAULT 0, download_id TEXT NOT NULL, imported_at TEXT NOT NULL, service_id TEXT NOT NULL DEFAULT '', PRIMARY KEY(source,service_id,owner_id,sub_id,download_id,imported_at));`,
		`CREATE INDEX IF NOT EXISTS idx_arr_imports_source_owner ON arr_imports(source,service_id,owner_id,sub_id,imported_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_arr_imports_hash ON arr_imports(download_id);`,
		`CREATE TABLE IF NOT EXISTS history_events (id INTEGER PRIMARY KEY AUTOINCREMENT, event_type TEXT NOT NULL, status TEXT NOT NULL, dry_run INTEGER NOT NULL DEFAULT 1, requested_kind TEXT NOT NULL DEFAULT '', requested_key TEXT NOT NULL DEFAULT '', requested_label TEXT NOT NULL DEFAULT '', reclaimable_bytes INTEGER NOT NULL DEFAULT 0, payload BLOB NOT NULL DEFAULT '{}', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_history_events_created ON history_events(created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS cleanup_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, started_at TEXT NOT NULL, completed_at TEXT NOT NULL, usage_before REAL NOT NULL, usage_after REAL NOT NULL, target_usage REAL NOT NULL, critical_usage REAL NOT NULL, planned_bytes INTEGER NOT NULL DEFAULT 0, media_bytes INTEGER NOT NULL DEFAULT 0, reclaimed_bytes INTEGER NOT NULL DEFAULT 0, media_removed INTEGER NOT NULL DEFAULT 0, torrents_removed INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '');`,
		`CREATE TABLE IF NOT EXISTS cleanup_actions (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id INTEGER NOT NULL, kind TEXT NOT NULL, source_id INTEGER NOT NULL DEFAULT 0, title TEXT NOT NULL DEFAULT '', value REAL NOT NULL DEFAULT 0, media_bytes INTEGER NOT NULL DEFAULT 0, reclaimed_bytes INTEGER NOT NULL DEFAULT 0, torrent_hash TEXT NOT NULL DEFAULT '', action TEXT NOT NULL, FOREIGN KEY(run_id) REFERENCES cleanup_runs(id));`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_runs_completed ON cleanup_runs(completed_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_actions_run ON cleanup_actions(run_id);`,
		`CREATE TABLE IF NOT EXISTS sessions (token TEXT PRIMARY KEY, created_at TEXT NOT NULL, expires_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);`,
		`CREATE TABLE IF NOT EXISTS torrent_history (client TEXT NOT NULL, hash TEXT NOT NULL, sampled_at TEXT NOT NULL, ratio REAL NOT NULL, seeds_swarm INTEGER NOT NULL, leechers_swarm INTEGER NOT NULL, uploaded_bytes INTEGER NOT NULL, downloaded_bytes INTEGER NOT NULL, state TEXT NOT NULL, last_activity INTEGER NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_history_hash_time ON torrent_history(client,hash,sampled_at);`,
	} {
		if err := s.exec(q); err != nil {
			s.Close()
			return nil, err
		}
	}
	for _, migration := range []struct{ table, column, definition string }{
		{"media_files", "service_id", "TEXT NOT NULL DEFAULT ''"},
		{"media_files", "service_name", "TEXT NOT NULL DEFAULT ''"},
		{"torrent_files", "service_id", "TEXT NOT NULL DEFAULT ''"},
		{"torrent_files", "service_name", "TEXT NOT NULL DEFAULT ''"},
		{"history_events", "media_bytes", "INTEGER NOT NULL DEFAULT 0"},
		{"history_events", "service_name", "TEXT NOT NULL DEFAULT ''"},
		{"arr_imports", "service_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(migration.table, migration.column, migration.definition); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) ensureColumn(table, column, definition string) error {
	st, err := s.prepare(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			C.sqlite3_finalize(st)
			return s.err(rc)
		}
		if colText(st, 1) == column {
			found = true
			break
		}
	}
	C.sqlite3_finalize(st)
	if found {
		return nil
	}
	return s.exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if s.db == nil {
		return nil
	}
	if rc := C.sqlite3_close(s.db); rc != C.SQLITE_OK {
		return s.err(rc)
	}
	s.db = nil
	return nil
}

func (s *Store) err(rc C.int) error {
	return fmt.Errorf("sqlite rc=%d: %s", int(rc), C.GoString(C.sqlite3_errmsg(s.db)))
}

func (s *Store) exec(sql string) error {
	c := C.CString(sql)
	defer C.free(unsafe.Pointer(c))
	var em *C.char
	rc := C.sqlite3_exec(s.db, c, nil, nil, &em)
	if rc != C.SQLITE_OK {
		var msg string
		if em != nil {
			msg = C.GoString(em)
			C.sqlite3_free(unsafe.Pointer(em))
		} else {
			msg = C.GoString(C.sqlite3_errmsg(s.db))
		}
		return fmt.Errorf("sqlite: %s", msg)
	}
	return nil
}

func (s *Store) prepare(sql string) (*C.sqlite3_stmt, error) {
	c := C.CString(sql)
	defer C.free(unsafe.Pointer(c))
	var st *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(s.db, c, -1, &st, nil); rc != C.SQLITE_OK {
		return nil, s.err(rc)
	}
	return st, nil
}

func bindText(st *C.sqlite3_stmt, idx int, v string) C.int {
	c := C.CString(v)
	defer C.free(unsafe.Pointer(c))
	return C.sqlite3_bind_text(st, C.int(idx), c, C.int(len(v)), (*[0]byte)(C.SQLITE_TRANSIENT))
}

func bindInt(st *C.sqlite3_stmt, idx int, v int64) C.int {
	return C.sqlite3_bind_int64(st, C.int(idx), C.sqlite3_int64(v))
}

func colText(st *C.sqlite3_stmt, idx int) string {
	p := C.sqlite3_column_text(st, C.int(idx))
	if p == nil {
		return ""
	}
	return C.GoString((*C.char)(unsafe.Pointer(p)))
}

func stepDone(s *Store, st *C.sqlite3_stmt) error {
	if rc := C.sqlite3_step(st); rc != C.SQLITE_DONE {
		return s.err(rc)
	}
	return nil
}

// withWriteTx publishes a logical snapshot atomically. The caller must own
// accessMu exclusively so no reader on the shared connection can observe the
// DELETE/INSERT replacement while it is in progress.
func (s *Store) withWriteTx(fn func() error) error {
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.exec("ROLLBACK")
		}
	}()
	if err := fn(); err != nil {
		return err
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			return err
		}
	}
	if err := s.exec("COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) SetMeta(key, value string) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.setMeta(key, value)
}

// setMeta writes metadata while the caller already owns accessMu.
func (s *Store) setMeta(key, value string) error {
	st, e := s.prepare(`INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, key)
	bindText(st, 2, value)
	return stepDone(s, st)
}

func (s *Store) Meta(key string) (string, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	return s.meta(key)
}

func (s *Store) meta(key string) (string, error) {
	st, e := s.prepare(`SELECT value FROM metadata WHERE key=?`)
	if e != nil {
		return "", e
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, key)
	rc := C.sqlite3_step(st)
	if rc == C.SQLITE_DONE {
		return "", nil
	}
	if rc != C.SQLITE_ROW {
		return "", s.err(rc)
	}
	return colText(st, 0), nil
}

func (s *Store) MetaInt64(key string) (int64, error) {
	v, e := s.Meta(key)
	if e != nil || v == "" {
		return 0, e
	}
	return strconv.ParseInt(v, 10, 64)
}

func (s *Store) SetMetaInt64(key string, v int64) error {
	return s.SetMeta(key, strconv.FormatInt(v, 10))
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
