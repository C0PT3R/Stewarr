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
	"sync"
	"time"
	"unsafe"

	"connarr/internal/model"
)

type Store struct {
	db           *C.sqlite3
	accessMu     sync.RWMutex
	beforeCommit func() error // test failure/blocking hook; nil in production
}

type HistoryEvent struct {
	ID               int64  `json:"id"`
	EventType        string `json:"eventType"`
	Status           string `json:"status"`
	DryRun           bool   `json:"dryRun"`
	RequestedKind    string `json:"requestedKind"`
	RequestedKey     string `json:"requestedKey"`
	RequestedLabel   string `json:"requestedLabel"`
	ReclaimableBytes int64  `json:"reclaimableBytes"`
	// MediaBytes is the raw sum of every selected file's own size, before
	// hardlink deduplication — "Library bytes removed". It is deliberately
	// tracked separately from ReclaimableBytes ("actual filesystem bytes
	// reclaimed"), since hardlinks mean those are not necessarily equal.
	MediaBytes int64           `json:"mediaBytes"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
}

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
		`CREATE TABLE IF NOT EXISTS unmanaged_files (path TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS files (path TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS media_files (kind TEXT NOT NULL, source_id INTEGER NOT NULL, source TEXT NOT NULL, source_file_id INTEGER NOT NULL, path TEXT NOT NULL, integration_id TEXT NOT NULL DEFAULT '', integration_name TEXT NOT NULL DEFAULT '', PRIMARY KEY(kind,source_id,source,source_file_id));`,
		`CREATE INDEX IF NOT EXISTS idx_media_files_media ON media_files(kind,source_id);`,
		`CREATE INDEX IF NOT EXISTS idx_media_files_path ON media_files(path);`,
		`CREATE TABLE IF NOT EXISTS media_file_parts (kind TEXT NOT NULL, source_id INTEGER NOT NULL, source TEXT NOT NULL, source_file_id INTEGER NOT NULL, part_group TEXT NOT NULL DEFAULT '', part_label TEXT NOT NULL DEFAULT '', part_order INTEGER NOT NULL DEFAULT 0, source_part_id INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(kind,source_id,source,source_file_id,source_part_id));`,
		`CREATE INDEX IF NOT EXISTS idx_media_file_parts_file ON media_file_parts(kind,source_id,source,source_file_id);`,
		`CREATE TABLE IF NOT EXISTS torrent_files (client TEXT NOT NULL, hash TEXT NOT NULL, file_index INTEGER NOT NULL, path TEXT NOT NULL, integration_id TEXT NOT NULL DEFAULT '', integration_name TEXT NOT NULL DEFAULT '', PRIMARY KEY(client,hash,file_index));`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_files_hash ON torrent_files(client,hash);`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_files_path ON torrent_files(path);`,
		`CREATE TABLE IF NOT EXISTS arr_imports (source TEXT NOT NULL, owner_id INTEGER NOT NULL, sub_id INTEGER NOT NULL DEFAULT 0, download_id TEXT NOT NULL, imported_at TEXT NOT NULL, PRIMARY KEY(source,owner_id,sub_id,download_id,imported_at));`,
		`CREATE INDEX IF NOT EXISTS idx_arr_imports_source_owner ON arr_imports(source,owner_id,sub_id,imported_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_arr_imports_hash ON arr_imports(download_id);`,
		`CREATE TABLE IF NOT EXISTS history_events (id INTEGER PRIMARY KEY AUTOINCREMENT, event_type TEXT NOT NULL, status TEXT NOT NULL, dry_run INTEGER NOT NULL DEFAULT 1, requested_kind TEXT NOT NULL DEFAULT '', requested_key TEXT NOT NULL DEFAULT '', requested_label TEXT NOT NULL DEFAULT '', reclaimable_bytes INTEGER NOT NULL DEFAULT 0, payload BLOB NOT NULL DEFAULT '{}', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_history_events_created ON history_events(created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS cleanup_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, started_at TEXT NOT NULL, completed_at TEXT NOT NULL, usage_before REAL NOT NULL, usage_after REAL NOT NULL, target_usage REAL NOT NULL, critical_usage REAL NOT NULL, planned_bytes INTEGER NOT NULL DEFAULT 0, media_bytes INTEGER NOT NULL DEFAULT 0, reclaimed_bytes INTEGER NOT NULL DEFAULT 0, media_removed INTEGER NOT NULL DEFAULT 0, torrents_removed INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '');`,
		`CREATE TABLE IF NOT EXISTS cleanup_actions (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id INTEGER NOT NULL, kind TEXT NOT NULL, source_id INTEGER NOT NULL DEFAULT 0, title TEXT NOT NULL DEFAULT '', value REAL NOT NULL DEFAULT 0, media_bytes INTEGER NOT NULL DEFAULT 0, reclaimed_bytes INTEGER NOT NULL DEFAULT 0, torrent_hash TEXT NOT NULL DEFAULT '', action TEXT NOT NULL, FOREIGN KEY(run_id) REFERENCES cleanup_runs(id));`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_runs_completed ON cleanup_runs(completed_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_actions_run ON cleanup_actions(run_id);`,
	} {
		if err := s.exec(q); err != nil {
			s.Close()
			return nil, err
		}
	}
	for _, migration := range []struct{ table, column, definition string }{
		{"media_files", "integration_id", "TEXT NOT NULL DEFAULT ''"},
		{"media_files", "integration_name", "TEXT NOT NULL DEFAULT ''"},
		{"torrent_files", "integration_id", "TEXT NOT NULL DEFAULT ''"},
		{"torrent_files", "integration_name", "TEXT NOT NULL DEFAULT ''"},
		{"history_events", "media_bytes", "INTEGER NOT NULL DEFAULT 0"},
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

func (s *Store) SaveMedia(items []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.saveMedia(items) })
}

func (s *Store) saveMedia(items []model.Media) error {
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
	return s.setMeta("warriors.updated_at", now)
}
func (s *Store) LoadMedia() ([]model.Media, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
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
		if out[i].RetentionValue != out[j].RetentionValue {
			return out[i].RetentionValue < out[j].RetentionValue
		}
		return out[i].SizeBytes > out[j].SizeBytes
	})
	ts, _ := s.meta("warriors.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}

func (s *Store) SaveTorrents(items []model.Torrent) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.saveTorrents(items) })
}

func (s *Store) saveTorrents(items []model.Torrent) error {
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
		// Persist only the torrent index Connarr needs for lists, valuation,
		// relationships and storage work. Client-owned diagnostics are lazy.
		p.Tracker = ""
		p.TotalSizeBytes = 0
		p.CompletedBytes = 0
		p.AmountLeftBytes = 0
		p.DownloadedBytes = 0
		p.UploadedBytes = 0
		p.DownloadedSession = 0
		p.UploadedSession = 0
		p.DownloadLimit = 0
		p.UploadLimit = 0
		p.MaxRatio = 0
		p.Availability = 0
		p.AddedOn = 0
		p.CompletionOn = 0
		p.SeenComplete = 0
		p.TimeActive = 0
		p.SeedingTime = 0
		p.ETA = 0
		p.Reannounce = 0
		p.ForceStart = false
		p.AutoTMM = false
		p.Sequential = false
		p.SuperSeeding = false
		p.Private = false
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
	return s.setMeta("portals.updated_at", now)
}
func (s *Store) LoadTorrents() ([]model.Torrent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
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
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
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
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
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
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
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
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	var out CleanupStats
	st, e := s.prepare(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN requested_kind IN ('media','season','bundle') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN requested_kind='torrent' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(media_bytes),0), COALESCE(SUM(reclaimable_bytes),0)
		FROM history_events WHERE event_type='removal' AND status='success'`)
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
	st, e = s.prepare(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN requested_kind IN ('media','season','bundle') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(reclaimable_bytes),0)
		FROM history_events WHERE event_type='removal' AND status='success' AND created_at >= datetime('now','-30 days')`)
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
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
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
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
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

func (s *Store) SaveUnmanagedFiles(items []model.UnmanagedFile) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.saveUnmanagedFiles(items) })
}

func (s *Store) saveUnmanagedFiles(items []model.UnmanagedFile) error {
	if err := s.exec("DELETE FROM unmanaged_files"); err != nil {
		return err
	}
	st, e := s.prepare(`INSERT INTO unmanaged_files(path,payload,updated_at) VALUES(?,?,?)`)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, f := range items {
		b, e := json.Marshal(f)
		if e != nil {
			return e
		}
		C.sqlite3_reset(st)
		C.sqlite3_clear_bindings(st)
		bindText(st, 1, f.Path)
		bindText(st, 2, string(b))
		bindText(st, 3, now)
		if e := stepDone(s, st); e != nil {
			return e
		}
	}
	return s.setMeta("unmanaged.updated_at", now)
}

func (s *Store) LoadUnmanagedFiles() ([]model.UnmanagedFile, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	st, e := s.prepare(`SELECT payload FROM unmanaged_files`)
	if e != nil {
		return nil, time.Time{}, e
	}
	defer C.sqlite3_finalize(st)
	var out []model.UnmanagedFile
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, time.Time{}, s.err(rc)
		}
		var f model.UnmanagedFile
		if e := json.Unmarshal([]byte(colText(st, 0)), &f); e != nil {
			return nil, time.Time{}, e
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	ts, _ := s.meta("unmanaged.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}

func (s *Store) ReplaceFiles(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.replaceFiles(files, mediaRefs, torrentRefs) })
}

func (s *Store) replaceFiles(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) error {
	for _, q := range []string{"DELETE FROM files", "DELETE FROM media_file_parts", "DELETE FROM media_files", "DELETE FROM torrent_files"} {
		if err := s.exec(q); err != nil {
			return err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	fst, err := s.prepare(`INSERT INTO files(path,payload,updated_at) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(fst)
	for _, f := range files {
		b, err := json.Marshal(f)
		if err != nil {
			return err
		}
		C.sqlite3_reset(fst)
		C.sqlite3_clear_bindings(fst)
		bindText(fst, 1, filepath.Clean(f.Path))
		bindText(fst, 2, string(b))
		bindText(fst, 3, now)
		if err := stepDone(s, fst); err != nil {
			return err
		}
	}
	mst, err := s.prepare(`INSERT INTO media_files(kind,source_id,source,source_file_id,path,integration_id,integration_name) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(mst)
	pst, err := s.prepare(`INSERT INTO media_file_parts(kind,source_id,source,source_file_id,part_group,part_label,part_order,source_part_id) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(pst)
	for _, r := range mediaRefs {
		C.sqlite3_reset(mst)
		C.sqlite3_clear_bindings(mst)
		bindText(mst, 1, string(r.MediaType))
		bindInt(mst, 2, int64(r.MediaID))
		bindText(mst, 3, r.Source)
		bindInt(mst, 4, int64(r.SourceFileID))
		bindText(mst, 5, filepath.Clean(r.Path))
		bindText(mst, 6, r.IntegrationID)
		bindText(mst, 7, r.IntegrationName)
		if err := stepDone(s, mst); err != nil {
			return err
		}
		for _, part := range r.Parts {
			C.sqlite3_reset(pst)
			C.sqlite3_clear_bindings(pst)
			bindText(pst, 1, string(r.MediaType))
			bindInt(pst, 2, int64(r.MediaID))
			bindText(pst, 3, r.Source)
			bindInt(pst, 4, int64(r.SourceFileID))
			bindText(pst, 5, part.Group)
			bindText(pst, 6, part.Label)
			bindInt(pst, 7, int64(part.Order))
			bindInt(pst, 8, int64(part.SourcePartID))
			if err := stepDone(s, pst); err != nil {
				return err
			}
		}
	}
	tst, err := s.prepare(`INSERT INTO torrent_files(client,hash,file_index,path,integration_id,integration_name) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(tst)
	for _, r := range torrentRefs {
		C.sqlite3_reset(tst)
		C.sqlite3_clear_bindings(tst)
		bindText(tst, 1, r.Client)
		bindText(tst, 2, strings.ToLower(r.Hash))
		bindInt(tst, 3, int64(r.FileIndex))
		bindText(tst, 4, filepath.Clean(r.Path))
		bindText(tst, 5, r.IntegrationID)
		bindText(tst, 6, r.IntegrationName)
		if err := stepDone(s, tst); err != nil {
			return err
		}
	}
	return s.setMeta("files.updated_at", now)
}

// PublishReconciliation commits every projection derived by one filesystem
// generation together. A crash or error therefore leaves the previous complete
// generation intact instead of mixing new topology with old Unmanaged, Media,
// or Torrent rows.
func (s *Store) PublishReconciliation(generation uint64, files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef, unmanaged []model.UnmanagedFile, torrents []model.Torrent, media []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		if err := s.replaceFiles(files, mediaRefs, torrentRefs); err != nil {
			return err
		}
		if err := s.saveUnmanagedFiles(unmanaged); err != nil {
			return err
		}
		if err := s.saveTorrents(torrents); err != nil {
			return err
		}
		if err := s.saveMedia(media); err != nil {
			return err
		}
		return s.setMeta("generation.files", strconv.FormatUint(generation, 10))
	})
}

type MediaIdentity struct {
	Kind     model.MediaType
	SourceID int
}

type ReconciliationDelta struct {
	Paths []string
	Files []model.File
	// MediaOwners are fully re-published: every existing row for that owner is
	// deleted before MediaRefs is inserted. Omit an owner here (rather than
	// including it with no matching MediaRefs) to leave its rows untouched.
	MediaOwners []MediaIdentity
	MediaRefs   []model.MediaFileRef
	// RemovedTorrentHashes are fully re-published the same way: every existing
	// torrent_files row for that hash is deleted, then TorrentRefs is
	// inserted. A hash present here with no matching TorrentRefs is removed
	// outright rather than replaced.
	RemovedTorrentHashes []string
	TorrentRefs          []model.TorrentFileRef
	Unmanaged            []model.UnmanagedFile
	Torrents             []model.Torrent
	Media                []model.Media
	Generation           uint64
	ScopeMetadataKey     string
}

// PublishReconciliationDelta commits only topology rows affected by a known
// mutation. Global Media/Torrent projections are still published atomically
// because their values and relationship explanations may depend on those rows.
func (s *Store) PublishReconciliationDelta(delta ReconciliationDelta) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		deletePath, err := s.prepare(`DELETE FROM files WHERE path=?`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(deletePath)
		deleteUnmanaged, err := s.prepare(`DELETE FROM unmanaged_files WHERE path=?`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(deleteUnmanaged)
		for _, path := range delta.Paths {
			for _, statement := range []*C.sqlite3_stmt{deletePath, deleteUnmanaged} {
				C.sqlite3_reset(statement)
				C.sqlite3_clear_bindings(statement)
				bindText(statement, 1, filepath.Clean(path))
				if err := stepDone(s, statement); err != nil {
					return err
				}
			}
		}
		insertFile, err := s.prepare(`INSERT INTO files(path,payload,updated_at) VALUES(?,?,?)`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(insertFile)
		for _, file := range delta.Files {
			payload, err := json.Marshal(file)
			if err != nil {
				return err
			}
			C.sqlite3_reset(insertFile)
			C.sqlite3_clear_bindings(insertFile)
			bindText(insertFile, 1, filepath.Clean(file.Path))
			bindText(insertFile, 2, string(payload))
			bindText(insertFile, 3, now)
			if err := stepDone(s, insertFile); err != nil {
				return err
			}
		}
		insertUnmanaged, err := s.prepare(`INSERT INTO unmanaged_files(path,payload,updated_at) VALUES(?,?,?)`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(insertUnmanaged)
		for _, file := range delta.Unmanaged {
			payload, err := json.Marshal(file)
			if err != nil {
				return err
			}
			C.sqlite3_reset(insertUnmanaged)
			C.sqlite3_clear_bindings(insertUnmanaged)
			bindText(insertUnmanaged, 1, filepath.Clean(file.Path))
			bindText(insertUnmanaged, 2, string(payload))
			bindText(insertUnmanaged, 3, now)
			if err := stepDone(s, insertUnmanaged); err != nil {
				return err
			}
		}
		deleteParts, err := s.prepare(`DELETE FROM media_file_parts WHERE kind=? AND source_id=?`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(deleteParts)
		deleteMedia, err := s.prepare(`DELETE FROM media_files WHERE kind=? AND source_id=?`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(deleteMedia)
		for _, owner := range delta.MediaOwners {
			for _, statement := range []*C.sqlite3_stmt{deleteParts, deleteMedia} {
				C.sqlite3_reset(statement)
				C.sqlite3_clear_bindings(statement)
				bindText(statement, 1, string(owner.Kind))
				bindInt(statement, 2, int64(owner.SourceID))
				if err := stepDone(s, statement); err != nil {
					return err
				}
			}
		}
		mediaStatement, err := s.prepare(`INSERT INTO media_files(kind,source_id,source,source_file_id,path,integration_id,integration_name) VALUES(?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(mediaStatement)
		partStatement, err := s.prepare(`INSERT INTO media_file_parts(kind,source_id,source,source_file_id,part_group,part_label,part_order,source_part_id) VALUES(?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(partStatement)
		for _, ref := range delta.MediaRefs {
			C.sqlite3_reset(mediaStatement)
			C.sqlite3_clear_bindings(mediaStatement)
			bindText(mediaStatement, 1, string(ref.MediaType))
			bindInt(mediaStatement, 2, int64(ref.MediaID))
			bindText(mediaStatement, 3, ref.Source)
			bindInt(mediaStatement, 4, int64(ref.SourceFileID))
			bindText(mediaStatement, 5, filepath.Clean(ref.Path))
			bindText(mediaStatement, 6, ref.IntegrationID)
			bindText(mediaStatement, 7, ref.IntegrationName)
			if err := stepDone(s, mediaStatement); err != nil {
				return err
			}
			for _, part := range ref.Parts {
				C.sqlite3_reset(partStatement)
				C.sqlite3_clear_bindings(partStatement)
				bindText(partStatement, 1, string(ref.MediaType))
				bindInt(partStatement, 2, int64(ref.MediaID))
				bindText(partStatement, 3, ref.Source)
				bindInt(partStatement, 4, int64(ref.SourceFileID))
				bindText(partStatement, 5, part.Group)
				bindText(partStatement, 6, part.Label)
				bindInt(partStatement, 7, int64(part.Order))
				bindInt(partStatement, 8, int64(part.SourcePartID))
				if err := stepDone(s, partStatement); err != nil {
					return err
				}
			}
		}
		deleteTorrent, err := s.prepare(`DELETE FROM torrent_files WHERE hash=?`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(deleteTorrent)
		for _, hash := range delta.RemovedTorrentHashes {
			C.sqlite3_reset(deleteTorrent)
			C.sqlite3_clear_bindings(deleteTorrent)
			bindText(deleteTorrent, 1, strings.ToLower(strings.TrimSpace(hash)))
			if err := stepDone(s, deleteTorrent); err != nil {
				return err
			}
		}
		insertTorrentFile, err := s.prepare(`INSERT INTO torrent_files(client,hash,file_index,path,integration_id,integration_name) VALUES(?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer C.sqlite3_finalize(insertTorrentFile)
		for _, ref := range delta.TorrentRefs {
			C.sqlite3_reset(insertTorrentFile)
			C.sqlite3_clear_bindings(insertTorrentFile)
			bindText(insertTorrentFile, 1, ref.Client)
			bindText(insertTorrentFile, 2, strings.ToLower(ref.Hash))
			bindInt(insertTorrentFile, 3, int64(ref.FileIndex))
			bindText(insertTorrentFile, 4, filepath.Clean(ref.Path))
			bindText(insertTorrentFile, 5, ref.IntegrationID)
			bindText(insertTorrentFile, 6, ref.IntegrationName)
			if err := stepDone(s, insertTorrentFile); err != nil {
				return err
			}
		}
		if err := s.saveTorrents(delta.Torrents); err != nil {
			return err
		}
		if err := s.saveMedia(delta.Media); err != nil {
			return err
		}
		if err := s.setMeta("files.updated_at", now); err != nil {
			return err
		}
		if err := s.setMeta("unmanaged.updated_at", now); err != nil {
			return err
		}
		if err := s.setMeta("generation.files", strconv.FormatUint(delta.Generation, 10)); err != nil {
			return err
		}
		if delta.ScopeMetadataKey != "" {
			if err := s.setMeta(delta.ScopeMetadataKey, ""); err != nil {
				return err
			}
		}
		return nil
	})
}

// PublishInventory atomically advances qBittorrent's incremental cursor with
// the Media and Torrent snapshot to which it belongs. Replaying an old RID is
// safe; advancing it without its snapshot is not.
func (s *Store) PublishInventory(generation uint64, torrentRID int64, torrents []model.Torrent, media []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		if err := s.saveTorrents(torrents); err != nil {
			return err
		}
		if torrentRID != 0 {
			if err := s.setMeta("qbittorrent.rid", strconv.FormatInt(torrentRID, 10)); err != nil {
				return err
			}
		}
		if err := s.saveMedia(media); err != nil {
			return err
		}
		return s.setMeta("generation.inventory", strconv.FormatUint(generation, 10))
	})
}

// PublishEnrichment replaces only Media valuation inputs/results and records
// the base generation they enrich. It cannot disturb Torrent or File topology.
func (s *Store) PublishEnrichment(name string, generation uint64, media []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		if err := s.saveMedia(media); err != nil {
			return err
		}
		return s.setMeta("generation.enrichment."+name, strconv.FormatUint(generation, 10))
	})
}

func (s *Store) LoadFiles() ([]model.File, []model.MediaFileRef, []model.TorrentFileRef, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	var files []model.File
	st, err := s.prepare(`SELECT payload FROM files ORDER BY path`)
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			C.sqlite3_finalize(st)
			return nil, nil, nil, time.Time{}, s.err(rc)
		}
		var f model.File
		if err := json.Unmarshal([]byte(colText(st, 0)), &f); err != nil {
			C.sqlite3_finalize(st)
			return nil, nil, nil, time.Time{}, err
		}
		files = append(files, f)
	}
	C.sqlite3_finalize(st)
	var mr []model.MediaFileRef
	st, err = s.prepare(`SELECT kind,source_id,source,source_file_id,path,integration_id,integration_name FROM media_files ORDER BY kind,source_id,path`)
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			C.sqlite3_finalize(st)
			return nil, nil, nil, time.Time{}, s.err(rc)
		}
		mr = append(mr, model.MediaFileRef{MediaType: model.MediaType(colText(st, 0)), MediaID: int(C.sqlite3_column_int64(st, 1)), Source: colText(st, 2), SourceFileID: int(C.sqlite3_column_int64(st, 3)), Path: colText(st, 4), IntegrationID: colText(st, 5), IntegrationName: colText(st, 6)})
	}
	C.sqlite3_finalize(st)
	partsByFile := map[string][]model.MediaFilePart{}
	st, err = s.prepare(`SELECT kind,source_id,source,source_file_id,part_group,part_label,part_order,source_part_id FROM media_file_parts ORDER BY kind,source_id,source_file_id,part_order,part_label`)
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			C.sqlite3_finalize(st)
			return nil, nil, nil, time.Time{}, s.err(rc)
		}
		key := fmt.Sprintf("%s:%d:%s:%d", colText(st, 0), int(C.sqlite3_column_int64(st, 1)), colText(st, 2), int(C.sqlite3_column_int64(st, 3)))
		partsByFile[key] = append(partsByFile[key], model.MediaFilePart{Group: colText(st, 4), Label: colText(st, 5), Order: int(C.sqlite3_column_int64(st, 6)), SourcePartID: int(C.sqlite3_column_int64(st, 7))})
	}
	C.sqlite3_finalize(st)
	for i := range mr {
		key := fmt.Sprintf("%s:%d:%s:%d", mr[i].MediaType, mr[i].MediaID, mr[i].Source, mr[i].SourceFileID)
		mr[i].Parts = append([]model.MediaFilePart(nil), partsByFile[key]...)
	}
	var tr []model.TorrentFileRef
	st, err = s.prepare(`SELECT client,hash,file_index,path,integration_id,integration_name FROM torrent_files ORDER BY client,hash,file_index`)
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			C.sqlite3_finalize(st)
			return nil, nil, nil, time.Time{}, s.err(rc)
		}
		tr = append(tr, model.TorrentFileRef{Client: colText(st, 0), Hash: colText(st, 1), FileIndex: int(C.sqlite3_column_int64(st, 2)), Path: colText(st, 3), IntegrationID: colText(st, 4), IntegrationName: colText(st, 5)})
	}
	C.sqlite3_finalize(st)
	ts, _ := s.meta("files.updated_at")
	updated, _ := time.Parse(time.RFC3339Nano, ts)
	return files, mr, tr, updated, nil
}

func (s *Store) SaveHistoryEvent(e HistoryEvent) (int64, error) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	st, err := s.prepare(`INSERT INTO history_events(event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, e.EventType)
	bindText(st, 2, e.Status)
	bindInt(st, 3, boolInt(e.DryRun))
	bindText(st, 4, e.RequestedKind)
	bindText(st, 5, e.RequestedKey)
	bindText(st, 6, e.RequestedLabel)
	bindInt(st, 7, e.ReclaimableBytes)
	bindText(st, 8, string(e.Payload))
	bindText(st, 9, e.Error)
	bindText(st, 10, e.CreatedAt.UTC().Format(time.RFC3339Nano))
	bindInt(st, 11, e.MediaBytes)
	if err := stepDone(s, st); err != nil {
		return 0, err
	}
	return int64(C.sqlite3_last_insert_rowid(s.db)), nil
}

// UpdateHistoryEvent finalizes the durable operation row created before a
// removal begins. The operation identity is preserved across every outcome.
func (s *Store) UpdateHistoryEvent(e HistoryEvent) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if e.ID <= 0 {
		return fmt.Errorf("history event id is required")
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	st, err := s.prepare(`UPDATE history_events SET status=?,dry_run=?,requested_kind=?,requested_key=?,requested_label=?,reclaimable_bytes=?,payload=?,error=?,media_bytes=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, e.Status)
	bindInt(st, 2, boolInt(e.DryRun))
	bindText(st, 3, e.RequestedKind)
	bindText(st, 4, e.RequestedKey)
	bindText(st, 5, e.RequestedLabel)
	bindInt(st, 6, e.ReclaimableBytes)
	bindText(st, 7, string(e.Payload))
	bindText(st, 8, e.Error)
	bindInt(st, 9, e.MediaBytes)
	bindInt(st, 10, e.ID)
	if err := stepDone(s, st); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return fmt.Errorf("history event %d does not exist", e.ID)
	}
	return nil
}

// InterruptStartedHistoryEvents closes operations left in-flight by a prior
// process exit. Their exact starting plan remains available in payload.
func (s *Store) InterruptStartedHistoryEvents() error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.exec(`UPDATE history_events SET status='interrupted', error=CASE WHEN error='' THEN 'application stopped before the removal operation was finalized' ELSE error END WHERE event_type='removal' AND status='started'`)
}

// InFlightRemovalHistoryEvents returns the domain journal entries that need to
// be reconciled with scheduler state before new work starts after a restart.
func (s *Store) InFlightRemovalHistoryEvents() ([]HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	statement, err := s.prepare(`SELECT id,event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes FROM history_events WHERE event_type='removal' AND status IN ('queued','started') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(statement)
	var events []HistoryEvent
	for {
		result := C.sqlite3_step(statement)
		if result == C.SQLITE_DONE {
			break
		}
		if result != C.SQLITE_ROW {
			return nil, s.err(result)
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, colText(statement, 10))
		events = append(events, HistoryEvent{ID: int64(C.sqlite3_column_int64(statement, 0)), EventType: colText(statement, 1), Status: colText(statement, 2), DryRun: C.sqlite3_column_int64(statement, 3) != 0, RequestedKind: colText(statement, 4), RequestedKey: colText(statement, 5), RequestedLabel: colText(statement, 6), ReclaimableBytes: int64(C.sqlite3_column_int64(statement, 7)), Payload: json.RawMessage(colText(statement, 8)), Error: colText(statement, 9), CreatedAt: createdAt, MediaBytes: int64(C.sqlite3_column_int64(statement, 11))})
	}
	return events, nil
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func (s *Store) HistoryEvents(limit int) ([]HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	st, err := s.prepare(`SELECT id,event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes FROM history_events ORDER BY created_at DESC LIMIT ?`)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(st)
	bindInt(st, 1, int64(limit))
	var out []HistoryEvent
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		t, _ := time.Parse(time.RFC3339Nano, colText(st, 10))
		out = append(out, HistoryEvent{ID: int64(C.sqlite3_column_int64(st, 0)), EventType: colText(st, 1), Status: colText(st, 2), DryRun: C.sqlite3_column_int64(st, 3) != 0, RequestedKind: colText(st, 4), RequestedKey: colText(st, 5), RequestedLabel: colText(st, 6), ReclaimableBytes: int64(C.sqlite3_column_int64(st, 7)), Payload: json.RawMessage(colText(st, 8)), Error: colText(st, 9), CreatedAt: t, MediaBytes: int64(C.sqlite3_column_int64(st, 11))})
	}
	return out, nil
}

func (s *Store) HistoryEventByID(id int64) (HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	if id <= 0 {
		return HistoryEvent{}, fmt.Errorf("history event id is required")
	}
	st, err := s.prepare(`SELECT id,event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes FROM history_events WHERE id=?`)
	if err != nil {
		return HistoryEvent{}, err
	}
	defer C.sqlite3_finalize(st)
	bindInt(st, 1, id)
	rc := C.sqlite3_step(st)
	if rc == C.SQLITE_DONE {
		return HistoryEvent{}, fmt.Errorf("history event %d does not exist", id)
	}
	if rc != C.SQLITE_ROW {
		return HistoryEvent{}, s.err(rc)
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, colText(st, 10))
	return HistoryEvent{ID: int64(C.sqlite3_column_int64(st, 0)), EventType: colText(st, 1), Status: colText(st, 2), DryRun: C.sqlite3_column_int64(st, 3) != 0, RequestedKind: colText(st, 4), RequestedKey: colText(st, 5), RequestedLabel: colText(st, 6), ReclaimableBytes: int64(C.sqlite3_column_int64(st, 7)), Payload: json.RawMessage(colText(st, 8)), Error: colText(st, 9), CreatedAt: createdAt, MediaBytes: int64(C.sqlite3_column_int64(st, 11))}, nil
}
