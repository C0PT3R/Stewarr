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
	"path/filepath"
	"sort"
	"stewarr/internal/model"
	"strconv"
	"strings"
	"time"
)

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
	mst, err := s.prepare(`INSERT INTO media_files(kind,source_id,source,source_file_id,path,service_id,service_name) VALUES(?,?,?,?,?,?,?)`)
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
		bindText(mst, 6, r.ServiceID)
		bindText(mst, 7, r.ServiceName)
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
	tst, err := s.prepare(`INSERT INTO torrent_files(client,hash,file_index,path,service_id,service_name) VALUES(?,?,?,?,?,?)`)
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
		bindText(tst, 5, r.ServiceID)
		bindText(tst, 6, r.ServiceName)
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
		mediaStatement, err := s.prepare(`INSERT INTO media_files(kind,source_id,source,source_file_id,path,service_id,service_name) VALUES(?,?,?,?,?,?,?)`)
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
			bindText(mediaStatement, 6, ref.ServiceID)
			bindText(mediaStatement, 7, ref.ServiceName)
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
		insertTorrentFile, err := s.prepare(`INSERT INTO torrent_files(client,hash,file_index,path,service_id,service_name) VALUES(?,?,?,?,?,?)`)
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
			bindText(insertTorrentFile, 5, ref.ServiceID)
			bindText(insertTorrentFile, 6, ref.ServiceName)
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

// PublishInventory atomically advances every configured qBittorrent
// instance's incremental cursor (keyed by service ID, since each
// instance has its own independent sync stream) with the Media and Torrent
// snapshot to which they belong. Replaying an old RID is safe; advancing it
// without its snapshot is not.
func (s *Store) PublishInventory(generation uint64, torrentRIDs map[string]int64, torrents []model.Torrent, media []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		if err := s.saveTorrents(torrents); err != nil {
			return err
		}
		for serviceID, rid := range torrentRIDs {
			if rid == 0 {
				continue
			}
			if err := s.setMeta("qbittorrent."+serviceID+".rid", strconv.FormatInt(rid, 10)); err != nil {
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
	st, err = s.prepare(`SELECT kind,source_id,source,source_file_id,path,service_id,service_name FROM media_files ORDER BY kind,source_id,path`)
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
		mr = append(mr, model.MediaFileRef{MediaType: model.MediaType(colText(st, 0)), MediaID: int(C.sqlite3_column_int64(st, 1)), Source: colText(st, 2), SourceFileID: int(C.sqlite3_column_int64(st, 3)), Path: colText(st, 4), ServiceID: colText(st, 5), ServiceName: colText(st, 6)})
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
	st, err = s.prepare(`SELECT client,hash,file_index,path,service_id,service_name FROM torrent_files ORDER BY client,hash,file_index`)
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
		tr = append(tr, model.TorrentFileRef{Client: colText(st, 0), Hash: colText(st, 1), FileIndex: int(C.sqlite3_column_int64(st, 2)), Path: colText(st, 3), ServiceID: colText(st, 4), ServiceName: colText(st, 5)})
	}
	C.sqlite3_finalize(st)
	ts, _ := s.meta("files.updated_at")
	updated, _ := time.Parse(time.RFC3339Nano, ts)
	return files, mr, tr, updated, nil
}
