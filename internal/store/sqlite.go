package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"spartarr/internal/model"
)

type Store struct{ db *C.sqlite3 }

type ImportEvent struct {
	Source     string
	OwnerID    int
	SubID      int
	DownloadID string
	ImportedAt time.Time
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
		`CREATE TABLE IF NOT EXISTS warriors (kind TEXT NOT NULL, source_id INTEGER NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(kind,source_id));`,
		`CREATE TABLE IF NOT EXISTS portals (client TEXT NOT NULL, hash TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(client,hash));`,
		`CREATE TABLE IF NOT EXISTS arr_imports (source TEXT NOT NULL, owner_id INTEGER NOT NULL, sub_id INTEGER NOT NULL DEFAULT 0, download_id TEXT NOT NULL, imported_at TEXT NOT NULL, PRIMARY KEY(source,owner_id,sub_id,download_id,imported_at));`,
		`CREATE INDEX IF NOT EXISTS idx_arr_imports_source_owner ON arr_imports(source,owner_id,sub_id,imported_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_arr_imports_hash ON arr_imports(download_id);`,
		`CREATE TABLE IF NOT EXISTS cleanup_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, started_at TEXT NOT NULL, completed_at TEXT NOT NULL, usage_before REAL NOT NULL, usage_after REAL NOT NULL, target_usage REAL NOT NULL, critical_usage REAL NOT NULL, planned_bytes INTEGER NOT NULL DEFAULT 0, media_bytes INTEGER NOT NULL DEFAULT 0, reclaimed_bytes INTEGER NOT NULL DEFAULT 0, media_removed INTEGER NOT NULL DEFAULT 0, torrents_removed INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '');`,
		`CREATE TABLE IF NOT EXISTS cleanup_actions (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id INTEGER NOT NULL, kind TEXT NOT NULL, source_id INTEGER NOT NULL DEFAULT 0, title TEXT NOT NULL DEFAULT '', strength REAL NOT NULL DEFAULT 0, media_bytes INTEGER NOT NULL DEFAULT 0, reclaimed_bytes INTEGER NOT NULL DEFAULT 0, torrent_hash TEXT NOT NULL DEFAULT '', action TEXT NOT NULL, FOREIGN KEY(run_id) REFERENCES cleanup_runs(id));`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_runs_completed ON cleanup_runs(completed_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_actions_run ON cleanup_actions(run_id);`,
	} {
		if err := s.exec(q); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
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

func (s *Store) SetMeta(key, value string) error {
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

func (s *Store) SaveMedia(items []model.Media) error {
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = s.exec("ROLLBACK")
		}
	}()
	if err := s.exec("DELETE FROM warriors"); err != nil {
		return err
	}
	st, e := s.prepare(`INSERT INTO warriors(kind,source_id,payload,updated_at) VALUES(?,?,?,?)`)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, m := range items {
		b, e := json.Marshal(m)
		if e != nil {
			return e
		}
		C.sqlite3_reset(st)
		C.sqlite3_clear_bindings(st)
		bindText(st, 1, string(m.Type))
		bindInt(st, 2, int64(m.SourceID))
		bindText(st, 3, string(b))
		bindText(st, 4, now)
		if e := stepDone(s, st); e != nil {
			return e
		}
	}
	if err := s.exec("COMMIT"); err != nil {
		return err
	}
	ok = true
	return s.SetMeta("warriors.updated_at", now)
}
func (s *Store) LoadMedia() ([]model.Media, time.Time, error) {
	st, e := s.prepare(`SELECT payload FROM warriors`)
	if e != nil {
		return nil, time.Time{}, e
	}
	defer C.sqlite3_finalize(st)
	var out []model.Media
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, time.Time{}, s.err(rc)
		}
		var m model.Media
		if e := json.Unmarshal([]byte(colText(st, 0)), &m); e != nil {
			return nil, time.Time{}, e
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Strength != out[j].Strength {
			return out[i].Strength < out[j].Strength
		}
		return out[i].SizeBytes > out[j].SizeBytes
	})
	ts, _ := s.Meta("warriors.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}

func (s *Store) SaveTorrents(items []model.Torrent) error {
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = s.exec("ROLLBACK")
		}
	}()
	if err := s.exec("DELETE FROM portals"); err != nil {
		return err
	}
	st, e := s.prepare(`INSERT INTO portals(client,hash,payload,updated_at) VALUES(?,?,?,?)`)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, p := range items {
		b, e := json.Marshal(p)
		if e != nil {
			return e
		}
		C.sqlite3_reset(st)
		C.sqlite3_clear_bindings(st)
		bindText(st, 1, p.Client)
		bindText(st, 2, strings.ToLower(p.Hash))
		bindText(st, 3, string(b))
		bindText(st, 4, now)
		if e := stepDone(s, st); e != nil {
			return e
		}
	}
	if err := s.exec("COMMIT"); err != nil {
		return err
	}
	ok = true
	return s.SetMeta("portals.updated_at", now)
}
func (s *Store) LoadTorrents() ([]model.Torrent, error) {
	st, e := s.prepare(`SELECT payload FROM portals`)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	var out []model.Torrent
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		var p model.Torrent
		if e := json.Unmarshal([]byte(colText(st, 0)), &p); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) AddImportEvents(events []ImportEvent) error {
	if len(events) == 0 {
		return nil
	}
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = s.exec("ROLLBACK")
		}
	}()
	st, e := s.prepare(`INSERT OR IGNORE INTO arr_imports(source,owner_id,sub_id,download_id,imported_at) VALUES(?,?,?,?,?)`)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	for _, x := range events {
		C.sqlite3_reset(st)
		C.sqlite3_clear_bindings(st)
		bindText(st, 1, x.Source)
		bindInt(st, 2, int64(x.OwnerID))
		bindInt(st, 3, int64(x.SubID))
		bindText(st, 4, strings.ToLower(strings.TrimSpace(x.DownloadID)))
		bindText(st, 5, x.ImportedAt.UTC().Format(time.RFC3339Nano))
		if e := stepDone(s, st); e != nil {
			return e
		}
	}
	if err := s.exec("COMMIT"); err != nil {
		return err
	}
	ok = true
	return nil
}
func (s *Store) ImportEvents(source string) ([]ImportEvent, error) {
	st, e := s.prepare(`SELECT owner_id,sub_id,download_id,imported_at FROM arr_imports WHERE source=? ORDER BY imported_at DESC`)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, source)
	var out []ImportEvent
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		t, _ := time.Parse(time.RFC3339Nano, colText(st, 3))
		out = append(out, ImportEvent{Source: source, OwnerID: int(C.sqlite3_column_int64(st, 0)), SubID: int(C.sqlite3_column_int64(st, 1)), DownloadID: colText(st, 2), ImportedAt: t})
	}
	return out, nil
}
func (s *Store) AllImportHashes() (map[string]bool, error) {
	st, e := s.prepare(`SELECT DISTINCT download_id FROM arr_imports`)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	out := map[string]bool{}
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		out[strings.ToLower(colText(st, 0))] = true
	}
	return out, nil
}

type CleanupStats struct {
	Runs            int64
	MediaRemoved    int64
	TorrentsRemoved int64
	MediaBytes      int64
	ReclaimedBytes  int64
	Last30Runs      int64
	Last30Media     int64
	Last30Bytes     int64
}

type CleanupRun struct {
	ID              int64
	StartedAt       time.Time
	CompletedAt     time.Time
	UsageBefore     float64
	UsageAfter      float64
	TargetUsage     float64
	CriticalUsage   float64
	PlannedBytes    int64
	MediaBytes      int64
	ReclaimedBytes  int64
	MediaRemoved    int64
	TorrentsRemoved int64
	Status          string
	Reason          string
}

func (s *Store) CleanupStatistics() (CleanupStats, error) {
	var out CleanupStats
	st, e := s.prepare(`SELECT COUNT(*), COALESCE(SUM(media_removed),0), COALESCE(SUM(torrents_removed),0), COALESCE(SUM(media_bytes),0), COALESCE(SUM(reclaimed_bytes),0) FROM cleanup_runs WHERE status='completed'`)
	if e != nil {
		return out, e
	}
	rc := C.sqlite3_step(st)
	if rc == C.SQLITE_ROW {
		out.Runs = int64(C.sqlite3_column_int64(st, 0))
		out.MediaRemoved = int64(C.sqlite3_column_int64(st, 1))
		out.TorrentsRemoved = int64(C.sqlite3_column_int64(st, 2))
		out.MediaBytes = int64(C.sqlite3_column_int64(st, 3))
		out.ReclaimedBytes = int64(C.sqlite3_column_int64(st, 4))
	} else if rc != C.SQLITE_DONE {
		C.sqlite3_finalize(st)
		return out, s.err(rc)
	}
	C.sqlite3_finalize(st)
	st, e = s.prepare(`SELECT COUNT(*), COALESCE(SUM(media_removed),0), COALESCE(SUM(reclaimed_bytes),0) FROM cleanup_runs WHERE status='completed' AND completed_at >= datetime('now','-30 days')`)
	if e != nil {
		return out, e
	}
	defer C.sqlite3_finalize(st)
	rc = C.sqlite3_step(st)
	if rc == C.SQLITE_ROW {
		out.Last30Runs = int64(C.sqlite3_column_int64(st, 0))
		out.Last30Media = int64(C.sqlite3_column_int64(st, 1))
		out.Last30Bytes = int64(C.sqlite3_column_int64(st, 2))
	} else if rc != C.SQLITE_DONE {
		return out, s.err(rc)
	}
	return out, nil
}

func (s *Store) CleanupRuns(limit int) ([]CleanupRun, error) {
	if limit <= 0 {
		limit = 50
	}
	st, e := s.prepare(`SELECT id,started_at,completed_at,usage_before,usage_after,target_usage,critical_usage,planned_bytes,media_bytes,reclaimed_bytes,media_removed,torrents_removed,status,reason FROM cleanup_runs ORDER BY completed_at DESC LIMIT ?`)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	bindInt(st, 1, int64(limit))
	var out []CleanupRun
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		started, _ := time.Parse(time.RFC3339Nano, colText(st, 1))
		completed, _ := time.Parse(time.RFC3339Nano, colText(st, 2))
		out = append(out, CleanupRun{
			ID: int64(C.sqlite3_column_int64(st, 0)), StartedAt: started, CompletedAt: completed,
			UsageBefore: float64(C.sqlite3_column_double(st, 3)), UsageAfter: float64(C.sqlite3_column_double(st, 4)),
			TargetUsage: float64(C.sqlite3_column_double(st, 5)), CriticalUsage: float64(C.sqlite3_column_double(st, 6)),
			PlannedBytes: int64(C.sqlite3_column_int64(st, 7)), MediaBytes: int64(C.sqlite3_column_int64(st, 8)), ReclaimedBytes: int64(C.sqlite3_column_int64(st, 9)),
			MediaRemoved: int64(C.sqlite3_column_int64(st, 10)), TorrentsRemoved: int64(C.sqlite3_column_int64(st, 11)), Status: colText(st, 12), Reason: colText(st, 13),
		})
	}
	return out, nil
}

func (s *Store) RecordCleanupRun(r CleanupRun) (int64, error) {
	st, e := s.prepare(`INSERT INTO cleanup_runs(started_at,completed_at,usage_before,usage_after,target_usage,critical_usage,planned_bytes,media_bytes,reclaimed_bytes,media_removed,torrents_removed,status,reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if e != nil {
		return 0, e
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, r.StartedAt.UTC().Format(time.RFC3339Nano))
	bindText(st, 2, r.CompletedAt.UTC().Format(time.RFC3339Nano))
	C.sqlite3_bind_double(st, 3, C.double(r.UsageBefore))
	C.sqlite3_bind_double(st, 4, C.double(r.UsageAfter))
	C.sqlite3_bind_double(st, 5, C.double(r.TargetUsage))
	C.sqlite3_bind_double(st, 6, C.double(r.CriticalUsage))
	bindInt(st, 7, r.PlannedBytes)
	bindInt(st, 8, r.MediaBytes)
	bindInt(st, 9, r.ReclaimedBytes)
	bindInt(st, 10, r.MediaRemoved)
	bindInt(st, 11, r.TorrentsRemoved)
	bindText(st, 12, r.Status)
	bindText(st, 13, r.Reason)
	if e := stepDone(s, st); e != nil {
		return 0, e
	}
	return int64(C.sqlite3_last_insert_rowid(s.db)), nil
}
