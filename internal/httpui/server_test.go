package httpui

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"connarr/internal/config"
	"connarr/internal/inventory"
	"connarr/internal/model"
	"connarr/internal/removal"
	"connarr/internal/store"
	"connarr/internal/tasks"
)

func TestRemovalSubmissionParsesBrowserMultipartForm(t *testing.T) {
	var body bytes.Buffer
	formWriter := multipart.NewWriter(&body)
	for field, value := range map[string]string{"kind": "media", "media_type": "movie", "media_id": "42", "managed_file": "radarr:9"} {
		if err := formWriter.WriteField(field, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := formWriter.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://connarr.local/removal/execute", &body)
	request.Header.Set("Content-Type", formWriter.FormDataContentType())
	response := httptest.NewRecorder()
	if err := parseRemovalForm(response, request); err != nil {
		t.Fatal(err)
	}
	if request.Form.Get("kind") != "media" || request.Form.Get("media_id") != "42" || request.Form.Get("managed_file") != "radarr:9" {
		t.Fatalf("multipart removal form was not preserved: %#v", request.Form)
	}
}

func TestTorrentRemovalRejectsInjectedLibraryPath(t *testing.T) {
	form := url.Values{
		"kind":           {"torrent"},
		"hash":           {"0ce683820c305af1aaca0c1fde91f08eccb9e374"},
		"target":         {"1"},
		"unmanaged_path": {"/data/Films/Afterburn (2025)/Afterburn.mkv"},
	}
	if err := validateRemovalScope(form); err == nil {
		t.Fatal("torrent removal accepted an injected unmanaged library path")
	}
}

func TestScheduledExecutionRejectsInjectedLibraryPathBeforeInventoryAccess(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"kind":           {"torrent"},
		"hash":           {"abc"},
		"target":         {"1"},
		"unmanaged_path": {"/data/Films/Afterburn (2025)/Afterburn.mkv"},
	}
	request := &http.Request{Method: http.MethodPost, Form: form}
	response := httptest.NewRecorder()
	server.executeRemovalNowContext(response, request, context.Background())
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "scope rejected") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTorrentRemovalScopeAcceptsOnlyTorrentTarget(t *testing.T) {
	form := url.Values{"kind": {"torrent"}, "hash": {"abc"}, "target": {"1"}}
	if err := validateRemovalScope(form); err != nil {
		t.Fatalf("clean torrent removal was rejected: %v", err)
	}
}

func TestMediaRemovalScopeAcceptsRelatedTorrentAction(t *testing.T) {
	form := url.Values{"kind": {"media"}, "managed_file": {"radarr:9"}, "torrent": {"abc"}}
	if err := validateRemovalScope(form); err != nil {
		t.Fatalf("media removal rejected a related torrent action: %v", err)
	}
}

func TestEveryRemovalKindRejectsCrossOwnerActions(t *testing.T) {
	for name, form := range map[string]url.Values{
		"media with direct path":   {"kind": {"media"}, "unmanaged_path": {"/data/file"}},
		"unmanaged with torrent":   {"kind": {"unmanaged"}, "path": {"/data/file"}, "torrent": {"abc"}},
		"torrent with media owner": {"kind": {"torrent"}, "target": {"1"}, "managed_file": {"radarr:1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateRemovalScope(form); err == nil {
				t.Fatalf("cross-owner form was accepted: %#v", form)
			}
		})
	}
}

// TestUnmanagedRemovalScopeAcceptsCleanForm guards the re-enablement of
// direct filesystem removal for Unmanaged files: a clean "unmanaged" kind
// submission carrying only "path" must be accepted, not rejected outright
// the way it was while this feature was disabled pending refactoring.
func TestUnmanagedRemovalScopeAcceptsCleanForm(t *testing.T) {
	form := url.Values{"kind": {"unmanaged"}, "path": {"/data/Films/Agent Zeta/movie.mkv"}}
	if err := validateRemovalScope(form); err != nil {
		t.Fatalf("clean unmanaged removal was rejected: %v", err)
	}
}

func TestUnmanagedRemovalScopeRequiresAtLeastOnePath(t *testing.T) {
	form := url.Values{"kind": {"unmanaged"}}
	if err := validateRemovalScope(form); err == nil {
		t.Fatal("expected an error when no path is selected")
	}
}

// TestRemovalUnmanagedHandlerIsLive confirms the GET overlay handler is no
// longer the hardcoded 403 stub it was while this feature was disabled —
// it now actually calls buildUnmanagedRemovalPlan. A path unknown to this
// minimal server still fails (nothing is genuinely unmanaged here), but
// that failure must come from real plan-building, not a blanket rejection.
func TestRemovalUnmanagedHandlerIsLive(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.removalUnmanaged(response, httptest.NewRequest(http.MethodGet, "/removal/unmanaged?path=/data/file", nil))
	if response.Code == http.StatusForbidden {
		t.Fatalf("removalUnmanaged is still hardcoded to reject: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestUnmanagedPageExposesRemovalControls(t *testing.T) {
	content, err := uiFiles.ReadFile("templates/unmanaged.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"/removal/unmanaged", "unmanagedPick", "unmanagedRemoveButton", "unmanagedAll", `name="path"`} {
		if !bytes.Contains(content, []byte(required)) {
			t.Fatalf("Unmanaged page missing removal control %q", required)
		}
	}
	for _, required := range []string{"Unmanaged files", "No current service claims these paths"} {
		if !bytes.Contains(content, []byte(required)) {
			t.Fatalf("Unmanaged explanation missing %q", required)
		}
	}
}

func TestCrossOriginWriteIsRejected(t *testing.T) {
	h := sameOriginWrites(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(http.MethodPost, "http://connarr.local/removal/execute", nil)
	r.Host = "connarr.local"
	r.Header.Set("Origin", "http://evil.local")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestSelectedUnmanagedStatesPreservesConfirmedIdentity(t *testing.T) {
	p := removal.RemovalPlan{Files: []removal.FileState{
		{Path: "/data/a", Owner: removal.UnmanagedOwner, Selected: true, Exists: true, IdentityKnown: true, Device: 7, Inode: 9},
		{Path: "/data/b", Owner: removal.UnmanagedOwner, Selected: false},
	}}
	xs := selectedUnmanagedStates(p, []string{"/data/a", "/data/b"})
	if len(xs) != 1 || xs[0].Device != 7 || xs[0].Inode != 9 {
		t.Fatalf("states=%#v", xs)
	}
}

func TestGroupRelatedTorrentsSeparatesRelationshipTypes(t *testing.T) {
	groups := groupRelatedTorrents([]relatedRemovalTorrent{
		{Torrent: model.Torrent{Hash: "old", Name: "Old", AssociationStatus: "SUPERSEDED"}},
		{Torrent: model.Torrent{Hash: "current", Name: "Current", AssociationStatus: "ASSOCIATED"}, Selected: true},
	})
	if len(groups) != 2 || groups[0].Label != "Current" || groups[1].Label != "Superseded" {
		t.Fatalf("unexpected groups: %#v", groups)
	}
	if len(groups[0].Torrents) != 1 || !groups[0].Torrents[0].Selected || groups[0].Torrents[0].Torrent.Hash != "current" {
		t.Fatalf("current group lost torrent state: %#v", groups[0])
	}
}

func TestStorageGuidanceExplainsBlockingAssociatedTorrent(t *testing.T) {
	plan := removal.RemovalPlan{Files: []removal.FileState{
		{Path: "/library/movie.mkv", Owner: removal.MediaOwner, Selected: true, Exists: true, SizeBytes: 4096, IdentityKnown: true, Device: 1, Inode: 2, Links: 2},
		{Path: "/downloads/movie.mkv", Owner: removal.TorrentOwner, OwnerKey: "abc", Selected: false, Exists: true, SizeBytes: 4096, IdentityKnown: true, Device: 1, Inode: 2, Links: 2},
	}}
	title, action := storageGuidance(plan, model.Movie, []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "abc", AssociationStatus: "ASSOCIATED"}}})
	if title == "" || action != "It is hardlinked to the current torrent. Also select that torrent below to reclaim the shared data." {
		t.Fatalf("unexpected guidance: %q / %q", title, action)
	}
	plan.Files[1].Selected = true
	if title, action := storageGuidance(plan, model.Movie, []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "abc", AssociationStatus: "ASSOCIATED"}, Selected: true}}); title != "" || action != "" {
		t.Fatalf("guidance must disappear when the final link is selected: %q / %q", title, action)
	}
}

func TestRemovalTemplateUsesFilenamesAndRelationshipGroups(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := removalData{
		Plan: removal.RemovalPlan{Kind: removal.MediaObject, RequestedLabel: "Movie", ReclaimableBytes: 1024},
		FileGroups: []removalPhysicalFileGroup{{Key: "1:2", SizeBytes: 1024, Selectable: true, Selected: true, DisplayPaths: []removalDisplayPath{
			{Label: "Radarr", Path: "/data/Films/actual-file.mkv", Text: "/actual-file.mkv", ActionLabel: "Remove managed file", Selectable: true, Selected: true},
			{Label: "qBittorrent", Path: "/data/downloads/actual-file.mkv", Text: "/actual-file.mkv", ActionLabel: "Remove torrent and its complete data set", Selectable: true, Selected: true},
		}}},
		ManagedFileCount: 1, ManagedSectionLabel: "Movie files", SelectedActions: 1,
		ManagedGroups: []managedRemovalGroup{{Label: "Files", Files: []managedRemovalFile{{
			Ref: model.MediaFileRef{Source: "radarr", SourceFileID: 9}, File: model.File{SizeBytes: 1024},
			Selected: true, Filename: "actual-file.mkv", Label: "actual-file.mkv", PhysicalKey: "1:2",
		}}}},
		Related: []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "abc", Name: "Current Release", AssociationStatus: model.TorrentCurrent}, Selected: true, Selectable: true}},
		RelatedGroups: []relatedRemovalTorrentGroup{
			{Label: "Current", Torrents: []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "abc", Name: "Current Release", AssociationStatus: model.TorrentCurrent}, Selected: true, Selectable: true, PhysicallyBacks: true, FileCount: 1}}},
			{Label: "Superseded", Torrents: []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "old", Name: "Old Release", AssociationStatus: model.TorrentSuperseded}, FileCount: 1}}},
		},
		RelatedTorrentCount: 2, SelectedTorrentCount: 1, PreservedTorrentCount: 1,
		MediaType: string(model.Movie), MediaID: 1,
	}
	var output bytes.Buffer
	if err := server.removalTpl.Execute(&output, data); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"Movie files", "actual-file.mkv", "Media torrents · 2", "1 selected for removal · 1 preserved", "physically backs this media", "preserved historical relationship", "Files and storage", "Remove selected", "data-removal-consequences", "data-removal-storage"} {
		if !bytes.Contains(output.Bytes(), []byte(expected)) {
			t.Fatalf("removal template does not contain %q: %s", expected, html)
		}
	}
	if bytes.Contains(output.Bytes(), []byte("Associated torrents")) || bytes.Contains(output.Bytes(), []byte("freed if fully removed")) {
		t.Fatalf("obsolete removal wording remains: %s", html)
	}
	for _, name := range []string{"Current Release", "Old Release"} {
		if bytes.Count(output.Bytes(), []byte(name)) != 1 {
			t.Fatalf("torrent %q must appear exactly once: %s", name, html)
		}
	}
	if bytes.Contains(output.Bytes(), []byte("Unassociated")) {
		t.Fatalf("media removal must not show unassociated torrents: %s", html)
	}
}

func selectedTorrent(torrents []relatedRemovalTorrent, hash string) bool {
	for _, torrent := range torrents {
		if torrent.Torrent.Hash == hash {
			return torrent.Selected
		}
	}
	return false
}

func TestTorrentRemovalDisclosesButCannotSelectRelatedLibraryPath(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := removalData{
		Plan: removal.RemovalPlan{Kind: removal.TorrentObject, RequestedLabel: "Afterburn", Files: []removal.FileState{
			{Path: "/data/downloads/complete/Afterburn.mkv", Owner: removal.TorrentOwner},
			{Path: "/data/Films/Afterburn (2025)/Afterburn.mkv", Owner: removal.UnmanagedOwner},
		}},
		TorrentTarget: true, TorrentSelected: true, SelectedActions: 1, Hash: "abc",
		RelatedUnmanaged: []relatedUnmanagedFile{{Path: "/data/Films/Afterburn (2025)/Afterburn.mkv", SizeBytes: 10}},
		FileGroups:       []removalPhysicalFileGroup{{Paths: []string{"/data/downloads/complete/Afterburn.mkv", "/data/Films/Afterburn (2025)/Afterburn.mkv"}}},
	}
	var output bytes.Buffer
	if err := server.removalTpl.Execute(&output, data); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"Preserved related library paths", "Removing the torrent does not remove them", "1 physical file · 2 paths"} {
		if !bytes.Contains(output.Bytes(), []byte(expected)) {
			t.Fatalf("torrent removal disclosure missing %q: %s", expected, html)
		}
	}
	if bytes.Contains(output.Bytes(), []byte(`name="unmanaged_path"`)) || bytes.Contains(output.Bytes(), []byte(`name="managed_file"`)) {
		t.Fatalf("torrent removal exposed a cross-owner action: %s", html)
	}
}

// TestTorrentRemovalDisclosesHardlinkedMediaButCannotSelectIt guards a real
// gap: removing a torrent that's hardlinked to a service-owned library file
// (not just an Unmanaged one) frees no space at all until that file is also
// removed — but the removal plan must never let a torrent-kind submission
// mutate a media-owned file directly (validateRemovalScope forbids
// "managed_file" for kind "torrent", since owned objects are only ever
// manipulated through their own service). The torrent removal view must
// disclose the hardlink and point at the media, without offering a
// checkbox that would just get rejected at admission.
func TestTorrentRemovalDisclosesHardlinkedMediaButCannotSelectIt(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := removalData{
		Plan:          removal.RemovalPlan{Kind: removal.TorrentObject, RequestedLabel: "Afterburn", Files: []removal.FileState{{Path: "/data/downloads/complete/Afterburn.mkv", Owner: removal.TorrentOwner}}},
		TorrentTarget: true, TorrentSelected: true, SelectedActions: 1, Hash: "abc",
		RelatedManaged: []relatedManagedMedia{{Media: model.MediaRef{Type: model.Movie, ServiceID: "radarr-1", SourceID: 42, Title: "Afterburn", Year: 2025}, FileCount: 1}},
		FileGroups:     []removalPhysicalFileGroup{{Paths: []string{"/data/downloads/complete/Afterburn.mkv"}}},
	}
	var output bytes.Buffer
	if err := server.removalTpl.Execute(&output, data); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"Hardlinked library files", "will not free this space", "Afterburn (2025)", "/library/movie/42?service_id=radarr-1"} {
		if !bytes.Contains(output.Bytes(), []byte(expected)) {
			t.Fatalf("torrent removal missing hardlinked-media disclosure %q: %s", expected, html)
		}
	}
	if bytes.Contains(output.Bytes(), []byte(`name="managed_file"`)) {
		t.Fatalf("torrent removal exposed a cross-owner managed_file action: %s", html)
	}
}

func TestRemovalSelectionCalculationIsLocal(t *testing.T) {
	script, err := uiFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"selectionChanged(event)", "this.calculate()", "selectedActionKeys()", "syncExecutionInputs(actions)", "actionSelections", "setControlSelection", "[data-physical-pick]"} {
		if !bytes.Contains(script, []byte(expected)) {
			t.Fatalf("local removal calculator does not contain %q", expected)
		}
	}
	selectionStart := bytes.Index(script, []byte("selectionChanged(event)"))
	selectionEnd := bytes.Index(script[selectionStart:], []byte("updateGroupStates()"))
	if selectionStart < 0 || selectionEnd < 0 || bytes.Contains(script[selectionStart:selectionStart+selectionEnd], []byte("fetch(")) {
		t.Fatal("selection changes must never make a network request")
	}
}

func TestLibraryTemplateRenders(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := libraryData{
		Rows: []mediaRow{{Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test", Year: 2026, RetentionValue: 12.5, SizeBytes: 1024}}},
		Page: 1, PageSize: 50, TotalPages: 1, TotalItems: 1, Sort: "title", Order: "asc", HasMediaLibrary: true,
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
	data := torrentData{Torrents: []model.Torrent{{Hash: "abc", Name: "Torrent", AssociationStatus: "ASSOCIATED"}}, TotalItems: 1, Page: 1, PageSize: 50, TotalPages: 1, Sort: "status", Order: "asc", HasTorrentClient: true, SortURLs: map[string]string{"status": "/torrents", "name": "/torrents", "media": "/torrents", "state": "/torrents", "size": "/torrents", "ratio": "/torrents", "upload": "/torrents", "seeds": "/torrents", "leechers": "/torrents", "activity": "/torrents"}, SizeLinks: []navLink{{Value: 50, URL: "/torrents"}}}
	var b bytes.Buffer
	if err := s.torrentTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
}

func TestFilterMedia(t *testing.T) {
	items := []model.Media{
		{Type: model.Movie, SourceID: 1, Title: "Alien", Requested: true, Views: 2, Torrents: []model.Torrent{{Hash: "a", AssociationStatus: model.TorrentCurrent}}, Tags: []string{"keep"}},
		{Type: model.Series, SourceID: 2, Title: "Severance", Requested: false, Views: 0},
	}
	got := filterMedia(items, "alien", "movie", "any", nil, "yes", "yes", "yes", true)
	if len(got) != 1 || got[0].Title != "Alien" {
		t.Fatalf("unexpected media filter result: %#v", got)
	}
	got = filterMedia(items, "", "series", "any", nil, "any", "no", "no", true)
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

func TestApplyUnmanagedSummaryBeforeFirstScan(t *testing.T) {
	var d homeData
	applyUnmanagedSummary(&d, nil, time.Time{}, nil)
	if d.UnmanagedAvailable {
		t.Fatal("unmanaged data must not be authoritative before first successful scan")
	}
	if d.UnmanagedError != "" {
		t.Fatalf("startup without a completed scan is not an error: %q", d.UnmanagedError)
	}
}

func TestApplyUnmanagedSummaryError(t *testing.T) {
	var d homeData
	applyUnmanagedSummary(&d, nil, time.Time{}, errors.New("qBittorrent unavailable"))
	if d.UnmanagedError != "qBittorrent unavailable" {
		t.Fatalf("unexpected error: %q", d.UnmanagedError)
	}
}

func TestFilterMediaHidesNoFileMediaByDefault(t *testing.T) {
	items := []model.Media{
		{Type: model.Movie, SourceID: 1, Title: "Present", SizeBytes: 1024},
		{Type: model.Movie, SourceID: 2, Title: "Missing", SizeBytes: 0},
	}
	got := filterMedia(items, "", "any", "any", nil, "any", "any", "any", false)
	if len(got) != 1 || got[0].Title != "Present" {
		t.Fatalf("unexpected default file filter: %#v", got)
	}
	got = filterMedia(items, "", "any", "any", nil, "any", "any", "any", true)
	if len(got) != 2 {
		t.Fatalf("show-no-files should include both media, got %d", len(got))
	}
}

func TestTasksTemplateRenders(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := tasksData{Groups: []taskGroup{{Name: "Library & Storage", Tasks: []tasks.Status{{
		ID: "inventory", Name: "Inventory refresh", Description: "Refresh state",
		Interval: time.Hour, LastDuration: 1500 * time.Millisecond, NextRun: time.Now().Add(time.Hour),
	}}}}}
	var b bytes.Buffer
	if err := s.tasksTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
}

func TestTaskGroupsHidesUnconfiguredServices(t *testing.T) {
	all := []tasks.Status{
		{ID: "inventory", Name: "Base inventory"},
		{ID: "files", Name: "File reconciliation"},
		{ID: removalTaskID, Name: "Removal operations"},
		{ID: autoRemovalTaskID, Name: "Automatic removal"},
		{ID: "jellyfin", Name: "Jellyfin enrichment"},
		{ID: "seerr", Name: "Seerr enrichment"},
	}

	server := &Server{inv: inventory.New(config.Config{}, nil)}
	groups := server.taskGroups(all)
	if len(groups) != 1 || groups[0].Name != "Library & Storage" {
		t.Fatalf("expected only the core group with neither service configured, got %#v", groups)
	}
	if len(groups[0].Tasks) != 4 {
		t.Fatalf("expected all four core tasks grouped together, got %#v", groups[0].Tasks)
	}

	cfg := config.Config{}
	cfg.Jellyfin.URL = "http://jellyfin"
	server = &Server{inv: inventory.New(cfg, nil)}
	groups = server.taskGroups(all)
	if len(groups) != 2 || groups[1].Name != "Jellyfin" || len(groups[1].Tasks) != 1 {
		t.Fatalf("expected a Jellyfin group once configured, got %#v", groups)
	}

	cfg.Seerr.URL = "http://seerr"
	server = &Server{inv: inventory.New(cfg, nil)}
	groups = server.taskGroups(all)
	if len(groups) != 3 || groups[1].Name != "Jellyfin" || groups[2].Name != "Seerr" {
		t.Fatalf("expected both enrichment groups once both are configured, got %#v", groups)
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
	current, superseded, unassociated := groupMediaTorrents(items)
	if len(current) != 1 || current[0].Hash != "c1" {
		t.Fatalf("unexpected current torrents: %#v", current)
	}
	if len(superseded) != 2 || superseded[0].Hash != "s1" || superseded[1].Hash != "s2" {
		t.Fatalf("unexpected superseded torrents: %#v", superseded)
	}
	if len(unassociated) != 2 || unassociated[0].Hash != "o1" || unassociated[1].Hash != "u1" {
		t.Fatalf("unexpected unassociated torrents: %#v", unassociated)
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
		Unassociated []model.Torrent
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
		TotalItems: 1, Page: 1, PageSize: 50, TotalPages: 1, Sort: "status", Order: "asc", HasTorrentClient: true,
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
		Media                             model.Media
		Rank, Total                       int
		Updated                           time.Time
		LastErr                           error
		Refreshing                        bool
		Current, Superseded, Unassociated []model.Torrent
		Files                             []inventory.FileView
		FileCount                         int
		FilesUpdated                      time.Time
		FilesErr                          error
		RemoveMedia                       inventory.RemovalEstimate
		RemoveWithCurrent                 inventory.RemovalEstimate
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
		{Path: "/data/downloads/a.mkv", Owner: removal.TorrentOwner, OwnerKey: "abc", Selected: true, Selectable: true, Exists: true, SizeBytes: 123, IdentityKnown: true, Device: 56, Inode: 99, Links: 2},
		{Path: "/data/Films/a.mkv", Owner: removal.MediaOwner, OwnerKey: "radarr:9", Selected: true, Selectable: true, Exists: true, SizeBytes: 123, IdentityKnown: true, Device: 56, Inode: 99, Links: 2},
	}
	groups := groupRemovalFiles(removal.RemovalPlan{Kind: removal.MediaObject, Files: files}, nil)
	if len(groups) != 1 {
		t.Fatalf("expected 1 physical group, got %d", len(groups))
	}
	if len(groups[0].Paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(groups[0].Paths))
	}
	if groups[0].SizeBytes != 123 || groups[0].Links != 2 {
		t.Fatalf("unexpected group: %+v", groups[0])
	}
	if !groups[0].Selectable || !groups[0].Selected || len(groups[0].DisplayPaths) != 2 {
		t.Fatalf("physical owner actions=%+v", groups[0])
	}
}

// TestGroupRemovalFilesMakesUnmanagedTargetSelectable guards the actual gap
// that kept standalone Unmanaged removal broken even after validateRemovalScope's
// blanket rejection was lifted: groupRemovalFiles had no switch case at all
// for removal.UnmanagedOwner, so every target file fell into the
// "not selectable" branch and rendered as "Preserved · not owned by this
// operation" regardless of what buildUnmanagedRemovalPlan intended.
func TestGroupRemovalFilesMakesUnmanagedTargetSelectable(t *testing.T) {
	files := []removal.FileState{
		{Path: "/data/downloads/orphan.iso", Owner: removal.UnmanagedOwner, OwnerKey: "/data/downloads/orphan.iso", Selected: true, Selectable: true, Exists: true, SizeBytes: 999},
	}
	groups := groupRemovalFiles(removal.RemovalPlan{Kind: removal.UnmanagedObject, Files: files}, nil)
	if len(groups) != 1 || len(groups[0].DisplayPaths) != 1 {
		t.Fatalf("expected 1 group with 1 display path, got %+v", groups)
	}
	display := groups[0].DisplayPaths[0]
	if !display.Selectable || !display.Selected {
		t.Fatalf("expected the unmanaged target to be selectable and selected, got %+v", display)
	}
	if display.ActionName != "path" || display.ActionValue != "/data/downloads/orphan.iso" {
		t.Fatalf("expected action name/value path=/data/downloads/orphan.iso, got %q=%q", display.ActionName, display.ActionValue)
	}
	if strings.Contains(display.ActionLabel, "Preserved") {
		t.Fatalf("expected the unmanaged target not to be marked Preserved, got %q", display.ActionLabel)
	}
}

func TestMediaPlanDefaultsPhysicallyHardlinkedTorrentAndRejectsUnrelatedTorrent(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "Films", "Movie.mkv")
	torrentPath := filepath.Join(root, "downloads", "Movie.mkv")
	otherMediaPath := filepath.Join(root, "Films", "Other.mkv")
	otherTorrentPath := filepath.Join(root, "downloads", "Other-linked.mkv")
	unrelatedPath := filepath.Join(root, "downloads", "Other.mkv")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(torrentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("shared movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(mediaPath, torrentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherMediaPath, []byte("other shared movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(otherMediaPath, otherTorrentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedPath, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	inspect := func(path string) model.File {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		return model.File{Path: path, Exists: true, SizeBytes: info.Size(), IdentityKnown: true, Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Links: uint64(stat.Nlink)}
	}
	files := []model.File{inspect(mediaPath), inspect(torrentPath), inspect(otherMediaPath), inspect(otherTorrentPath), inspect(unrelatedPath)}
	mediaRefs := []model.MediaFileRef{
		{MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 9, Path: mediaPath},
		{MediaType: model.Movie, MediaID: 2, Source: "radarr", SourceFileID: 10, Path: otherMediaPath},
	}
	torrentRefs := []model.TorrentFileRef{
		{Client: "qBittorrent", Hash: "linked", FileIndex: 0, Path: torrentPath},
		{Client: "qBittorrent", Hash: "linked", FileIndex: 1, Path: otherTorrentPath},
		{Client: "qBittorrent", Hash: "unrelated", FileIndex: 0, Path: unrelatedPath},
	}
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Movie"}, {Type: model.Movie, SourceID: 2, Title: "Other"}}
	mediaRelation := model.MediaRef{Type: model.Movie, SourceID: 1, Title: "Movie"}
	torrents := []model.Torrent{
		{Hash: "linked", Name: "Linked", AssociationStatus: model.TorrentUnassociated},
		{Hash: "copied", Name: "Copied current import", AssociationStatus: model.TorrentCurrent, MediaItems: []model.MediaRef{mediaRelation}},
		{Hash: "old", Name: "Superseded release", AssociationStatus: model.TorrentSuperseded, FormerMediaItems: []model.MediaRef{mediaRelation}},
		{Hash: "unrelated", Name: "Unrelated", AssociationStatus: model.TorrentUnassociated},
	}
	if err := database.PublishInventory(1, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	if err := database.PublishReconciliation(1, files, mediaRefs, torrentRefs, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	server := &Server{inv: inventory.New(config.Config{}, database)}
	plan, err := server.buildMediaRemovalPlan(model.Movie, 1, "", false, map[string]bool{}, map[string]bool{}, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Related) != 2 {
		t.Fatalf("related torrents=%#v", plan.Related)
	}
	selectedByHash := map[string]bool{}
	for _, related := range plan.Related {
		selectedByHash[related.Torrent.Hash] = related.Selected
	}
	if !selectedByHash["linked"] || selectedByHash["copied"] {
		t.Fatalf("physical Current must default selected and copied Current preserved: %#v", plan.Related)
	}
	if plan.RelatedTorrentCount != 3 || len(plan.RelatedGroups) != 2 {
		t.Fatalf("media relationship context missing Current/Superseded torrents: count=%d groups=%#v", plan.RelatedTorrentCount, plan.RelatedGroups)
	}
	for _, group := range plan.RelatedGroups {
		if group.Label == "Unassociated" {
			t.Fatalf("media removal exposed an Unassociated group: %#v", plan.RelatedGroups)
		}
	}
	if plan.Plan.ReclaimableBytes != int64(len("shared movie")) {
		t.Fatalf("reclaimable=%d", plan.Plan.ReclaimableBytes)
	}
	for _, file := range plan.Plan.Files {
		if file.OwnerKey == "radarr:10" && file.Selectable {
			t.Fatalf("another media owner's file became selectable: %#v", file)
		}
	}
	foundPreservedOwner := false
	for _, group := range plan.FileGroups {
		foundPreservedOwner = foundPreservedOwner || group.PreservedLinks > 0
	}
	if !foundPreservedOwner {
		t.Fatalf("multi-file torrent did not disclose the preserved other media owner: %#v", plan.FileGroups)
	}
	revalidated, err := server.buildMediaRemovalPlan(model.Movie, 1, "", true, selectedManagedSet([]string{"radarr:9"}), mapFromValues([]string{"linked"}), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(revalidated.Related) != 2 || !selectedTorrent(revalidated.Related, "linked") || selectedTorrent(revalidated.Related, "copied") {
		t.Fatalf("execution revalidation discarded related torrent: %#v", revalidated.Related)
	}
	if _, err := server.buildMediaRemovalPlan(model.Movie, 1, "", true, map[string]bool{"radarr:9": true}, map[string]bool{"unrelated": true}, map[string]bool{}); err == nil || !strings.Contains(err.Error(), "not current") {
		t.Fatalf("unrelated torrent selection error=%v", err)
	}
	if _, err := server.buildMediaRemovalPlan(model.Movie, 1, "", true, map[string]bool{"radarr:10": true}, map[string]bool{}, map[string]bool{}); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-media managed selection error=%v", err)
	}
}

func TestPhysicalCandidatesExposeUnmanagedHardlinkSibling(t *testing.T) {
	files := []model.File{
		{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100},
		{Path: "/series/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100},
	}
	trefs := []model.TorrentFileRef{{Client: "Downloader", Hash: "abc", FileIndex: 0, Path: "/downloads/a.mkv"}}
	cm := map[string]removal.CandidateFile{"/downloads/a.mkv": {Path: "/downloads/a.mkv", Owner: removal.TorrentOwner, OwnerKey: "abc", Selected: true}}
	got := physicalCandidates(files, nil, trefs, cm, nil, map[string]bool{"abc": true}, nil)
	var sibling removal.CandidateFile
	ok := false
	for _, candidate := range got {
		if candidate.Path == "/series/a.mkv" {
			sibling, ok = candidate, true
			break
		}
	}
	if !ok {
		t.Fatal("expected physical sibling candidate")
	}
	if sibling.Owner != removal.UnmanagedOwner {
		t.Fatalf("expected unmanaged sibling, got %s", sibling.Owner)
	}
}

func TestPhysicalCandidatesPreserveMultipleOwnerClaimsOnSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.mkv")
	if err := os.WriteFile(path, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	files := []model.File{{Path: path, Exists: true, SizeBytes: info.Size(), IdentityKnown: true, Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Links: uint64(stat.Nlink)}}
	mediaRef := model.MediaFileRef{MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 9, Path: path}
	torrentRef := model.TorrentFileRef{Hash: "abc", Path: path}
	initial := map[string]removal.CandidateFile{}
	mergeRemovalCandidate(initial, removal.CandidateFile{Path: path, Owner: removal.MediaOwner, OwnerKey: "radarr:9", Selected: true, Selectable: true})
	got := physicalCandidates(files, []model.MediaFileRef{mediaRef}, []model.TorrentFileRef{torrentRef}, initial, map[string]bool{"radarr:9": true}, map[string]bool{"abc": true}, nil)
	owners := map[removal.FileOwner]bool{}
	for _, candidate := range got {
		owners[candidate.Owner] = true
	}
	if len(got) != 2 || !owners[removal.MediaOwner] || !owners[removal.TorrentOwner] {
		t.Fatalf("same-path claims were collapsed: %#v", got)
	}
}

func TestGroupRemovalFilesReportsMissingHardlinks(t *testing.T) {
	states := []removal.FileState{{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100, Owner: removal.TorrentOwner}}
	inventoryFiles := []model.File{{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100}}
	groups := groupRemovalFiles(removal.RemovalPlan{Kind: removal.TorrentObject, RequestedKey: "abc", Files: states}, inventoryFiles)
	if len(groups) != 1 || groups[0].MissingLinks != 1 {
		t.Fatalf("expected one missing hardlink, got %+v", groups)
	}
}

func TestGroupUnmanagedFilesCollapsesHardlinks(t *testing.T) {
	items := []model.UnmanagedFile{
		{Path: "/a", SizeBytes: 54321, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
		{Path: "/b", SizeBytes: 54321, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
	}
	groups := groupUnmanagedFiles(items)
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
