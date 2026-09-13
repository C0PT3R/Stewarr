package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"time"
)

// TorrentHistorySample is one periodic health-scan reading for one
// torrent. Unlike the live model.Torrent snapshot, these accumulate over
// time so sustained patterns (a stalled swarm, a dead tracker) can be
// told apart from a single misleading instantaneous reading.
type TorrentHistorySample struct {
	Client          string
	Hash            string
	SampledAt       time.Time
	Ratio           float64
	SeedsSwarm      int
	LeechersSwarm   int
	UploadedBytes   int64
	DownloadedBytes int64
	State           string
	LastActivity    int64
	// TrackerWorking/TrackerWorkingKnown/TrackerMessage mirror
	// model.TrackerHealth — see its field docs for what each one means.
	TrackerWorking      bool
	TrackerWorkingKnown bool
	TrackerMessage      string
}

// SaveTorrentHistorySamples appends one row per sample. Existing rows are
// never modified or replaced; only PruneTorrentHistory removes rows, and
// only by age.
func (s *Store) SaveTorrentHistorySamples(samples []TorrentHistorySample) error {
	if len(samples) == 0 {
		return nil
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		st, e := s.prepare(`INSERT INTO torrent_history(client,hash,sampled_at,ratio,seeds_swarm,leechers_swarm,uploaded_bytes,downloaded_bytes,state,last_activity,tracker_working,tracker_working_known,tracker_message) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if e != nil {
			return e
		}
		defer C.sqlite3_finalize(st)
		for _, sample := range samples {
			C.sqlite3_reset(st)
			C.sqlite3_clear_bindings(st)
			bindText(st, 1, sample.Client)
			bindText(st, 2, sample.Hash)
			bindText(st, 3, sample.SampledAt.UTC().Format(time.RFC3339Nano))
			C.sqlite3_bind_double(st, 4, C.double(sample.Ratio))
			bindInt(st, 5, int64(sample.SeedsSwarm))
			bindInt(st, 6, int64(sample.LeechersSwarm))
			bindInt(st, 7, sample.UploadedBytes)
			bindInt(st, 8, sample.DownloadedBytes)
			bindText(st, 9, sample.State)
			bindInt(st, 10, sample.LastActivity)
			bindInt(st, 11, boolInt(sample.TrackerWorking))
			bindInt(st, 12, boolInt(sample.TrackerWorkingKnown))
			bindText(st, 13, sample.TrackerMessage)
			if e := stepDone(s, st); e != nil {
				return e
			}
		}
		return nil
	})
}

// PruneTorrentHistory deletes every sample older than before, so the table
// never grows unbounded. Called once per sampling cycle, right after
// SaveTorrentHistorySamples.
func (s *Store) PruneTorrentHistory(before time.Time) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		st, e := s.prepare(`DELETE FROM torrent_history WHERE sampled_at < ?`)
		if e != nil {
			return e
		}
		defer C.sqlite3_finalize(st)
		bindText(st, 1, before.UTC().Format(time.RFC3339Nano))
		return stepDone(s, st)
	})
}

// TorrentHistorySince returns every retained sample at or after since,
// across every torrent, in one query — grouped by client+hash so a caller
// scoring many torrents at once (e.g. valuation.ApplyTorrentValue) never
// needs one query per torrent. Each torrent's samples are oldest first.
func (s *Store) TorrentHistorySince(since time.Time) (map[string][]TorrentHistorySample, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	st, e := s.prepare(`SELECT client,hash,sampled_at,ratio,seeds_swarm,leechers_swarm,uploaded_bytes,downloaded_bytes,state,last_activity,tracker_working,tracker_working_known,tracker_message FROM torrent_history WHERE sampled_at >= ? ORDER BY client,hash,sampled_at ASC`)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, since.UTC().Format(time.RFC3339Nano))
	out := map[string][]TorrentHistorySample{}
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		client := colText(st, 0)
		hash := colText(st, 1)
		sampledAt, _ := time.Parse(time.RFC3339Nano, colText(st, 2))
		key := client + "|" + hash
		out[key] = append(out[key], TorrentHistorySample{
			Client:              client,
			Hash:                hash,
			SampledAt:           sampledAt,
			Ratio:               float64(C.sqlite3_column_double(st, 3)),
			SeedsSwarm:          int(C.sqlite3_column_int64(st, 4)),
			LeechersSwarm:       int(C.sqlite3_column_int64(st, 5)),
			UploadedBytes:       int64(C.sqlite3_column_int64(st, 6)),
			DownloadedBytes:     int64(C.sqlite3_column_int64(st, 7)),
			State:               colText(st, 8),
			LastActivity:        int64(C.sqlite3_column_int64(st, 9)),
			TrackerWorking:      C.sqlite3_column_int64(st, 10) != 0,
			TrackerWorkingKnown: C.sqlite3_column_int64(st, 11) != 0,
			TrackerMessage:      colText(st, 12),
		})
	}
	return out, nil
}

// TorrentHistorySamples returns every retained sample for one torrent,
// oldest first.
func (s *Store) TorrentHistorySamples(client, hash string) ([]TorrentHistorySample, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	st, e := s.prepare(`SELECT sampled_at,ratio,seeds_swarm,leechers_swarm,uploaded_bytes,downloaded_bytes,state,last_activity,tracker_working,tracker_working_known,tracker_message FROM torrent_history WHERE client=? AND hash=? ORDER BY sampled_at ASC`)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, client)
	bindText(st, 2, hash)
	var out []TorrentHistorySample
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		sampledAt, _ := time.Parse(time.RFC3339Nano, colText(st, 0))
		out = append(out, TorrentHistorySample{
			Client:              client,
			Hash:                hash,
			SampledAt:           sampledAt,
			Ratio:               float64(C.sqlite3_column_double(st, 1)),
			SeedsSwarm:          int(C.sqlite3_column_int64(st, 2)),
			LeechersSwarm:       int(C.sqlite3_column_int64(st, 3)),
			UploadedBytes:       int64(C.sqlite3_column_int64(st, 4)),
			DownloadedBytes:     int64(C.sqlite3_column_int64(st, 5)),
			State:               colText(st, 6),
			LastActivity:        int64(C.sqlite3_column_int64(st, 7)),
			TrackerWorking:      C.sqlite3_column_int64(st, 8) != 0,
			TrackerWorkingKnown: C.sqlite3_column_int64(st, 9) != 0,
			TrackerMessage:      colText(st, 10),
		})
	}
	return out, nil
}
