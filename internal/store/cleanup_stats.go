package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import "time"

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
