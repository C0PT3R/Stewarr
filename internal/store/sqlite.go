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

	"togetharr/internal/model"
)

type Store struct {
	db      *C.sqlite3
	writeMu sync.Mutex
}

type HistoryEvent struct {
	ID               int64           `json:"id"`
	EventType        string          `json:"eventType"`
	Status           string          `json:"status"`
	DryRun           bool            `json:"dryRun"`
	RequestedKind    string          `json:"requestedKind"`
	RequestedKey     string          `json:"requestedKey"`
	RequestedLabel   string          `json:"requestedLabel"`
	ReclaimableBytes int64           `json:"reclaimableBytes"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	Error            string          `json:"error,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
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
		`CREATE TABLE IF NOT EXISTS unclaimed_files (path TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS files (path TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS media_files (kind TEXT NOT NULL, source_id INTEGER NOT NULL, source TEXT NOT NULL, source_file_id INTEGER NOT NULL, path TEXT NOT NULL, PRIMARY KEY(kind,source_id,source,source_file_id));`,
		`CREATE INDEX IF NOT EXISTS idx_media_files_media ON media_files(kind,source_id);`,
		`CREATE INDEX IF NOT EXISTS idx_media_files_path ON media_files(path);`,
		`CREATE TABLE IF NOT EXISTS media_file_parts (kind TEXT NOT NULL, source_id INTEGER NOT NULL, source TEXT NOT NULL, source_file_id INTEGER NOT NULL, part_group TEXT NOT NULL DEFAULT '', part_label TEXT NOT NULL DEFAULT '', part_order INTEGER NOT NULL DEFAULT 0, source_part_id INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(kind,source_id,source,source_file_id,source_part_id));`,
		`CREATE INDEX IF NOT EXISTS idx_media_file_parts_file ON media_file_parts(kind,source_id,source,source_file_id);`,
		`CREATE TABLE IF NOT EXISTS torrent_files (client TEXT NOT NULL, hash TEXT NOT NULL, file_index INTEGER NOT NULL, path TEXT NOT NULL, PRIMARY KEY(client,hash,file_index));`,
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.setMeta(key, value)
}

// setMeta writes metadata while the caller already owns writeMu.
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
	return s.setMeta("warriors.updated_at", now)
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
		if out[i].Value != out[j].Value {
			return out[i].Value < out[j].Value
		}
		return out[i].SizeBytes > out[j].SizeBytes
	})
	ts, _ := s.Meta("warriors.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}

func (s *Store) SaveTorrents(items []model.Torrent) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
		// Persist only the torrent index Togetharr needs for lists, valuation,
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
	if err := s.exec("COMMIT"); err != nil {
		return err
	}
	ok = true
	return s.setMeta("portals.updated_at", now)
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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

func (s *Store) SaveUnclaimedFiles(items []model.UnclaimedFile) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = s.exec("ROLLBACK")
		}
	}()
	if err := s.exec("DELETE FROM unclaimed_files"); err != nil {
		return err
	}
	st, e := s.prepare(`INSERT INTO unclaimed_files(path,payload,updated_at) VALUES(?,?,?)`)
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
	if err := s.exec("COMMIT"); err != nil {
		return err
	}
	ok = true
	return s.setMeta("unclaimed.updated_at", now)
}

func (s *Store) LoadUnclaimedFiles() ([]model.UnclaimedFile, time.Time, error) {
	st, e := s.prepare(`SELECT payload FROM unclaimed_files`)
	if e != nil {
		return nil, time.Time{}, e
	}
	defer C.sqlite3_finalize(st)
	var out []model.UnclaimedFile
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, time.Time{}, s.err(rc)
		}
		var f model.UnclaimedFile
		if e := json.Unmarshal([]byte(colText(st, 0)), &f); e != nil {
			return nil, time.Time{}, e
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	ts, _ := s.Meta("unclaimed.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}

func (s *Store) ReplaceFiles(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = s.exec("ROLLBACK")
		}
	}()
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
	mst, err := s.prepare(`INSERT INTO media_files(kind,source_id,source,source_file_id,path) VALUES(?,?,?,?,?)`)
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
	tst, err := s.prepare(`INSERT INTO torrent_files(client,hash,file_index,path) VALUES(?,?,?,?)`)
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
		if err := stepDone(s, tst); err != nil {
			return err
		}
	}
	if err := s.exec("COMMIT"); err != nil {
		return err
	}
	ok = true
	return s.setMeta("files.updated_at", now)
}

func (s *Store) LoadFiles() ([]model.File, []model.MediaFileRef, []model.TorrentFileRef, time.Time, error) {
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
	st, err = s.prepare(`SELECT kind,source_id,source,source_file_id,path FROM media_files ORDER BY kind,source_id,path`)
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
		mr = append(mr, model.MediaFileRef{MediaType: model.MediaType(colText(st, 0)), MediaID: int(C.sqlite3_column_int64(st, 1)), Source: colText(st, 2), SourceFileID: int(C.sqlite3_column_int64(st, 3)), Path: colText(st, 4)})
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
	st, err = s.prepare(`SELECT client,hash,file_index,path FROM torrent_files ORDER BY client,hash,file_index`)
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
		tr = append(tr, model.TorrentFileRef{Client: colText(st, 0), Hash: colText(st, 1), FileIndex: int(C.sqlite3_column_int64(st, 2)), Path: colText(st, 3)})
	}
	C.sqlite3_finalize(st)
	ts, _ := s.Meta("files.updated_at")
	updated, _ := time.Parse(time.RFC3339Nano, ts)
	return files, mr, tr, updated, nil
}

func (s *Store) SaveHistoryEvent(e HistoryEvent) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	st, err := s.prepare(`INSERT INTO history_events(event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`)
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
	if err := stepDone(s, st); err != nil {
		return 0, err
	}
	return int64(C.sqlite3_last_insert_rowid(s.db)), nil
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func (s *Store) HistoryEvents(limit int) ([]HistoryEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	st, err := s.prepare(`SELECT id,event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at FROM history_events ORDER BY created_at DESC LIMIT ?`)
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
		out = append(out, HistoryEvent{ID: int64(C.sqlite3_column_int64(st, 0)), EventType: colText(st, 1), Status: colText(st, 2), DryRun: C.sqlite3_column_int64(st, 3) != 0, RequestedKind: colText(st, 4), RequestedKey: colText(st, 5), RequestedLabel: colText(st, 6), ReclaimableBytes: int64(C.sqlite3_column_int64(st, 7)), Payload: json.RawMessage(colText(st, 8)), Error: colText(st, 9), CreatedAt: t})
	}
	return out, nil
}
