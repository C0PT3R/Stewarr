package httpui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"togetharr/internal/inventory"
	"togetharr/internal/model"
	"togetharr/internal/removal"
	"togetharr/internal/tasks"
)

func TestLibraryTemplateRenders(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := libraryData{
		Rows: []mediaRow{{Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test", Year: 2026, Value: 12.5, SizeBytes: 1024}}},
		Page: 1, PageSize: 50, TotalPages: 1, TotalItems: 1, Sort: "title", Order: "asc",
		SortURLs:  map[string]string{"value": "/library", "title": "/library", "type": "/library", "rating": "/library", "votes": "/library", "views": "/library", "lastwatched": "/library", "requested": "/library", "size": "/library", "torrents": "/library"},
		SizeLinks: []navLink{{Value: 25, URL: "/library"}, {Value: 50, URL: "/library"}, {Value: 100, URL: "/library"}, {Value: 250, URL: "/library"}},
	}
	var b bytes.Buffer
	if err := s.libraryTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
}

func TestTorrentTemplateRendersPaged(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := torrentData{Torrents: []model.Torrent{{Hash: "abc", Name: "Torrent", AssociationStatus: "ASSOCIATED"}}, TotalItems: 1, Page: 1, PageSize: 50, TotalPages: 1, Sort: "status", Order: "asc", SortURLs: map[string]string{"status": "/torrents", "name": "/torrents", "media": "/torrents", "state": "/torrents", "size": "/torrents", "ratio": "/torrents", "upload": "/torrents", "seeds": "/torrents", "leechers": "/torrents", "activity": "/torrents"}, SizeLinks: []navLink{{Value: 50, URL: "/torrents"}}}
	var b bytes.Buffer
	if err := s.torrentTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
}

func TestFilterMedia(t *testing.T) {
	items := []model.Media{
		{Type: model.Movie, SourceID: 1, Title: "Alien", Requested: true, Views: 2, Torrents: []model.Torrent{{Hash: "a"}}, Tags: []string{"keep"}},
		{Type: model.Series, SourceID: 2, Title: "Severance", Requested: false, Views: 0},
	}
	got := filterMedia(items, "alien", "movie", "yes", "yes", "yes", true)
	if len(got) != 1 || got[0].Title != "Alien" {
		t.Fatalf("unexpected media filter result: %#v", got)
	}
	got = filterMedia(items, "", "series", "any", "no", "no", true)
	if len(got) != 1 || got[0].Title != "Severance" {
		t.Fatalf("unexpected series filter result: %#v", got)
	}
}

func TestFilterTorrents(t *testing.T) {
	items := []model.Torrent{
		{Hash: "abc", Name: "Old Release", AssociationStatus: "SUPERSEDED", ReclaimableKnown: true, ReclaimableBytes: 1024, UploadSpeed: 0, LeechersConnected: 0},
		{Hash: "def", Name: "Current Release", AssociationStatus: "ASSOCIATED", UploadSpeed: 100},
	}
	got := filterTorrents(items, "old", "SUPERSEDED", "positive", "inactive")
	if len(got) != 1 || got[0].Hash != "abc" {
		t.Fatalf("unexpected torrent filter result: %#v", got)
	}
	got = filterTorrents(items, "def", "ASSOCIATED", "any", "active")
	if len(got) != 1 || got[0].Hash != "def" {
		t.Fatalf("unexpected active torrent filter result: %#v", got)
	}
}

func TestApplyUnclaimedSummaryBeforeFirstScan(t *testing.T) {
	var d homeData
	applyUnclaimedSummary(&d, nil, time.Time{}, nil)
	if d.UnclaimedAvailable {
		t.Fatal("unclaimed data must not be authoritative before first successful scan")
	}
	if d.UnclaimedError != "" {
		t.Fatalf("startup without a completed scan is not an error: %q", d.UnclaimedError)
	}
}

func TestApplyUnclaimedSummaryError(t *testing.T) {
	var d homeData
	applyUnclaimedSummary(&d, nil, time.Time{}, errors.New("qBittorrent unavailable"))
	if d.UnclaimedError != "qBittorrent unavailable" {
		t.Fatalf("unexpected error: %q", d.UnclaimedError)
	}
}

func TestFilterMediaHidesNoFileMediaByDefault(t *testing.T) {
	items := []model.Media{
		{Type: model.Movie, SourceID: 1, Title: "Present", SizeBytes: 1024},
		{Type: model.Movie, SourceID: 2, Title: "Missing", SizeBytes: 0},
	}
	got := filterMedia(items, "", "any", "any", "any", "any", false)
	if len(got) != 1 || got[0].Title != "Present" {
		t.Fatalf("unexpected default file filter: %#v", got)
	}
	got = filterMedia(items, "", "any", "any", "any", "any", true)
	if len(got) != 2 {
		t.Fatalf("show-no-files should include both media, got %d", len(got))
	}
}

func TestTasksTemplateRenders(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := tasksData{Tasks: []tasks.Status{{
		ID: "inventory", Name: "Inventory refresh", Description: "Refresh state",
		Interval: time.Hour, LastDuration: 1500 * time.Millisecond, NextRun: time.Now().Add(time.Hour),
	}}}
	var b bytes.Buffer
	if err := s.tasksTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
}

func TestGroupMediaTorrents(t *testing.T) {
	items := []model.Torrent{
		{Hash: "s2", Name: "Z old", AssociationStatus: "SUPERSEDED"},
		{Hash: "c1", Name: "Current", AssociationStatus: "ASSOCIATED"},
		{Hash: "o1", Name: "Orphan", AssociationStatus: "ORPHANED"},
		{Hash: "s1", Name: "A old", AssociationStatus: "SUPERSEDED"},
		{Hash: "u1", Name: "Unknown", AssociationStatus: "UNASSOCIATED"},
	}
	current, superseded, orphaned := groupMediaTorrents(items)
	if len(current) != 1 || current[0].Hash != "c1" {
		t.Fatalf("unexpected current torrents: %#v", current)
	}
	if len(superseded) != 2 || superseded[0].Hash != "s1" || superseded[1].Hash != "s2" {
		t.Fatalf("unexpected superseded torrents: %#v", superseded)
	}
	if len(orphaned) != 1 || orphaned[0].Hash != "o1" {
		t.Fatalf("unexpected orphaned torrents: %#v", orphaned)
	}
}

func TestProfileTemplateShowsTorrentNamesAndGroups(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := struct {
		Media        model.Media
		Rank         int
		Total        int
		Updated      time.Time
		LastErr      error
		Refreshing   bool
		Current      []model.Torrent
		Superseded   []model.Torrent
		Orphaned     []model.Torrent
		Files        []model.File
		FileCount    int
		FilesUpdated time.Time
		FilesErr     error
	}{
		Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test"}, Total: 1,
		Current:    []model.Torrent{{Hash: "a", Name: "Current.Release", AssociationStatus: "ASSOCIATED"}},
		Superseded: []model.Torrent{{Hash: "b", Name: "Old.Release", AssociationStatus: "SUPERSEDED"}},
	}
	var b bytes.Buffer
	if err := s.profileTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Current.Release", "Old.Release", "Superseded"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("profile missing %q", want)
		}
	}
}

func TestTorrentTemplateShowsHistoricalMediaForSupersededTorrent(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := torrentData{
		Torrents:   []model.Torrent{{Hash: "old", Name: "Old.Release", AssociationStatus: "SUPERSEDED", FormerMediaItems: []model.MediaRef{{Type: model.Movie, SourceID: 42, Title: "Example", Year: 2025}}}},
		TotalItems: 1, Page: 1, PageSize: 50, TotalPages: 1, Sort: "status", Order: "asc",
		SortURLs:  map[string]string{"status": "/torrents", "name": "/torrents", "media": "/torrents", "state": "/torrents", "size": "/torrents", "reclaimable": "/torrents", "ratio": "/torrents", "upload": "/torrents", "seeds": "/torrents", "leechers": "/torrents", "activity": "/torrents"},
		SizeLinks: []navLink{{Value: 50, URL: "/torrents"}},
	}
	var b bytes.Buffer
	if err := s.torrentTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !bytes.Contains([]byte(out), []byte("Example")) || !bytes.Contains([]byte(out), []byte("historical")) {
		t.Fatalf("superseded torrent should expose its historical media relationship: %s", out)
	}
}

func TestProfileTemplateRendersFileTopology(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := struct {
		Media                         model.Media
		Rank, Total                   int
		Updated                       time.Time
		LastErr                       error
		Refreshing                    bool
		Current, Superseded, Orphaned []model.Torrent
		Files                         []inventory.FileView
		FileCount                     int
		FilesUpdated                  time.Time
		FilesErr                      error
		RemoveMedia                   inventory.RemovalEstimate
		RemoveWithCurrent             inventory.RemovalEstimate
	}{
		Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test"}, Total: 1,
		Files:     []inventory.FileView{{File: model.File{Path: "/media/a.mkv", SizeBytes: 100, Exists: true, IdentityKnown: true, Links: 2}, SharedWith: []inventory.FilePeer{{Path: "/downloads/a.mkv", Torrents: []inventory.TorrentFileOwner{{Hash: "abc", Name: "Release"}}}}}},
		FileCount: 1, FilesUpdated: time.Now(), RemoveMedia: inventory.RemovalEstimate{Known: true, SharedBytes: 100, Files: 1}, RemoveWithCurrent: inventory.RemovalEstimate{Known: true, ReclaimableBytes: 100, Files: 2},
	}
	var b bytes.Buffer
	if err := s.profileTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"hardlinked · 2 paths / 1 physical file", "/media/a.mkv", "/downloads/a.mkv", "Removing media frees <strong>0.0 B</strong>", "Removing media and current torrents frees <strong>100.0 B</strong>"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("profile missing %q: %s", want, out)
		}
	}
}

func TestProvenManagedRefsForTorrentUsesPhysicalIdentity(t *testing.T) {
	d := t.TempDir()
	dl := filepath.Join(d, "download.mkv")
	media := filepath.Join(d, "media.mkv")
	copyPath := filepath.Join(d, "copy.mkv")
	if err := os.WriteFile(dl, []byte("same bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(dl, media); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, []byte("same bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	inspect := func(path string) model.File {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		st := fi.Sys().(*syscall.Stat_t)
		return model.File{Path: path, SizeBytes: fi.Size(), Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: uint64(st.Ino), Links: uint64(st.Nlink)}
	}
	files := []model.File{inspect(dl), inspect(media), inspect(copyPath)}
	refs := []model.MediaFileRef{
		{MediaType: model.Series, MediaID: 1, Source: "sonarr", SourceFileID: 10, Path: media},
		{MediaType: model.Series, MediaID: 1, Source: "sonarr", SourceFileID: 11, Path: copyPath},
	}
	trefs := []model.TorrentFileRef{{Client: "qBittorrent", Hash: "abc", FileIndex: 0, Path: dl}}
	got := provenManagedRefsForTorrent(files, refs, trefs, "ABC")
	if len(got) != 1 || got[0].SourceFileID != 10 {
		t.Fatalf("expected only hardlinked managed file, got %#v", got)
	}
}

func TestGroupRemovalFilesGroupsHardlinksByPhysicalIdentity(t *testing.T) {
	files := []removal.FileState{
		{Path: "/data/downloads/a.mkv", Exists: true, SizeBytes: 123, IdentityKnown: true, Device: 56, Inode: 99, Links: 2},
		{Path: "/data/Films/a.mkv", Exists: true, SizeBytes: 123, IdentityKnown: true, Device: 56, Inode: 99, Links: 2},
	}
	groups := groupRemovalFiles(files, nil)
	if len(groups) != 1 {
		t.Fatalf("expected 1 physical group, got %d", len(groups))
	}
	if len(groups[0].Paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(groups[0].Paths))
	}
	if groups[0].SizeBytes != 123 || groups[0].Links != 2 {
		t.Fatalf("unexpected group: %+v", groups[0])
	}
}

func TestPhysicalCandidatesExposeUnclaimedHardlinkSibling(t *testing.T) {
	files := []model.File{
		{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100},
		{Path: "/series/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100},
	}
	trefs := []model.TorrentFileRef{{Client: "Downloader", Hash: "abc", FileIndex: 0, Path: "/downloads/a.mkv"}}
	cm := map[string]removal.CandidateFile{"/downloads/a.mkv": {Path: "/downloads/a.mkv", Owner: removal.TorrentOwner, OwnerKey: "abc", Selected: true}}
	got := physicalCandidates(files, nil, trefs, cm, nil, map[string]bool{"abc": true}, nil)
	sibling, ok := got["/series/a.mkv"]
	if !ok {
		t.Fatal("expected physical sibling candidate")
	}
	if sibling.Owner != removal.UnclaimedOwner {
		t.Fatalf("expected unclaimed sibling, got %s", sibling.Owner)
	}
}

func TestGroupRemovalFilesReportsMissingHardlinks(t *testing.T) {
	states := []removal.FileState{{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100, Owner: removal.TorrentOwner}}
	inventoryFiles := []model.File{{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100}}
	groups := groupRemovalFiles(states, inventoryFiles)
	if len(groups) != 1 || groups[0].MissingLinks != 1 {
		t.Fatalf("expected one missing hardlink, got %+v", groups)
	}
}

func TestGroupUnclaimedFilesCollapsesHardlinks(t *testing.T) {
	items := []model.UnclaimedFile{
		{Path: "/a", SizeBytes: 54321, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
		{Path: "/b", SizeBytes: 54321, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
	}
	groups := groupUnclaimedFiles(items)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if got := len(groups[0].Paths); got != 2 {
		t.Fatalf("got %d paths, want 2", got)
	}
	if groups[0].SizeBytes != 54321 {
		t.Fatalf("size = %d, want 54321", groups[0].SizeBytes)
	}
	if groups[0].ReclaimableBytes != 54321 {
		t.Fatalf("reclaimable = %d, want 54321", groups[0].ReclaimableBytes)
	}
}
