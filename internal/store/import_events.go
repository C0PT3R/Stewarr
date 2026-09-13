package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"strings"
	"time"
)

type ImportEvent struct {
	Source     string
	ServiceID  string
	OwnerID    int
	SubID      int
	DownloadID string
	ImportedAt time.Time
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
	st, e := s.prepare(`INSERT OR IGNORE INTO arr_imports(source,owner_id,sub_id,download_id,imported_at,service_id) VALUES(?,?,?,?,?,?)`)
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
		bindText(st, 6, x.ServiceID)
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

// ImportEvents returns import history for one service instance of
// source ("radarr"/"sonarr"). serviceID disambiguates movie/series IDs
// across multiple configured instances of the same type; pass "" to match
// only pre-multi-instance rows persisted before this column existed.
func (s *Store) ImportEvents(source, serviceID string) ([]ImportEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	st, e := s.prepare(`SELECT owner_id,sub_id,download_id,imported_at FROM arr_imports WHERE source=? AND service_id=? ORDER BY imported_at DESC`)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, source)
	bindText(st, 2, serviceID)
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
		out = append(out, ImportEvent{Source: source, ServiceID: serviceID, OwnerID: int(C.sqlite3_column_int64(st, 0)), SubID: int(C.sqlite3_column_int64(st, 1)), DownloadID: colText(st, 2), ImportedAt: t})
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
