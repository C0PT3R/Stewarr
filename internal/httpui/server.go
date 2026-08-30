package httpui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"connarr/internal/cleanup"
	"connarr/internal/inventory"
	"connarr/internal/model"
	"connarr/internal/product"
	"connarr/internal/removal"
	"connarr/internal/store"
	"connarr/internal/tasks"
)

type Server struct {
	inv              *inventory.Service
	tasks            *tasks.Manager
	homeTpl          *template.Template
	libraryTpl       *template.Template
	historyTpl       *template.Template
	profileTpl       *template.Template
	torrentTpl       *template.Template
	torrentDetailTpl *template.Template
	unmanagedTpl     *template.Template
	tasksTpl         *template.Template
	removalTpl       *template.Template
	operationTpl     *template.Template
	staticHandler    http.Handler
	revisions        *revisionHub
	startOnce        sync.Once
	admissionMu      sync.Mutex
	homeMu           sync.Mutex
	homeRevision     uint64
	homeCache        homeData
}

func New(inventoryService *inventory.Service, taskManager *tasks.Manager) (*Server, error) {
	templateFunctions := template.FuncMap{"appName": func() string { return product.Name }, "appVersion": func() string { return product.Version }, "head": appHead, "chrome": func(active string) template.HTML { return template.HTML(appChrome(active)) }, "human": func(bytes int64) string {
		if bytes < 0 {
			bytes = 0
		}
		return cleanup.Human(uint64(bytes))
	}, "humanU": func(bytes uint64) string { return cleanup.Human(bytes) }, "rate": func(bytesPerSecond int64) string { return cleanup.Human(uint64(max64(bytesPerSecond, 0))) + "/s" }, "duration": humanDuration, "durationGo": func(duration time.Duration) string { return humanDuration(int64(duration / time.Second)) }, "unixTime": unixTime, "pct": func(ratio float64) string { return fmt.Sprintf("%.1f%%", ratio*100) }, "fmtTime": func(timestamp *time.Time) string {
		if timestamp == nil {
			return "Never"
		}
		return timestamp.Local().Format("2006-01-02")
	}, "join": strings.Join, "add": func(first, second int) int { return first + second }, "managedKey": managedFileKey, "shortPath": shortPath, "widthPct": func(part, total uint64) string {
		if total == 0 {
			return "0"
		}
		return fmt.Sprintf("%.3f", float64(part)/float64(total)*100)
	}, "fmtUpdated": func(timestamp time.Time) string {
		if timestamp.IsZero() {
			return "Never"
		}
		return timestamp.Local().Format("2006-01-02 15:04:05")
	}}
	homeTemplate, err := parseUITemplate("home.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	libraryTemplate, err := parseUITemplate("library.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	historyTemplate, err := parseUITemplate("history.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	profileTemplate, err := parseUITemplate("media.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	torrentTemplate, err := parseUITemplate("torrents.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	torrentDetailTemplate, err := parseUITemplate("torrent.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	unmanagedTemplate, err := parseUITemplate("unmanaged.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	tasksTemplate, err := parseUITemplate("tasks.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	removalTemplate, err := parseUITemplate("removal.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	operationTemplate, err := parseUITemplate("operation.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	staticHandler, err := uiStaticHandler()
	if err != nil {
		return nil, err
	}
	server := &Server{inv: inventoryService, tasks: taskManager, homeTpl: homeTemplate, libraryTpl: libraryTemplate, historyTpl: historyTemplate, profileTpl: profileTemplate, torrentTpl: torrentTemplate, torrentDetailTpl: torrentDetailTemplate, unmanagedTpl: unmanagedTemplate, tasksTpl: tasksTemplate, removalTpl: removalTemplate, operationTpl: operationTemplate, staticHandler: staticHandler, revisions: newRevisionHub()}
	if taskManager != nil {
		if err := taskManager.Register(tasks.Definition{ID: removalTaskID, Name: "Removal operations", Description: "Execute durable owner and filesystem mutations.", PayloadRunner: server.runScheduledRemoval, Resources: []tasks.ResourceClaim{{Resource: "owner-filesystem-mutation", Mode: tasks.ClaimExclusive}}, Priority: tasks.PriorityMutation, Recovery: tasks.RecoveryAttention}); err != nil {
			return nil, err
		}
		if inventoryService != nil {
			if database := inventoryService.Store(); database != nil {
				if err := server.recoverScheduledRemovals(database); err != nil {
					return nil, err
				}
			}
		}
	}
	return server, nil
}
func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/", server.staticHandler)
	mux.HandleFunc("/ui/events", server.uiEvents)
	mux.HandleFunc("/ui/status", server.uiStatus)
	mux.HandleFunc("/", server.home)
	mux.HandleFunc("/library", server.library)
	mux.HandleFunc("/library/", server.media)
	mux.HandleFunc("/media/", server.media)
	mux.HandleFunc("/torrents", server.torrents)
	mux.HandleFunc("/torrents/", server.torrentDetail)
	mux.HandleFunc("/downloads/unmanaged", server.unmanagedDownloads)
	mux.HandleFunc("/downloads/unmanaged/scan", server.scanUnmanagedNow)
	mux.HandleFunc("/history", server.history)
	mux.HandleFunc("/tasks", server.tasksPage)
	mux.HandleFunc("/tasks/run", server.runTask)
	mux.HandleFunc("/removal/media", server.removalMedia)
	mux.HandleFunc("/removal/torrent", server.removalTorrent)
	mux.HandleFunc("/removal/unmanaged", server.removalUnmanaged)
	mux.HandleFunc("/removal/execute", server.executeRemoval)
	mux.HandleFunc("/api/media", server.apiMedia)
	mux.HandleFunc("/api/dashboard", server.apiDashboard)
	mux.HandleFunc("/api/torrents", server.apiTorrents)
	mux.HandleFunc("/api/unmanaged", server.apiUnmanaged)
	mux.HandleFunc("/api/files", server.apiFiles)
	mux.HandleFunc("/api/refresh", server.refresh)
	mux.HandleFunc("/api/plan", server.plan)
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte("ok")) })
	return sameOriginWrites(mux)
}

type tasksData struct {
	Tasks     []tasks.Status
	Workflows []tasks.WorkflowStatus
}

type operationPageData struct {
	Active     string
	FragmentID string
	Label      string
	Notice     operationNotice
	BackURL    string
	BackLabel  string
}

func (server *Server) renderOperation(response http.ResponseWriter, data operationPageData) {
	if strings.TrimSpace(data.Label) == "" {
		data.Label = "Removal operation"
	}
	if err := renderTemplate(response, server.operationTpl, data); err != nil {
		log.Printf("[http] render operation state: %v", err)
	}
}

func (server *Server) tasksPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tasks" {
		http.NotFound(w, r)
		return
	}
	if server.tasks == nil {
		log.Printf("[removal] [operation=0] rejected reason=%q", "durable scheduler unavailable")
		http.Error(w, "task manager unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := renderTemplate(w, server.tasksTpl, tasksData{Tasks: server.tasks.Snapshot(), Workflows: server.tasks.WorkflowStatuses()}); err != nil {
		log.Printf("[http] render tasks: %v", err)
	}
}

func (server *Server) runTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if server.tasks == nil {
		http.Error(w, "task manager unavailable", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}
	receipt, err := server.tasks.RunAsyncApp(id)
	if err != nil {
		http.Error(w, "task could not be scheduled: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("X-Connarr-Trigger-ID", receipt.TriggerID)
	http.Redirect(w, r, "/tasks?trigger="+url.QueryEscape(receipt.TriggerID), http.StatusSeeOther)
}

type mediaRow struct {
	Media     model.Media
	Removable bool
}

type navLink struct {
	Value int
	URL   string
}

func (server *Server) planningReliable(r inventory.Reliability) bool {
	return r.Inventory && r.Valuation && r.FileModel == "reliable" && (server.tasks == nil || !server.tasks.ConsistencyPending())
}

// deviceViews builds one cleanup plan per known storage device, since there
// is no longer a single global storage path to build one plan against.
func (server *Server) deviceViews(items []model.Media, planningReliable bool) []deviceView {
	devices := server.inv.StorageDevices()
	if len(devices) == 0 {
		return nil
	}
	mediaByDevice := server.inv.MediaByDevice(items)
	cfg := server.inv.Config()
	views := make([]deviceView, 0, len(devices))
	for _, device := range devices {
		p, planErr := cleanup.Build(device.RepresentativePath, cfg.Storage.TargetUsagePercent, cfg.Storage.CriticalUsagePercent, mediaByDevice[device.RepresentativePath], planningReliable)
		views = append(views, deviceView{Storage: device, Plan: p, PlanErr: planErr})
	}
	return views
}

type libraryData struct {
	Rows            []mediaRow
	Updated         time.Time
	LastErr         error
	Refreshing      bool
	TotalItems      int
	Page            int
	PageSize        int
	TotalPages      int
	HasPrev         bool
	HasNext         bool
	PrevURL         string
	NextURL         string
	PageLinks       []navLink
	Sort            string
	Order           string
	SortURLs        map[string]string
	SizeLinks       []navLink
	AllItems        int
	Query           string
	TypeFilter      string
	RequestedFilter string
	WatchedFilter   string
	TorrentFilter   string
	ShowNoFiles     bool
	ClearURL        string
}

// deviceView pairs one physical storage device with its own cleanup plan:
// removing the single global storage path means there is no longer one
// answer to "is cleanup needed," only a per-device one.
type deviceView struct {
	Storage inventory.StorageDevice
	Plan    cleanup.Plan
	PlanErr error
}

type homeData struct {
	Devices              []deviceView
	Reliability          inventory.Reliability
	Updated              time.Time
	LastErr              error
	Refreshing           bool
	TotalMedia           int
	Movies               int
	Series               int
	LibraryBytes         int64
	TotalTorrents        int
	Current              int
	Superseded           int
	Unassociated         int
	ObsoleteReclaimable  int64
	ObsoleteKnown        int
	UnmanagedFiles       int
	UnmanagedBytes       int64
	UnmanagedReclaimable int64
	UnmanagedAvailable   bool
	UnmanagedError       string
	Services             []inventory.ServiceStatus
	Stats                store.CleanupStats
}

func (server *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	d := server.dashboardSnapshot()
	if e := renderTemplate(w, server.homeTpl, d); e != nil {
		log.Printf("[http] render home: %v", e)
	}
}

func (server *Server) dashboardSnapshot() homeData {
	revision := server.revisions.current().Revision
	server.homeMu.Lock()
	defer server.homeMu.Unlock()
	if server.homeRevision == revision {
		return server.homeCache
	}
	items, updated, last := server.inv.Snapshot()
	projection := server.pendingProjection()
	items = projection.filterMedia(items)
	reliability := server.inv.ReliabilitySnapshot()
	planningReliable := server.planningReliable(reliability)
	d := homeData{Updated: updated, LastErr: last, Refreshing: server.inv.IsRefreshing(), Reliability: reliability, TotalMedia: len(items), Devices: server.deviceViews(items, planningReliable)}
	if !planningReliable && d.LastErr == nil {
		if server.tasks != nil && server.tasks.ConsistencyPending() {
			d.LastErr = fmt.Errorf("Automatic removal planning paused. Post-removal synchronization is pending")
		} else {
			d.LastErr = fmt.Errorf("Automatic removal planning paused. Jellyfin: %s; Seerr: %s; File topology: %s", reliability.Jellyfin, reliability.Seerr, reliability.FileModel)
		}
	}
	for _, svc := range server.inv.StatusSnapshot() {
		if svc.Configured {
			d.Services = append(d.Services, svc)
		}
	}
	for _, m := range items {
		d.LibraryBytes += m.SizeBytes
		if m.Type == model.Movie {
			d.Movies++
		} else if m.Type == model.Series {
			d.Series++
		}
	}
	ts := projection.filterTorrents(server.inv.TorrentSnapshot())
	d.TotalTorrents = len(ts)
	for _, t := range ts {
		switch normalizeTorrentStatus(t.AssociationStatus) {
		case model.TorrentCurrent:
			d.Current++
		case model.TorrentSuperseded:
			d.Superseded++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += t.ReclaimableBytes
			}
		default:
			d.Unassociated++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += t.ReclaimableBytes
			}
		}
	}
	ufs, unmanagedUpdated, ue := server.inv.UnmanagedSnapshot()
	ufs = projection.filterUnmanaged(ufs)
	applyUnmanagedSummary(&d, ufs, unmanagedUpdated, ue)
	if db := server.inv.Store(); db != nil {
		d.Stats, _ = db.CleanupStatistics()
	}
	server.homeRevision = revision
	server.homeCache = d
	return d
}

func applyUnmanagedSummary(d *homeData, files []model.UnmanagedFile, updated time.Time, scanErr error) {
	if scanErr != nil {
		d.UnmanagedError = scanErr.Error()
		return
	}
	if updated.IsZero() {
		// No successful scan has completed yet. This is a normal startup state,
		// not an error and not an empty authoritative result.
		return
	}
	d.UnmanagedAvailable = true
	d.UnmanagedFiles = len(files)
	for _, f := range files {
		d.UnmanagedBytes += f.SizeBytes
		d.UnmanagedReclaimable += f.ReclaimableBytes
	}
}

func normalizeBoolFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "true", "1":
		return "yes"
	case "no", "false", "0":
		return "no"
	default:
		return "any"
	}
}

func normalizeMediaTypeFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "movie":
		return "movie"
	case "series":
		return "series"
	default:
		return "any"
	}
}

func mediaMatchesSearch(m model.Media, q string) bool {
	if q == "" {
		return true
	}
	hay := []string{m.Title, m.Path, m.IMDBID, strings.Join(m.Tags, " "), fmt.Sprint(m.TMDBID), fmt.Sprint(m.TVDBID), fmt.Sprint(m.SourceID)}
	for _, v := range hay {
		if strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	return false
}

func filterMedia(items []model.Media, q, typeFilter, requestedFilter, watchedFilter, torrentFilter string, showNoFiles bool) []model.Media {
	out := make([]model.Media, 0, len(items))
	for _, m := range items {
		if !showNoFiles && m.SizeBytes <= 0 {
			continue
		}
		if !mediaMatchesSearch(m, q) {
			continue
		}
		if typeFilter != "any" && string(m.Type) != typeFilter {
			continue
		}
		if requestedFilter == "yes" && !m.Requested {
			continue
		}
		if requestedFilter == "no" && m.Requested {
			continue
		}
		watched := m.Views > 0 || m.LastWatched != nil
		if watchedFilter == "yes" && !watched {
			continue
		}
		if watchedFilter == "no" && watched {
			continue
		}
		hasTorrent := false
		for _, torrent := range m.Torrents {
			if normalizeTorrentStatus(torrent.AssociationStatus) == model.TorrentCurrent {
				hasTorrent = true
				break
			}
		}
		if torrentFilter == "yes" && !hasTorrent {
			continue
		}
		if torrentFilter == "no" && hasTorrent {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (server *Server) library(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/library" {
		http.NotFound(w, r)
		return
	}
	items, updated, last := server.inv.Snapshot()
	items = server.pendingProjection().filterMedia(items)
	reliability := server.inv.ReliabilitySnapshot()
	planningReliable := server.planningReliable(reliability)
	if !planningReliable && last == nil {
		if server.tasks != nil && server.tasks.ConsistencyPending() {
			last = fmt.Errorf("Automatic removal planning paused. Post-removal synchronization is pending")
		} else {
			last = fmt.Errorf("Automatic removal planning paused. Jellyfin: %s; Seerr: %s; File topology: %s", reliability.Jellyfin, reliability.Seerr, reliability.FileModel)
		}
	}

	allItems := len(items)

	qtext := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	typeFilter := normalizeMediaTypeFilter(r.URL.Query().Get("type"))
	requestedFilter := normalizeBoolFilter(r.URL.Query().Get("requested"))
	watchedFilter := normalizeBoolFilter(r.URL.Query().Get("watched"))
	torrentFilter := normalizeBoolFilter(r.URL.Query().Get("torrent"))
	showNoFiles := r.URL.Query().Get("show_no_files") == "1"
	items = filterMedia(items, qtext, typeFilter, requestedFilter, watchedFilter, torrentFilter, showNoFiles)

	pageSize := allowedPageSize(queryInt(r, "page_size", 50))
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	sortKey := r.URL.Query().Get("sort")
	if !validMediaSort(sortKey) {
		sortKey = "title"
	}
	order := strings.ToLower(r.URL.Query().Get("order"))
	if order != "asc" && order != "desc" {
		order = "asc"
	}
	sortMediaItems(items, sortKey, order)

	total := len(items)
	totalPages := 1
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	if page > totalPages {
		page = totalPages
	}
	from := (page - 1) * pageSize
	if from > total {
		from = total
	}
	to := from + pageSize
	if to > total {
		to = total
	}
	rows := make([]mediaRow, 0, to-from)
	for _, m := range items[from:to] {
		rows = append(rows, mediaRow{Media: m, Removable: m.SizeBytes > 0 || len(m.Torrents) > 0})
	}

	mkURL := func(pg, size int, sk, ord string) string {
		q := url.Values{}
		q.Set("page", strconv.Itoa(pg))
		q.Set("page_size", strconv.Itoa(size))
		q.Set("sort", sk)
		q.Set("order", ord)
		if qtext != "" {
			q.Set("q", qtext)
		}
		if typeFilter != "any" {
			q.Set("type", typeFilter)
		}
		if requestedFilter != "any" {
			q.Set("requested", requestedFilter)
		}
		if watchedFilter != "any" {
			q.Set("watched", watchedFilter)
		}
		if torrentFilter != "any" {
			q.Set("torrent", torrentFilter)
		}
		if showNoFiles {
			q.Set("show_no_files", "1")
		}
		return "/library?" + q.Encode()
	}
	sortURLs := map[string]string{}
	for _, key := range []string{"value", "title", "type", "rating", "votes", "views", "lastwatched", "requested", "size", "torrents"} {
		nextOrder := defaultSortOrder(key)
		if sortKey == key {
			if order == "asc" {
				nextOrder = "desc"
			} else {
				nextOrder = "asc"
			}
		}
		sortURLs[key] = mkURL(1, pageSize, key, nextOrder)
	}
	sizeLinks := []navLink{}
	for _, size := range []int{25, 50, 100, 250} {
		sizeLinks = append(sizeLinks, navLink{Value: size, URL: mkURL(1, size, sortKey, order)})
	}
	pageLinks := []navLink{}
	for pg := maxInt(1, page-2); pg <= minInt(totalPages, page+2); pg++ {
		pageLinks = append(pageLinks, navLink{Value: pg, URL: mkURL(pg, pageSize, sortKey, order)})
	}
	d := libraryData{Rows: rows, Updated: updated, LastErr: last, Refreshing: server.inv.IsRefreshing(), TotalItems: total, AllItems: allItems, Page: page, PageSize: pageSize, TotalPages: totalPages, HasPrev: page > 1, HasNext: page < totalPages, Sort: sortKey, Order: order, SortURLs: sortURLs, SizeLinks: sizeLinks, PageLinks: pageLinks, Query: r.URL.Query().Get("q"), TypeFilter: typeFilter, RequestedFilter: requestedFilter, WatchedFilter: watchedFilter, TorrentFilter: torrentFilter, ShowNoFiles: showNoFiles, ClearURL: "/library"}
	if d.HasPrev {
		d.PrevURL = mkURL(page-1, pageSize, sortKey, order)
	}
	if d.HasNext {
		d.NextURL = mkURL(page+1, pageSize, sortKey, order)
	}
	if e := renderTemplate(w, server.libraryTpl, d); e != nil {
		log.Printf("[http] render library: %v", e)
	}
}

func renderTemplate(w http.ResponseWriter, tpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		http.Error(w, "template rendering failed", http.StatusInternalServerError)
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(buf.Bytes())
	return err
}

func queryInt(r *http.Request, key string, fallback int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	return v
}
func allowedPageSize(v int) int {
	for _, n := range []int{25, 50, 100, 250} {
		if v == n {
			return v
		}
	}
	return 50
}
func validMediaSort(v string) bool {
	switch v {
	case "value", "title", "type", "rating", "votes", "views", "lastwatched", "requested", "size", "torrents":
		return true
	}
	return false
}
func defaultSortOrder(key string) string {
	switch key {
	case "value", "title", "type":
		return "asc"
	}
	return "desc"
}
func sortMediaItems(items []model.Media, key, order string) {
	dir := 1
	if order == "desc" {
		dir = -1
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		cmp := 0
		switch key {
		case "value":
			if a.RetentionValue < b.RetentionValue {
				cmp = -1
			} else if a.RetentionValue > b.RetentionValue {
				cmp = 1
			}
		case "title":
			cmp = strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		case "type":
			cmp = strings.Compare(string(a.Type), string(b.Type))
		case "rating":
			if a.Rating < b.Rating {
				cmp = -1
			} else if a.Rating > b.Rating {
				cmp = 1
			}
		case "votes":
			cmp = a.VoteCount - b.VoteCount
		case "views":
			cmp = a.Views - b.Views
		case "lastwatched":
			av, bv := int64(0), int64(0)
			if a.LastWatched != nil {
				av = a.LastWatched.Unix()
			}
			if b.LastWatched != nil {
				bv = b.LastWatched.Unix()
			}
			if av < bv {
				cmp = -1
			} else if av > bv {
				cmp = 1
			}
		case "requested":
			if !a.Requested && b.Requested {
				cmp = -1
			} else if a.Requested && !b.Requested {
				cmp = 1
			}
		case "size":
			if a.SizeBytes < b.SizeBytes {
				cmp = -1
			} else if a.SizeBytes > b.SizeBytes {
				cmp = 1
			}
		case "torrents":
			cmp = len(a.Torrents) - len(b.Torrents)
		}
		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		}
		return cmp*dir < 0
	})
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func groupMediaTorrents(items []model.Torrent) (current, superseded, unassociated []model.Torrent) {
	for _, t := range items {
		switch normalizeTorrentStatus(t.AssociationStatus) {
		case model.TorrentCurrent:
			current = append(current, t)
		case model.TorrentSuperseded:
			superseded = append(superseded, t)
		case model.TorrentUnassociated:
			unassociated = append(unassociated, t)
		}
	}
	byName := func(items []model.Torrent) {
		sort.SliceStable(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	}
	byName(current)
	byName(superseded)
	byName(unassociated)
	return
}

func (server *Server) media(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/library/") {
		path = strings.TrimPrefix(path, "/library/")
	} else {
		path = strings.TrimPrefix(path, "/media/")
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	var sourceID int
	if _, err := fmt.Sscanf(parts[1], "%d", &sourceID); err != nil || sourceID <= 0 {
		http.NotFound(w, r)
		return
	}
	projection := server.pendingProjection()
	mediaType := model.MediaType(parts[0])
	if notice, pending := projection.mediaOperation(mediaType, sourceID); pending {
		server.renderOperation(w, operationPageData{Active: "library", FragmentID: "media-detail", Label: notice.Label, Notice: notice, BackURL: "/library", BackLabel: "Library"})
		return
	}

	items, updated, last := server.inv.Snapshot()
	reliability := server.inv.ReliabilitySnapshot()
	if !reliability.Valuation && last == nil {
		last = fmt.Errorf("%s Jellyfin: %s; Seerr: %s", reliability.Message, reliability.Jellyfin, reliability.Seerr)
	}
	for i := range items {
		m := items[i]
		if string(m.Type) != parts[0] || m.SourceID != sourceID {
			continue
		}
		m.Torrents = projection.filterTorrents(m.Torrents)
		current, superseded, unassociated := groupMediaTorrents(m.Torrents)
		storageView, filesUpdated, filesErr := server.inv.MediaStorage(m.Type, m.SourceID)
		files := storageView.Files
		if len(projection.ManagedFiles) > 0 {
			refs, _, _ := server.inv.ManagedFileRefs(m.Type, m.SourceID)
			suppressedPaths := map[string]bool{}
			for _, ref := range refs {
				if _, pending := projection.ManagedFiles[managedFileKey(ref)]; pending {
					suppressedPaths[filepath.Clean(ref.Path)] = true
				}
			}
			visible := files[:0]
			for _, file := range files {
				if !suppressedPaths[filepath.Clean(file.File.Path)] {
					visible = append(visible, file)
				}
			}
			files = visible
		}
		fileCount := len(files)
		if len(files) > 25 {
			files = files[:25]
		}

		data := struct {
			Media             model.Media
			Updated           time.Time
			LastErr           error
			Refreshing        bool
			Current           []model.Torrent
			Superseded        []model.Torrent
			Unassociated      []model.Torrent
			Files             []inventory.FileView
			FileCount         int
			FilesUpdated      time.Time
			FilesErr          error
			RemoveMedia       inventory.RemovalEstimate
			RemoveWithCurrent inventory.RemovalEstimate
		}{m, updated, last, server.inv.IsRefreshing(), current, superseded, unassociated, files, fileCount, filesUpdated, filesErr, storageView.RemoveMedia, storageView.RemoveWithCurrent}
		if e := renderTemplate(w, server.profileTpl, data); e != nil {
			log.Printf("[http] render media profile: %v", e)
		}
		return
	}
	if notice, found := server.removalHistory("media", fmt.Sprintf("%s:%d", mediaType, sourceID)); found {
		server.renderOperation(w, operationPageData{Active: "library", FragmentID: "media-detail", Label: notice.Label, Notice: notice, BackURL: "/library", BackLabel: "Library"})
		return
	}
	http.NotFound(w, r)
}

type torrentData struct {
	Torrents                          []model.Torrent
	Updated                           time.Time
	LastErr                           error
	Refreshing                        bool
	Current, Superseded, Unassociated int
	ObsoleteReclaimable               int64
	ObsoleteKnown                     int
	TotalItems                        int
	Page                              int
	PageSize                          int
	TotalPages                        int
	HasPrev, HasNext                  bool
	PrevURL, NextURL                  string
	PageLinks                         []navLink
	SizeLinks                         []navLink
	Sort, Order                       string
	SortURLs                          map[string]string
	AllItems                          int
	Query                             string
	StatusFilter                      string
	ReclaimableFilter                 string
	ActivityFilter                    string
	ClearURL                          string
}

func normalizeTorrentStatus(v string) string {
	return model.NormalizeTorrentStatus(v)
}

func validTorrentSort(v string) bool {
	switch v {
	case "status", "value", "name", "media", "state", "size", "reclaimable", "ratio", "upload", "seeds", "leechers", "activity":
		return true
	}
	return false
}

func sortTorrents(items []model.Torrent, key, order string) {
	dir := 1
	if order == "desc" {
		dir = -1
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		cmp := 0
		switch key {
		case "value":
			if a.SwarmValue < b.SwarmValue {
				cmp = -1
			} else if a.SwarmValue > b.SwarmValue {
				cmp = 1
			}
		case "status":
			cmp = strings.Compare(normalizeTorrentStatus(a.AssociationStatus), normalizeTorrentStatus(b.AssociationStatus))
		case "name":
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "media":
			cmp = len(a.MediaItems) - len(b.MediaItems)
		case "state":
			cmp = strings.Compare(a.State, b.State)
		case "size":
			if a.SizeBytes < b.SizeBytes {
				cmp = -1
			} else if a.SizeBytes > b.SizeBytes {
				cmp = 1
			}
		case "reclaimable":
			if a.ReclaimableBytes < b.ReclaimableBytes {
				cmp = -1
			} else if a.ReclaimableBytes > b.ReclaimableBytes {
				cmp = 1
			}
		case "ratio":
			if a.Ratio < b.Ratio {
				cmp = -1
			} else if a.Ratio > b.Ratio {
				cmp = 1
			}
		case "upload":
			if a.UploadSpeed < b.UploadSpeed {
				cmp = -1
			} else if a.UploadSpeed > b.UploadSpeed {
				cmp = 1
			}
		case "seeds":
			cmp = a.SeedsSwarm - b.SeedsSwarm
		case "leechers":
			cmp = a.LeechersSwarm - b.LeechersSwarm
		case "activity":
			if a.LastActivity < b.LastActivity {
				cmp = -1
			} else if a.LastActivity > b.LastActivity {
				cmp = 1
			}
		}
		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		return cmp*dir < 0
	})
}

func normalizeTorrentStatusFilter(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "CURRENT", "ASSOCIATED", "OPEN":
		return model.TorrentCurrent
	case "SUPERSEDED":
		return model.TorrentSuperseded
	case "UNASSOCIATED", "ORPHANED", "UNMATCHED":
		return model.TorrentUnassociated
	default:
		return "ANY"
	}
}
func normalizeReclaimableFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "positive", "known", "unknown":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "any"
	}
}
func normalizeActivityFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "active", "inactive":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "any"
	}
}
func torrentActive(t model.Torrent) bool {
	return t.UploadSpeed > 0 || t.DownloadSpeed > 0 || t.SeedsConnected > 0 || t.LeechersConnected > 0 || strings.Contains(strings.ToLower(t.State), "downloading") || strings.Contains(strings.ToLower(t.State), "uploading")
}
func torrentMatchesSearch(t model.Torrent, q string) bool {
	if q == "" {
		return true
	}
	hay := []string{t.Name, t.Hash, t.Tracker, t.Category, t.Tags, t.State, t.SavePath, t.ContentPath, t.Client, t.AssociationReason}
	for _, m := range t.MediaItems {
		hay = append(hay, m.Title, fmt.Sprint(m.SourceID))
	}
	for _, m := range t.FormerMediaItems {
		hay = append(hay, m.Title, fmt.Sprint(m.SourceID))
	}
	for _, v := range hay {
		if strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	return false
}
func filterTorrents(items []model.Torrent, q, status, reclaimable, activity string) []model.Torrent {
	if status != "ANY" {
		status = normalizeTorrentStatus(status)
	}
	out := make([]model.Torrent, 0, len(items))
	for _, t := range items {
		if !torrentMatchesSearch(t, q) {
			continue
		}
		if status != "ANY" && normalizeTorrentStatus(t.AssociationStatus) != status {
			continue
		}
		switch reclaimable {
		case "known":
			if !t.ReclaimableKnown {
				continue
			}
		case "unknown":
			if t.ReclaimableKnown {
				continue
			}
		case "positive":
			if !t.ReclaimableKnown || t.ReclaimableBytes <= 0 {
				continue
			}
		}
		a := torrentActive(t)
		if activity == "active" && !a {
			continue
		}
		if activity == "inactive" && a {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (server *Server) torrents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/torrents" {
		http.NotFound(w, r)
		return
	}
	_, updated, last := server.inv.Snapshot()
	all := server.pendingProjection().filterTorrents(server.inv.TorrentSnapshot())
	allItems := len(all)
	counts := map[string]int{model.TorrentCurrent: 0, model.TorrentSuperseded: 0, model.TorrentUnassociated: 0}
	var obsoleteReclaimable int64
	obsoleteKnown := 0
	for i := range all {
		all[i].AssociationStatus = normalizeTorrentStatus(all[i].AssociationStatus)
		counts[all[i].AssociationStatus]++
		if all[i].AssociationStatus != model.TorrentCurrent && all[i].ReclaimableKnown {
			obsoleteKnown++
			obsoleteReclaimable += all[i].ReclaimableBytes
		}
	}

	qtext := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	statusFilter := normalizeTorrentStatusFilter(r.URL.Query().Get("status"))
	reclaimableFilter := normalizeReclaimableFilter(r.URL.Query().Get("reclaimable"))
	activityFilter := normalizeActivityFilter(r.URL.Query().Get("activity"))
	all = filterTorrents(all, qtext, statusFilter, reclaimableFilter, activityFilter)

	pageSize := allowedPageSize(queryInt(r, "page_size", 50))
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	sortKey := r.URL.Query().Get("sort")
	if !validTorrentSort(sortKey) {
		sortKey = "status"
	}
	order := strings.ToLower(r.URL.Query().Get("order"))
	if order != "asc" && order != "desc" {
		order = "asc"
	}
	sortTorrents(all, sortKey, order)
	total := len(all)
	pages := 1
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	if page > pages {
		page = pages
	}
	from := (page - 1) * pageSize
	if from > total {
		from = total
	}
	to := from + pageSize
	if to > total {
		to = total
	}
	mk := func(pg, size int, sk, ord string) string {
		q := url.Values{}
		q.Set("page", strconv.Itoa(pg))
		q.Set("page_size", strconv.Itoa(size))
		q.Set("sort", sk)
		q.Set("order", ord)
		if qtext != "" {
			q.Set("q", qtext)
		}
		if statusFilter != "ANY" {
			q.Set("status", strings.ToLower(statusFilter))
		}
		if reclaimableFilter != "any" {
			q.Set("reclaimable", reclaimableFilter)
		}
		if activityFilter != "any" {
			q.Set("activity", activityFilter)
		}
		return "/torrents?" + q.Encode()
	}
	sortURLs := map[string]string{}
	for _, k := range []string{"status", "value", "name", "media", "state", "size", "reclaimable", "ratio", "upload", "seeds", "leechers", "activity"} {
		no := "asc"
		if k == "size" || k == "reclaimable" || k == "ratio" || k == "upload" || k == "seeds" || k == "leechers" || k == "activity" {
			no = "desc"
		}
		if sortKey == k {
			if order == "asc" {
				no = "desc"
			} else {
				no = "asc"
			}
		}
		sortURLs[k] = mk(1, pageSize, k, no)
	}
	sizes := []navLink{}
	for _, n := range []int{25, 50, 100, 250} {
		sizes = append(sizes, navLink{Value: n, URL: mk(1, n, sortKey, order)})
	}
	links := []navLink{}
	for pg := maxInt(1, page-2); pg <= minInt(pages, page+2); pg++ {
		links = append(links, navLink{Value: pg, URL: mk(pg, pageSize, sortKey, order)})
	}
	d := torrentData{Torrents: all[from:to], Updated: updated, LastErr: last, Refreshing: server.inv.IsRefreshing(), Current: counts[model.TorrentCurrent], Superseded: counts[model.TorrentSuperseded], Unassociated: counts[model.TorrentUnassociated], ObsoleteKnown: obsoleteKnown, ObsoleteReclaimable: obsoleteReclaimable, TotalItems: total, AllItems: allItems, Page: page, PageSize: pageSize, TotalPages: pages, HasPrev: page > 1, HasNext: page < pages, PageLinks: links, SizeLinks: sizes, Sort: sortKey, Order: order, SortURLs: sortURLs, Query: r.URL.Query().Get("q"), StatusFilter: statusFilter, ReclaimableFilter: reclaimableFilter, ActivityFilter: activityFilter, ClearURL: "/torrents"}
	if d.HasPrev {
		d.PrevURL = mk(page-1, pageSize, sortKey, order)
	}
	if d.HasNext {
		d.NextURL = mk(page+1, pageSize, sortKey, order)
	}
	if e := renderTemplate(w, server.torrentTpl, d); e != nil {
		log.Printf("[http] render torrents: %v", e)
	}
}

type unmanagedFileGroup struct {
	Paths            []model.UnmanagedFile
	FirstPath        string
	SizeBytes        int64
	ModifiedAt       time.Time
	Device           uint64
	Inode            uint64
	Links            uint64
	ReclaimableKnown bool
	ReclaimableBytes int64
	SharedBytes      int64
	MissingLinks     int
}

type unmanagedData struct {
	Files                                     []unmanagedFileGroup
	Updated                                   time.Time
	ScanErr                                   error
	TotalItems, AllItems                      int
	TotalBytes, ReclaimableBytes, SharedBytes int64
	Page, PageSize, TotalPages                int
	HasPrev, HasNext                          bool
	PrevURL, NextURL                          string
	PageLinks, SizeLinks                      []navLink
	Sort, Order, Query                        string
	SortURLs                                  map[string]string
}

func groupUnmanagedFiles(items []model.UnmanagedFile) []unmanagedFileGroup {
	type groupKey struct {
		device uint64
		inode  uint64
		path   string
	}
	groups := map[groupKey]*unmanagedFileGroup{}
	order := make([]groupKey, 0, len(items))
	for _, f := range items {
		cp := filepath.Clean(f.Path)
		key := groupKey{path: cp}
		if f.Device != 0 || f.Inode != 0 {
			key = groupKey{device: f.Device, inode: f.Inode}
		}
		g := groups[key]
		if g == nil {
			g = &unmanagedFileGroup{SizeBytes: f.SizeBytes, ModifiedAt: f.ModifiedAt, Device: f.Device, Inode: f.Inode, Links: f.Links, ReclaimableKnown: f.ReclaimableKnown}
			groups[key] = g
			order = append(order, key)
		}
		if f.ModifiedAt.After(g.ModifiedAt) {
			g.ModifiedAt = f.ModifiedAt
		}
		if f.Links > g.Links {
			g.Links = f.Links
		}
		g.ReclaimableKnown = g.ReclaimableKnown && f.ReclaimableKnown
		f.Path = cp
		g.Paths = append(g.Paths, f)
	}
	out := make([]unmanagedFileGroup, 0, len(order))
	for _, key := range order {
		g := groups[key]
		sort.Slice(g.Paths, func(i, j int) bool { return strings.ToLower(g.Paths[i].Path) < strings.ToLower(g.Paths[j].Path) })
		if len(g.Paths) > 0 {
			g.FirstPath = g.Paths[0].Path
		}
		if g.Links > uint64(len(g.Paths)) {
			g.MissingLinks = int(g.Links - uint64(len(g.Paths)))
		}
		if g.ReclaimableKnown {
			if g.Links <= 1 || uint64(len(g.Paths)) >= g.Links {
				g.ReclaimableBytes = g.SizeBytes
			} else {
				g.SharedBytes = g.SizeBytes
			}
		}
		out = append(out, *g)
	}
	return out
}

func validUnmanagedSort(v string) bool {
	switch v {
	case "path", "size", "reclaimable", "links", "modified":
		return true
	}
	return false
}
func sortUnmanaged(items []unmanagedFileGroup, key, order string) {
	dir := 1
	if order == "desc" {
		dir = -1
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		cmp := 0
		switch key {
		case "path":
			cmp = strings.Compare(strings.ToLower(a.FirstPath), strings.ToLower(b.FirstPath))
		case "size":
			if a.SizeBytes < b.SizeBytes {
				cmp = -1
			} else if a.SizeBytes > b.SizeBytes {
				cmp = 1
			}
		case "reclaimable":
			if a.ReclaimableBytes < b.ReclaimableBytes {
				cmp = -1
			} else if a.ReclaimableBytes > b.ReclaimableBytes {
				cmp = 1
			}
		case "links":
			if a.Links < b.Links {
				cmp = -1
			} else if a.Links > b.Links {
				cmp = 1
			}
		case "modified":
			if a.ModifiedAt.Before(b.ModifiedAt) {
				cmp = -1
			} else if a.ModifiedAt.After(b.ModifiedAt) {
				cmp = 1
			}
		}
		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(a.FirstPath), strings.ToLower(b.FirstPath))
		}
		return cmp*dir < 0
	})
}

func (server *Server) scanUnmanagedNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var err error
	if server.tasks != nil {
		err = server.tasks.Run(r.Context(), "files")
	} else {
		err = server.inv.ScanUnmanaged(r.Context())
	}
	if r.Header.Get("X-Connarr-Scan") == "1" {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": err == nil, "error": func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}()})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/downloads/unmanaged", http.StatusSeeOther)
}

func (server *Server) unmanagedDownloads(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/downloads/unmanaged" {
		http.NotFound(w, r)
		return
	}
	raw, updated, scanErr := server.inv.UnmanagedSnapshot()
	raw = server.pendingProjection().filterUnmanaged(raw)
	all := groupUnmanagedFiles(raw)
	allItems := len(all)
	var totalBytes, reclaimableBytes, sharedBytes int64
	for _, f := range all {
		totalBytes += f.SizeBytes
		reclaimableBytes += f.ReclaimableBytes
		sharedBytes += f.SharedBytes
	}
	qtext := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if qtext != "" {
		filtered := make([]unmanagedFileGroup, 0, len(all))
		for _, f := range all {
			match := false
			for _, p := range f.Paths {
				if strings.Contains(strings.ToLower(p.Path), qtext) {
					match = true
					break
				}
			}
			if match {
				filtered = append(filtered, f)
			}
		}
		all = filtered
	}
	pageSize := allowedPageSize(queryInt(r, "page_size", 50))
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	sortKey := r.URL.Query().Get("sort")
	if !validUnmanagedSort(sortKey) {
		sortKey = "reclaimable"
	}
	order := strings.ToLower(r.URL.Query().Get("order"))
	if order != "asc" && order != "desc" {
		order = "desc"
	}
	sortUnmanaged(all, sortKey, order)
	total := len(all)
	pages := 1
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	if page > pages {
		page = pages
	}
	from := (page - 1) * pageSize
	if from > total {
		from = total
	}
	to := from + pageSize
	if to > total {
		to = total
	}
	mk := func(pg, size int, sk, ord string) string {
		q := url.Values{}
		q.Set("page", strconv.Itoa(pg))
		q.Set("page_size", strconv.Itoa(size))
		q.Set("sort", sk)
		q.Set("order", ord)
		if qtext != "" {
			q.Set("q", qtext)
		}
		return "/downloads/unmanaged?" + q.Encode()
	}
	sortURLs := map[string]string{}
	for _, k := range []string{"path", "size", "reclaimable", "links", "modified"} {
		no := "asc"
		if k != "path" {
			no = "desc"
		}
		if sortKey == k {
			if order == "asc" {
				no = "desc"
			} else {
				no = "asc"
			}
		}
		sortURLs[k] = mk(1, pageSize, k, no)
	}
	sizes := []navLink{}
	for _, n := range []int{25, 50, 100, 250} {
		sizes = append(sizes, navLink{n, mk(1, n, sortKey, order)})
	}
	links := []navLink{}
	for pg := maxInt(1, page-2); pg <= minInt(pages, page+2); pg++ {
		links = append(links, navLink{pg, mk(pg, pageSize, sortKey, order)})
	}
	d := unmanagedData{Files: all[from:to], Updated: updated, ScanErr: scanErr, TotalItems: total, AllItems: allItems, TotalBytes: totalBytes, ReclaimableBytes: reclaimableBytes, SharedBytes: sharedBytes, Page: page, PageSize: pageSize, TotalPages: pages, HasPrev: page > 1, HasNext: page < pages, PageLinks: links, SizeLinks: sizes, Sort: sortKey, Order: order, Query: r.URL.Query().Get("q"), SortURLs: sortURLs}
	if d.HasPrev {
		d.PrevURL = mk(page-1, pageSize, sortKey, order)
	}
	if d.HasNext {
		d.NextURL = mk(page+1, pageSize, sortKey, order)
	}
	if e := renderTemplate(w, server.unmanagedTpl, d); e != nil {
		log.Printf("[http] render unmanaged files: %v", e)
	}
}

type historyEventView struct {
	store.HistoryEvent
	Files   []removal.FileState
	Results []string
	Errors  []string
}

func historyEventViews(events []store.HistoryEvent) []historyEventView {
	views := make([]historyEventView, 0, len(events))
	for _, event := range events {
		view := historyEventView{HistoryEvent: event}
		var payload struct {
			Plan struct {
				Files []removal.FileState `json:"files"`
			} `json:"plan"`
			Results []string `json:"results"`
			Errors  []string `json:"errors"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			view.Files = payload.Plan.Files
			view.Results = payload.Results
			view.Errors = payload.Errors
		}
		views = append(views, view)
	}
	return views
}

func (server *Server) history(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/history" {
		http.NotFound(w, r)
		return
	}
	var stats store.CleanupStats
	var runs []store.CleanupRun
	var events []store.HistoryEvent
	if db := server.inv.Store(); db != nil {
		stats, _ = db.CleanupStatistics()
		runs, _ = db.CleanupRuns(100)
		events, _ = db.HistoryEvents(200)
	}
	d := struct {
		Stats  store.CleanupStats
		Runs   []store.CleanupRun
		Events []historyEventView
	}{stats, runs, historyEventViews(events)}
	if e := renderTemplate(w, server.historyTpl, d); e != nil {
		log.Printf("[http] render history: %v", e)
	}
}

func (server *Server) torrentDetail(w http.ResponseWriter, r *http.Request) {
	hash := strings.Trim(strings.TrimPrefix(r.URL.Path, "/torrents/"), "/")
	if hash == "" || strings.Contains(hash, "/") {
		http.NotFound(w, r)
		return
	}
	if notice, pending := server.pendingProjection().torrentOperation(hash); pending {
		server.renderOperation(w, operationPageData{Active: "torrents", FragmentID: "torrent-detail", Label: notice.Label, Notice: notice, BackURL: "/torrents", BackLabel: "Torrents"})
		return
	}
	_, updated, last := server.inv.Snapshot()
	torrent, detailErr := server.inv.TorrentDetail(hash)
	if strings.Contains(strings.ToLower(detailErrString(detailErr)), "not found") {
		if notice, found := server.removalHistory("torrent", strings.ToLower(hash)); found {
			server.renderOperation(w, operationPageData{Active: "torrents", FragmentID: "torrent-detail", Label: notice.Label, Notice: notice, BackURL: "/torrents", BackLabel: "Torrents"})
			return
		}
		http.NotFound(w, r)
		return
	}
	torrent.AssociationStatus = normalizeTorrentStatus(torrent.AssociationStatus)
	storageView, filesUpdated, filesErr := server.inv.TorrentStorage(hash)
	files := storageView.Files
	fileCount := len(files)
	if len(files) > 50 {
		files = files[:50]
	}
	data := struct {
		Torrent       model.Torrent
		Updated       time.Time
		LastErr       error
		Refreshing    bool
		DetailErr     error
		Files         []inventory.FileView
		FileCount     int
		FilesUpdated  time.Time
		FilesErr      error
		RemoveTorrent inventory.RemovalEstimate
	}{torrent, updated, last, server.inv.IsRefreshing(), detailErr, files, fileCount, filesUpdated, filesErr, storageView.RemoveTorrent}
	if e := renderTemplate(w, server.torrentDetailTpl, data); e != nil {
		log.Printf("[http] render torrent detail: %v", e)
	}
}

func (server *Server) apiMedia(w http.ResponseWriter, r *http.Request) {
	x, u, e := server.inv.Snapshot()
	writeJSON(w, map[string]any{"items": x, "updated": u, "error": errString(e), "refreshing": server.inv.IsRefreshing(), "reliability": server.inv.ReliabilitySnapshot()})
}

type dashboardAPI struct {
	Revision            uint64                    `json:"revision"`
	TotalMedia          int                       `json:"totalMedia"`
	Movies              int                       `json:"movies"`
	Series              int                       `json:"series"`
	LibraryBytes        string                    `json:"libraryBytes"`
	TotalTorrents       int                       `json:"totalTorrents"`
	Current             int                       `json:"current"`
	Superseded          int                       `json:"superseded"`
	Unassociated        int                       `json:"unassociated"`
	ObsoleteReclaimable string                    `json:"obsoleteReclaimable"`
	ObsoleteKnown       int                       `json:"obsoleteKnown"`
	Stats               store.CleanupStats        `json:"stats"`
	Services            []inventory.ServiceStatus `json:"services,omitempty"`
}

func (server *Server) apiDashboard(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "GET only", http.StatusMethodNotAllowed)
		return
	}
	revision := server.revisions.current().Revision
	etag := `"dashboard-` + strconv.FormatUint(revision, 10) + `"`
	response.Header().Set("ETag", etag)
	response.Header().Set("Cache-Control", "no-cache")
	if request.Header.Get("If-None-Match") == etag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	data := server.dashboardSnapshot()
	writeJSON(response, dashboardAPI{
		Revision:   revision,
		TotalMedia: data.TotalMedia, Movies: data.Movies, Series: data.Series, LibraryBytes: cleanup.Human(uint64(max64(data.LibraryBytes, 0))),
		TotalTorrents: data.TotalTorrents, Current: data.Current, Superseded: data.Superseded, Unassociated: data.Unassociated,
		ObsoleteReclaimable: cleanup.Human(uint64(max64(data.ObsoleteReclaimable, 0))), ObsoleteKnown: data.ObsoleteKnown,
		Stats: data.Stats, Services: data.Services,
	})
}
func (server *Server) apiTorrents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": server.inv.TorrentSnapshot(), "refreshing": server.inv.IsRefreshing()})
}
func (server *Server) apiUnmanaged(w http.ResponseWriter, r *http.Request) {
	x, u, e := server.inv.UnmanagedSnapshot()
	writeJSON(w, map[string]any{"items": x, "updated": u, "error": errString(e), "refreshing": server.inv.IsRefreshing()})
}
func (server *Server) apiFiles(w http.ResponseWriter, r *http.Request) {
	x, mr, tr, u, e := server.inv.FileSnapshot()
	writeJSON(w, map[string]any{"items": x, "mediaRefs": mr, "torrentRefs": tr, "updated": u, "error": errString(e)})
}
func (server *Server) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if server.tasks != nil {
		server.tasks.RunAsyncApp("inventory")
	} else {
		go func() {
			if e := server.inv.Refresh(context.Background()); e != nil {
				log.Printf("[inventory] refresh: %v", e)
			}
		}()
	}
	http.Redirect(w, r, "/", 303)
}
func (server *Server) plan(w http.ResponseWriter, r *http.Request) {
	x, _, _ := server.inv.Snapshot()
	reliability := server.inv.ReliabilitySnapshot()
	views := server.deviceViews(x, server.planningReliable(reliability))
	plans := make([]cleanup.Plan, 0, len(views))
	for _, view := range views {
		plans = append(plans, view.Plan)
	}
	writeJSON(w, plans)
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func detailErrString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func Run(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	log.Printf("[http] %s listening on %s", product.Name, addr)
	e := srv.ListenAndServe()
	if e == http.ErrServerClosed {
		return nil
	}
	return fmt.Errorf("http: %w", e)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func humanDuration(sec int64) string {
	if sec < 0 || sec >= 8640000 {
		return "—"
	}
	d := time.Duration(sec) * time.Second
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int((d%(24*time.Hour))/time.Hour))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm %ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	}
	return fmt.Sprintf("%ds", sec)
}
func shortPath(v string) string {
	const max = 88
	if len(v) <= max {
		return v
	}
	const left, right = 34, 51
	if len(v) <= left+right+3 {
		return v
	}
	return v[:left] + "..." + v[len(v)-right:]
}

func unixTime(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}

type relatedRemovalTorrent struct {
	Torrent         model.Torrent
	Selected        bool
	Selectable      bool
	PhysicallyBacks bool
	FileCount       int
}

type relatedRemovalTorrentGroup struct {
	Label    string
	Torrents []relatedRemovalTorrent
}

type relatedUnmanagedFile struct {
	Path      string
	Selected  bool
	SizeBytes int64
}

type managedRemovalFile struct {
	Ref         model.MediaFileRef
	File        model.File
	Selected    bool
	Filename    string
	Label       string
	PhysicalKey string
}

type managedRemovalGroup struct {
	Label        string
	Files        []managedRemovalFile
	AllSelected  bool
	SomeSelected bool
	Complete     bool
	TotalFiles   int
	SizeBytes    int64
}

type relatedManagedMedia struct {
	Media        model.MediaRef
	Groups       []managedRemovalGroup
	FileCount    int
	AllSelected  bool
	SomeSelected bool
}

type removalDisplayPath struct {
	Label       string
	Path        string
	Text        string
	ActionName  string
	ActionValue string
	ActionLabel string
	Selectable  bool
	Selected    bool
}

type removalPhysicalFileGroup struct {
	Key            string
	Paths          []string
	DisplayPaths   []removalDisplayPath
	SizeBytes      int64
	Exists         bool
	IdentityKnown  bool
	Links          uint64
	MissingLinks   uint64
	PreservedLinks int
	Error          string
	Selectable     bool
	Selected       bool
	SomeSelected   bool
}

type removalData struct {
	Plan                  removal.RemovalPlan
	FileGroups            []removalPhysicalFileGroup
	TorrentTarget         bool
	TorrentSelected       bool
	SelectedActions       int
	PotentialBytes        int64
	Related               []relatedRemovalTorrent
	RelatedGroups         []relatedRemovalTorrentGroup
	RelatedTorrentCount   int
	SelectedTorrentCount  int
	PreservedTorrentCount int
	RelatedUnmanaged      []relatedUnmanagedFile
	ManagedGroups         []managedRemovalGroup
	ManagedFileCount      int
	ManagedAllSelected    bool
	ManagedSomeSelected   bool
	RelatedManaged        []relatedManagedMedia
	SelectedManaged       []model.MediaFileRef
	CanUnmonitorMovies    bool
	CanUnmonitorEpisodes  bool
	CanExcludeMovies      bool
	CanExcludeSeries      bool
	ExclusionMedia        []model.Media
	ManagedSectionLabel   string
	SelectedFileCount     int
	SelectedLogicalBytes  int64
	StorageGuidanceTitle  string
	StorageGuidanceAction string
	BackURL               string
	MediaType             string
	MediaID               int
	Hash                  string
	UnmanagedPaths        []string
	SelectedUnmanaged     []string
	SelectionModel        string
	OperationToken        string
}

// validateRemovalScope is the coarse server-side command boundary. Exact
// owner identities and every cross-owner physical relationship are rebuilt and
// authorized from current topology inside the scheduler execution boundary.
func validateRemovalScope(form url.Values) error {
	forbidden := func(names ...string) error {
		for _, name := range names {
			if len(form[name]) != 0 {
				return fmt.Errorf("%s removal cannot mutate %s", form.Get("kind"), name)
			}
		}
		return nil
	}
	switch form.Get("kind") {
	case "media":
		if err := forbidden("unmanaged_path", "target", "path"); err != nil {
			return err
		}
	case "torrent":
		if err := forbidden("managed_file", "unmanaged_path", "torrent", "path", "unmonitor_movies", "unmonitor_episodes", "exclude_movies", "exclude_series"); err != nil {
			return err
		}
		if form.Get("target") != "1" {
			return fmt.Errorf("torrent removal requires the torrent target")
		}
	case "unmanaged":
		return fmt.Errorf("direct filesystem removal is disabled until the path belongs to an explicitly delegated Connarr cleanup root")
	}
	return nil
}

func groupRelatedTorrents(torrents []relatedRemovalTorrent) []relatedRemovalTorrentGroup {
	groupsByLabel := map[string][]relatedRemovalTorrent{}
	for _, relatedTorrent := range torrents {
		label := "Other"
		switch model.NormalizeTorrentStatus(relatedTorrent.Torrent.AssociationStatus) {
		case model.TorrentCurrent:
			label = "Current"
		case model.TorrentSuperseded:
			label = "Superseded"
		case model.TorrentUnassociated:
			label = "Unassociated"
		}
		groupsByLabel[label] = append(groupsByLabel[label], relatedTorrent)
	}
	groups := make([]relatedRemovalTorrentGroup, 0, 3)
	for _, label := range []string{"Current", "Superseded", "Unassociated", "Other"} {
		if len(groupsByLabel[label]) > 0 {
			groups = append(groups, relatedRemovalTorrentGroup{Label: label, Torrents: groupsByLabel[label]})
		}
	}
	return groups
}

func selectedUnmanagedSet(values []string) map[string]bool {
	m := map[string]bool{}
	for _, p := range values {
		p = filepath.Clean(strings.TrimSpace(p))
		if p != "" && p != "." {
			m[p] = true
		}
	}
	return m
}

func physicalCandidates(allFiles []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef, initial map[string]removal.CandidateFile, selectedManaged map[string]bool, selectedTorrents map[string]bool, selectedUnmanaged map[string]bool) map[string]removal.CandidateFile {
	normalized := map[string]removal.CandidateFile{}
	for _, candidate := range initial {
		mergeRemovalCandidate(normalized, candidate)
	}
	initial = normalized
	byPath := filesByPath(allFiles)
	ids := map[physicalID]bool{}
	for _, candidate := range initial {
		if f, ok := byPath[filepath.Clean(candidate.Path)]; ok && f.Exists && f.IdentityKnown {
			ids[physicalID{f.Device, f.Inode}] = true
		}
	}
	mediaByPath := map[string][]model.MediaFileRef{}
	for _, r := range mediaRefs {
		path := filepath.Clean(r.Path)
		mediaByPath[path] = append(mediaByPath[path], r)
	}
	torrentByPath := map[string][]model.TorrentFileRef{}
	for _, r := range torrentRefs {
		path := filepath.Clean(r.Path)
		torrentByPath[path] = append(torrentByPath[path], r)
	}
	for key, candidate := range initial {
		switch candidate.Owner {
		case removal.MediaOwner:
			candidate.Selected = selectedManaged[candidate.OwnerKey]
		case removal.TorrentOwner:
			candidate.Selected = selectedTorrents[strings.ToLower(candidate.OwnerKey)]
		case removal.UnmanagedOwner:
			candidate.Selected = selectedUnmanaged[filepath.Clean(candidate.Path)] || candidate.Selected
		}
		initial[key] = candidate
	}
	for _, f := range allFiles {
		if !f.Exists || !f.IdentityKnown || !ids[physicalID{f.Device, f.Inode}] {
			continue
		}
		p := filepath.Clean(f.Path)
		owned := false
		for _, r := range mediaByPath[p] {
			owned = true
			key := managedFileKey(r)
			mergeRemovalCandidate(initial, removal.CandidateFile{Path: p, Owner: removal.MediaOwner, OwnerKey: key, Label: filepath.Base(p), Selected: selectedManaged[key]})
		}
		for _, r := range torrentByPath[p] {
			owned = true
			h := strings.ToLower(r.Hash)
			mergeRemovalCandidate(initial, removal.CandidateFile{Path: p, Owner: removal.TorrentOwner, OwnerKey: h, Label: filepath.Base(p), Selected: selectedTorrents[h]})
		}
		if !owned {
			mergeRemovalCandidate(initial, removal.CandidateFile{Path: p, Owner: removal.UnmanagedOwner, OwnerKey: p, Label: filepath.Base(p), Selected: selectedUnmanaged[p]})
		}
	}
	return initial
}

func physicallyBackingTorrentHashes(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) map[string]bool {
	byPath := filesByPath(files)
	mediaIdentities := map[physicalID]bool{}
	for _, ref := range mediaRefs {
		file, ok := byPath[filepath.Clean(ref.Path)]
		if ok && file.Exists && file.IdentityKnown {
			mediaIdentities[physicalID{file.Device, file.Inode}] = true
		}
	}
	hashes := map[string]bool{}
	for _, ref := range torrentRefs {
		file, ok := byPath[filepath.Clean(ref.Path)]
		if !ok || !file.Exists || !file.IdentityKnown || !mediaIdentities[physicalID{file.Device, file.Inode}] {
			continue
		}
		if hash := strings.ToLower(strings.TrimSpace(ref.Hash)); hash != "" {
			hashes[hash] = true
		}
	}
	return hashes
}

func relatedUnmanagedFromCandidates(cm map[string]removal.CandidateFile, files map[string]model.File) []relatedUnmanagedFile {
	out := []relatedUnmanagedFile{}
	for p, c := range cm {
		if c.Owner != removal.UnmanagedOwner {
			continue
		}
		f := files[filepath.Clean(p)]
		out = append(out, relatedUnmanagedFile{Path: p, Selected: c.Selected, SizeBytes: f.SizeBytes})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out
}

func selectedUnmanagedPaths(xs []relatedUnmanagedFile) []string {
	out := []string{}
	for _, x := range xs {
		if x.Selected {
			out = append(out, x.Path)
		}
	}
	return out
}

func relatedTorrentList(cm map[string]removal.CandidateFile, torrents []model.Torrent, exclude string) []relatedRemovalTorrent {
	wanted := map[string]bool{}
	selected := map[string]bool{}
	for _, c := range cm {
		if c.Owner == removal.TorrentOwner {
			h := strings.ToLower(c.OwnerKey)
			if h != "" && !strings.EqualFold(h, exclude) {
				wanted[h] = true
				if c.Selected {
					selected[h] = true
				}
			}
		}
	}
	out := []relatedRemovalTorrent{}
	for _, t := range torrents {
		h := strings.ToLower(t.Hash)
		if wanted[h] {
			out = append(out, relatedRemovalTorrent{Torrent: t, Selected: selected[h]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Torrent.Name) < strings.ToLower(out[j].Torrent.Name)
	})
	return out
}

func rootCounts(files []model.File) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, f := range files {
		for _, c := range f.StorageContexts {
			key := c.IntegrationID
			if key == "" {
				key = strings.ToLower(c.IntegrationName)
			}
			if out[key] == nil {
				out[key] = map[string]bool{}
			}
			out[key][filepath.Clean(c.Root)] = true
		}
	}
	return out
}

func displayPath(path string, f model.File, owner removal.FileOwner, ownerName string, counts map[string]map[string]bool) removalDisplayPath {
	p := filepath.Clean(path)
	best := model.StorageContext{}
	bestLen := -1
	for _, c := range f.StorageContexts {
		if underPath(c.Root, p) && len(c.Root) > bestLen {
			best = c
			bestLen = len(c.Root)
		}
	}
	label := ownerName
	if label == "" {
		label = best.IntegrationName
	}
	if label == "" {
		label = "Storage"
	}
	if best.Root != "" {
		key := best.IntegrationID
		if key == "" {
			key = strings.ToLower(best.IntegrationName)
		}
		if roots := counts[key]; len(roots) > 1 && best.RootLabel != "" && !strings.EqualFold(best.RootLabel, best.IntegrationName) {
			label += " · " + best.RootLabel
		}
		if rel, err := filepath.Rel(best.Root, p); err == nil {
			p = "/" + filepath.ToSlash(rel)
			if p == "/." {
				p = "/"
			}
		}
	}
	if owner == removal.UnmanagedOwner {
		if best.IntegrationName != "" {
			label += " · Unmanaged"
		} else {
			label = "Unmanaged"
		}
	}
	return removalDisplayPath{Label: label, Path: path, Text: p}
}

func underPath(root, p string) bool {
	rel, e := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func groupRemovalFiles(plan removal.RemovalPlan, inventoryFiles []model.File) []removalPhysicalFileGroup {
	files := plan.Files
	invByPath := filesByPath(inventoryFiles)
	counts := rootCounts(inventoryFiles)
	type physicalKey struct {
		device uint64
		inode  uint64
	}
	groups := make([]removalPhysicalFileGroup, 0, len(files))
	byPhysical := make(map[physicalKey]int)
	byPath := make(map[string]int)
	groupForState := make([]int, 0, len(files))
	for _, f := range files {
		groupIndex := -1
		if f.Exists && f.IdentityKnown {
			key := physicalKey{device: f.Device, inode: f.Inode}
			if idx, ok := byPhysical[key]; ok {
				groupIndex = idx
			} else {
				groupIndex = len(groups)
				byPhysical[key] = groupIndex
				groups = append(groups, removalPhysicalFileGroup{
					Key: fmt.Sprintf("%d:%d", f.Device, f.Inode), SizeBytes: f.SizeBytes, Exists: true,
					IdentityKnown: true, Links: f.Links, Error: f.Error,
				})
			}
		} else {
			path := filepath.Clean(f.Path)
			if idx, ok := byPath[path]; ok {
				groupIndex = idx
			} else {
				groupIndex = len(groups)
				byPath[path] = groupIndex
				groups = append(groups, removalPhysicalFileGroup{
					Key: "path:" + path, SizeBytes: f.SizeBytes, Exists: f.Exists,
					IdentityKnown: f.IdentityKnown, Links: f.Links, Error: f.Error,
				})
			}
		}
		group := &groups[groupIndex]
		if f.Links > group.Links {
			group.Links = f.Links
		}
		if group.Error == "" && f.Error != "" {
			group.Error = f.Error
		}
		seenPath := false
		for _, path := range group.Paths {
			seenPath = seenPath || filepath.Clean(path) == filepath.Clean(f.Path)
		}
		if !seenPath {
			group.Paths = append(group.Paths, f.Path)
		}
		groupForState = append(groupForState, groupIndex)
	}
	selectedPaths := make([]map[string]bool, len(groups))
	for index, state := range files {
		if state.Selected {
			if selectedPaths[groupForState[index]] == nil {
				selectedPaths[groupForState[index]] = map[string]bool{}
			}
			selectedPaths[groupForState[index]][filepath.Clean(state.Path)] = true
		}
	}
	preservedPaths := make([]map[string]bool, len(groups))
	for i := range groups {
		sort.Strings(groups[i].Paths)
		if groups[i].IdentityKnown && groups[i].Links > uint64(len(groups[i].Paths)) {
			groups[i].MissingLinks = groups[i].Links - uint64(len(groups[i].Paths))
		}
	}
	for stateIndex, state := range files {
		groupIndex := groupForState[stateIndex]
		group := &groups[groupIndex]
		path := filepath.Clean(state.Path)
		display := displayPath(state.Path, invByPath[path], state.Owner, "", counts)
		display.Selected = state.Selected
		switch state.Owner {
		case removal.MediaOwner:
			if plan.Kind == removal.MediaObject && state.Selectable {
				display.ActionName, display.ActionValue = "managed_file", state.OwnerKey
				display.ActionLabel, display.Selectable = "Remove managed file", true
			}
		case removal.TorrentOwner:
			if plan.Kind == removal.MediaObject && state.Selectable {
				display.ActionName, display.ActionValue = "torrent", state.OwnerKey
				display.ActionLabel, display.Selectable = "Remove torrent and its complete data set", true
				if strings.TrimSpace(state.Label) != "" {
					display.ActionLabel = fmt.Sprintf("Remove torrent %q and its complete data set", state.Label)
				}
			} else if plan.Kind == removal.TorrentObject && state.Selectable && strings.EqualFold(state.OwnerKey, plan.RequestedKey) {
				display.ActionName, display.ActionValue = "target", "1"
				display.ActionLabel, display.Selectable = "Remove torrent and its complete data set", true
			}
		}
		if !display.Selectable {
			if selectedPaths[groupIndex][path] {
				display.ActionLabel = "Affected · this shared path is removed by another selected owner action"
			} else {
				display.ActionLabel = "Preserved · not owned by this operation"
				if state.Exists {
					if preservedPaths[groupIndex] == nil {
						preservedPaths[groupIndex] = map[string]bool{}
					}
					preservedPaths[groupIndex][path] = true
				}
			}
			display.Selected = false
		} else {
			group.Selectable = true
			group.SomeSelected = group.SomeSelected || display.Selected
		}
		group.DisplayPaths = append(group.DisplayPaths, display)
	}
	for i := range groups {
		groups[i].PreservedLinks = len(preservedPaths[i])
		groups[i].Selected = groups[i].Selectable
		for _, display := range groups[i].DisplayPaths {
			if display.Selectable && !display.Selected {
				groups[i].Selected = false
			}
		}
	}
	return groups
}

func mergeRemovalCandidate(m map[string]removal.CandidateFile, c removal.CandidateFile) {
	c.Path = filepath.Clean(c.Path)
	key := fmt.Sprintf("%s\x00%s\x00%s", c.Owner, strings.ToLower(strings.TrimSpace(c.OwnerKey)), c.Path)
	if old, ok := m[key]; ok {
		old.Selected = old.Selected || c.Selected
		old.Selectable = old.Selectable || c.Selectable
		if old.Label == "" {
			old.Label = c.Label
		}
		m[key] = old
		return
	}
	m[key] = c
}

func candidateSlice(m map[string]removal.CandidateFile) []removal.CandidateFile {
	out := make([]removal.CandidateFile, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Owner != out[j].Owner {
			return out[i].Owner < out[j].Owner
		}
		return out[i].OwnerKey < out[j].OwnerKey
	})
	return out
}

func managedFileKey(r model.MediaFileRef) string {
	return strings.ToLower(strings.TrimSpace(r.Source)) + ":" + strconv.Itoa(r.SourceFileID)
}

func mediaRefFor(items []model.Media, kind model.MediaType, id int) (model.MediaRef, bool) {
	for _, m := range items {
		if m.Type == kind && m.SourceID == id {
			return model.MediaRef{Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}, true
		}
	}
	return model.MediaRef{}, false
}

func groupManagedFiles(refs []model.MediaFileRef, files map[string]model.File, selected map[string]bool) []managedRemovalGroup {
	byGroup := map[string][]managedRemovalFile{}
	order := map[string]int{}
	for _, r := range refs {
		f, ok := files[filepath.Clean(r.Path)]
		if !ok {
			continue
		}
		group := "Files"
		label := filepath.Base(r.Path)
		ord := 1 << 30
		if len(r.Parts) > 0 {
			group = r.Parts[0].Group
			if group == "" {
				group = "Files"
			}
			labels := make([]string, 0, len(r.Parts))
			ord = r.Parts[0].Order
			for _, part := range r.Parts {
				if part.Label != "" {
					labels = append(labels, part.Label)
				}
				if part.Order < ord {
					ord = part.Order
				}
			}
			if len(labels) > 0 {
				label = strings.Join(labels, " / ")
			}
		}
		if cur, ok := order[group]; !ok || ord < cur {
			order[group] = ord
		}
		physicalKey := "path:" + filepath.Clean(f.Path)
		if f.Exists && f.IdentityKnown {
			physicalKey = fmt.Sprintf("%d:%d", f.Device, f.Inode)
		}
		byGroup[group] = append(byGroup[group], managedRemovalFile{
			Ref: r, File: f, Selected: selected[managedFileKey(r)],
			Filename: filepath.Base(r.Path), Label: label, PhysicalKey: physicalKey,
		})
	}
	groups := make([]managedRemovalGroup, 0, len(byGroup))
	for label, xs := range byGroup {
		sort.Slice(xs, func(i, j int) bool {
			oi, oj := 1<<30, 1<<30
			if len(xs[i].Ref.Parts) > 0 {
				oi = xs[i].Ref.Parts[0].Order
			}
			if len(xs[j].Ref.Parts) > 0 {
				oj = xs[j].Ref.Parts[0].Order
			}
			if oi != oj {
				return oi < oj
			}
			return strings.ToLower(xs[i].Label) < strings.ToLower(xs[j].Label)
		})
		all, some := len(xs) > 0, false
		for _, x := range xs {
			if x.Selected {
				some = true
			} else {
				all = false
			}
		}
		var size int64
		for _, x := range xs {
			size += x.File.SizeBytes
		}
		groups = append(groups, managedRemovalGroup{Label: label, Files: xs, AllSelected: all, SomeSelected: some, Complete: true, TotalFiles: len(xs), SizeBytes: size})
	}
	sort.Slice(groups, func(i, j int) bool {
		oi, oj := order[groups[i].Label], order[groups[j].Label]
		if oi != oj {
			return oi < oj
		}
		return strings.ToLower(groups[i].Label) < strings.ToLower(groups[j].Label)
	})
	return groups
}

func filesByPath(files []model.File) map[string]model.File {
	m := map[string]model.File{}
	for _, f := range files {
		m[filepath.Clean(f.Path)] = f
	}
	return m
}

type physicalID struct{ dev, ino uint64 }

func provenManagedRefsForTorrent(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef, hash string) []model.MediaFileRef {
	byPath := filesByPath(files)
	ids := map[physicalID]bool{}
	for _, tr := range torrentRefs {
		if !strings.EqualFold(tr.Hash, hash) {
			continue
		}
		if f, ok := byPath[filepath.Clean(tr.Path)]; ok && f.Exists && f.IdentityKnown {
			ids[physicalID{f.Device, f.Inode}] = true
		}
	}
	seen := map[string]bool{}
	out := []model.MediaFileRef{}
	for _, mr := range mediaRefs {
		f, ok := byPath[filepath.Clean(mr.Path)]
		if !ok || !f.Exists || !f.IdentityKnown || !ids[physicalID{f.Device, f.Inode}] {
			continue
		}
		key := managedFileKey(mr)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, mr)
	}
	return out
}

func selectedManagedRefs(refs []model.MediaFileRef, selected map[string]bool) []model.MediaFileRef {
	out := []model.MediaFileRef{}
	for _, r := range refs {
		if selected[managedFileKey(r)] {
			out = append(out, r)
		}
	}
	return out
}

func ownerOptions(refs []model.MediaFileRef) (movies, episodes bool) {
	for _, r := range refs {
		switch strings.ToLower(r.Source) {
		case "radarr":
			movies = true
		case "sonarr":
			for _, p := range r.Parts {
				if p.SourcePartID > 0 {
					episodes = true
					break
				}
			}
		}
	}
	return
}

func managedSectionLabel(mediaType model.MediaType) string {
	switch mediaType {
	case model.Movie:
		return "Movie files"
	case model.Series:
		return "Episode files"
	default:
		return "Library files"
	}
}

func selectedFileSummary(plan removal.RemovalPlan) (int, int64) {
	type physicalIdentity struct {
		device uint64
		inode  uint64
	}
	seenPhysical := map[physicalIdentity]bool{}
	seenPaths := map[string]bool{}
	count := 0
	var sizeBytes int64
	for _, file := range plan.Files {
		if !file.Selected || file.Owner != removal.MediaOwner {
			continue
		}
		count++
		if file.Exists && file.IdentityKnown {
			identity := physicalIdentity{device: file.Device, inode: file.Inode}
			if !seenPhysical[identity] {
				seenPhysical[identity] = true
				sizeBytes += file.SizeBytes
			}
			continue
		}
		cleanPath := filepath.Clean(file.Path)
		if !seenPaths[cleanPath] {
			seenPaths[cleanPath] = true
			sizeBytes += file.SizeBytes
		}
	}
	return count, sizeBytes
}

func storageGuidance(plan removal.RemovalPlan, mediaType model.MediaType, related []relatedRemovalTorrent) (string, string) {
	statusByHash := map[string]string{}
	for _, relatedTorrent := range related {
		statusByHash[strings.ToLower(relatedTorrent.Torrent.Hash)] = strings.ToUpper(relatedTorrent.Torrent.AssociationStatus)
	}
	type physicalIdentity struct {
		device uint64
		inode  uint64
	}
	type physicalSelection struct {
		selectedMedia bool
		selectedPaths int
		linkCount     uint64
		blockers      map[string]string
	}
	physicalFiles := map[physicalIdentity]*physicalSelection{}
	for _, file := range plan.Files {
		if !file.Exists || !file.IdentityKnown {
			continue
		}
		identity := physicalIdentity{device: file.Device, inode: file.Inode}
		selection := physicalFiles[identity]
		if selection == nil {
			selection = &physicalSelection{linkCount: file.Links, blockers: map[string]string{}}
			physicalFiles[identity] = selection
		}
		if file.Selected {
			selection.selectedPaths++
			if file.Owner == removal.MediaOwner {
				selection.selectedMedia = true
			}
		} else if file.Owner == removal.TorrentOwner {
			hash := strings.ToLower(file.OwnerKey)
			selection.blockers[hash] = statusByHash[hash]
		}
	}
	blockingHashes := map[string]string{}
	for _, selection := range physicalFiles {
		if !selection.selectedMedia || uint64(selection.selectedPaths) >= selection.linkCount {
			continue
		}
		for hash, status := range selection.blockers {
			blockingHashes[hash] = status
		}
	}
	if len(blockingHashes) == 0 {
		return "", ""
	}
	fileName := "library file"
	if mediaType == model.Movie {
		fileName = "movie file"
	} else if mediaType == model.Series {
		fileName = "episode file"
	}
	selectedFiles, _ := selectedFileSummary(plan)
	possessive := "its"
	if selectedFiles != 1 {
		fileName += "s"
		possessive = "their"
	}
	title := fmt.Sprintf("Removing the selected %s alone will not reclaim all of %s storage space.", fileName, possessive)
	if plan.ReclaimableBytes == 0 {
		title = fmt.Sprintf("Removing the selected %s alone will not reclaim storage space.", fileName)
	}
	allCurrent := true
	for _, status := range blockingHashes {
		if model.NormalizeTorrentStatus(status) != model.TorrentCurrent {
			allCurrent = false
			break
		}
	}
	if len(blockingHashes) == 1 && allCurrent {
		pronoun := "It is"
		if selectedFiles != 1 {
			pronoun = "They are"
		}
		return title, pronoun + " hardlinked to the current torrent. Also select that torrent below to reclaim the shared data."
	}
	if allCurrent {
		return title, "They are hardlinked to current torrents. Also select those torrents below to reclaim the shared data."
	}
	return title, "They are hardlinked to related torrents. Also select those torrents below to reclaim the shared data."
}

func exclusionOptions(refs []model.MediaFileRef, mediaItems []model.Media) (bool, bool, []model.Media) {
	selectedMedia := map[string]bool{}
	for _, ref := range refs {
		selectedMedia[fmt.Sprintf("%s:%d", ref.MediaType, ref.MediaID)] = true
	}
	canExcludeMovies, canExcludeSeries := false, false
	targets := []model.Media{}
	for _, mediaItem := range mediaItems {
		if !selectedMedia[fmt.Sprintf("%s:%d", mediaItem.Type, mediaItem.SourceID)] {
			continue
		}
		switch mediaItem.Type {
		case model.Movie:
			if mediaItem.TMDBID <= 0 {
				continue
			}
			canExcludeMovies = true
		case model.Series:
			if mediaItem.TVDBID <= 0 {
				continue
			}
			canExcludeSeries = true
		default:
			continue
		}
		targets = append(targets, mediaItem)
	}
	return canExcludeMovies, canExcludeSeries, targets
}

func (server *Server) buildMediaRemovalPlan(kind model.MediaType, id int, selectionExplicit bool, selectedManaged map[string]bool, selectedTorrents map[string]bool, selectedUnmanaged map[string]bool) (removalData, error) {
	items, _, _ := server.inv.Snapshot()
	mr, ok := mediaRefFor(items, kind, id)
	if !ok {
		return removalData{}, fmt.Errorf("media not found")
	}
	refs, _, ferr := server.inv.ManagedFileRefs(kind, id)
	if ferr != nil {
		return removalData{}, ferr
	}
	if !selectionExplicit {
		for _, r := range refs {
			selectedManaged[managedFileKey(r)] = true
		}
	}
	authorizedManaged := map[string]bool{}
	for _, ref := range refs {
		authorizedManaged[managedFileKey(ref)] = true
	}
	for key := range selectedManaged {
		if !authorizedManaged[key] {
			return removalData{}, fmt.Errorf("managed file does not belong to this media: %s", key)
		}
	}
	files, allMediaRefs, allTorrentRefs, _, _ := server.inv.FileSnapshot()
	byPath := filesByPath(files)
	cm := map[string]removal.CandidateFile{}
	for _, r := range refs {
		if _, ok := byPath[filepath.Clean(r.Path)]; !ok {
			continue
		}
		sel := selectedManaged[managedFileKey(r)]
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: r.Path, Owner: removal.MediaOwner, OwnerKey: managedFileKey(r), Label: mr.Title, Selected: sel, Selectable: true})
	}
	physicalTorrentHashes := physicallyBackingTorrentHashes(files, refs, allTorrentRefs)
	var mediaItem *model.Media
	for itemIndex := range items {
		if items[itemIndex].Type == kind && items[itemIndex].SourceID == id {
			mediaItem = &items[itemIndex]
			break
		}
	}
	torrentByHash := map[string]model.Torrent{}
	for _, torrent := range server.inv.TorrentSnapshot() {
		torrentByHash[strings.ToLower(torrent.Hash)] = torrent
	}
	contextByHash := map[string]relatedRemovalTorrent{}
	if mediaItem != nil {
		for _, torrent := range mediaItem.Torrents {
			status := model.NormalizeTorrentStatus(torrent.AssociationStatus)
			if status != model.TorrentCurrent && status != model.TorrentSuperseded {
				continue
			}
			hash := strings.ToLower(torrent.Hash)
			contextByHash[hash] = relatedRemovalTorrent{Torrent: torrent, Selectable: status == model.TorrentCurrent, PhysicallyBacks: torrent.MediaHardlinked}
		}
	}
	for hash := range physicalTorrentHashes {
		torrent, ok := torrentByHash[hash]
		if !ok {
			continue
		}
		torrent.AssociationStatus = model.TorrentCurrent
		torrent.AssociationReason = "Current filesystem topology proves this torrent physically backs managed media."
		context := contextByHash[hash]
		context.Torrent = torrent
		context.Selectable = true
		context.PhysicallyBacks = true
		contextByHash[hash] = context
	}
	authorizedTorrents := map[string]bool{}
	for hash, context := range contextByHash {
		if context.Selectable {
			authorizedTorrents[hash] = true
		}
	}
	for hash := range selectedTorrents {
		if !authorizedTorrents[hash] {
			return removalData{}, fmt.Errorf("torrent is not current for this media: %s", hash)
		}
	}
	if !selectionExplicit {
		for hash := range physicalTorrentHashes {
			selectedTorrents[hash] = true
		}
	}
	for hash, context := range contextByHash {
		torrentFiles, _, _ := server.inv.TorrentFiles(hash)
		context.FileCount = len(torrentFiles)
		context.Selected = selectedTorrents[hash]
		contextByHash[hash] = context
		if !context.Selectable {
			continue
		}
		torrent := context.Torrent
		selected := context.Selected
		for _, file := range torrentFiles {
			mergeRemovalCandidate(cm, removal.CandidateFile{Path: file.Path, Owner: removal.TorrentOwner, OwnerKey: hash, Label: torrent.Name, Selected: selected, Selectable: true})
		}
	}
	cm = physicalCandidates(files, allMediaRefs, allTorrentRefs, cm, selectedManaged, selectedTorrents, selectedUnmanaged)
	related := []relatedRemovalTorrent{}
	contextTorrents := []relatedRemovalTorrent{}
	for _, context := range contextByHash {
		contextTorrents = append(contextTorrents, context)
		if context.Selectable {
			related = append(related, context)
		}
	}
	relatedUnmanaged := relatedUnmanagedFromCandidates(cm, byPath)
	selectedUF := selectedUnmanagedPaths(relatedUnmanaged)
	if len(selectedUF) > 0 {
		if err := server.inv.VerifyUnmanaged(selectedUF); err != nil {
			return removalData{}, err
		}
	}
	if len(refs) == 0 && len(related) == 0 {
		return removalData{}, fmt.Errorf("nothing to remove")
	}
	sort.Slice(related, func(i, j int) bool {
		return strings.ToLower(related[i].Torrent.Name) < strings.ToLower(related[j].Torrent.Name)
	})
	sort.Slice(contextTorrents, func(i, j int) bool {
		leftStatus := model.NormalizeTorrentStatus(contextTorrents[i].Torrent.AssociationStatus)
		rightStatus := model.NormalizeTorrentStatus(contextTorrents[j].Torrent.AssociationStatus)
		if leftStatus != rightStatus {
			return leftStatus < rightStatus
		}
		return strings.ToLower(contextTorrents[i].Torrent.Name) < strings.ToLower(contextTorrents[j].Torrent.Name)
	})
	key := fmt.Sprintf("%s:%d", kind, id)
	candidates := candidateSlice(cm)
	p := removal.Build(removal.MediaObject, key, mr.Title, server.inv.Config().Removal.DryRun, candidates)
	all := append([]removal.CandidateFile(nil), candidates...)
	for i := range all {
		all[i].Selected = true
	}
	potential := removal.Build(removal.MediaObject, key, mr.Title, server.inv.Config().Removal.DryRun, all).SelectedPathBytes
	groups := groupManagedFiles(refs, byPath, selectedManaged)
	allSel, some := len(refs) > 0, false
	for _, r := range refs {
		if selectedManaged[managedFileKey(r)] {
			some = true
		} else {
			allSel = false
		}
	}
	selectedRefs := selectedManagedRefs(refs, selectedManaged)
	actions := len(selectedRefs) + len(selectedUF)
	selectedTorrentCount := 0
	for _, t := range related {
		if t.Selected {
			actions++
			selectedTorrentCount++
		}
	}
	moviesOpt, episodesOpt := ownerOptions(selectedRefs)
	canExcludeMovies, canExcludeSeries, exclusionMedia := exclusionOptions(selectedRefs, items)
	selectedFileCount, selectedLogicalBytes := selectedFileSummary(p)
	guidanceTitle, guidanceAction := storageGuidance(p, kind, related)
	return removalData{
		Plan: p, FileGroups: groupRemovalFiles(p, files), SelectedActions: actions,
		PotentialBytes: potential, Related: related, RelatedGroups: groupRelatedTorrents(contextTorrents),
		RelatedTorrentCount: len(contextTorrents), SelectedTorrentCount: selectedTorrentCount,
		PreservedTorrentCount: len(contextTorrents) - selectedTorrentCount,
		RelatedUnmanaged:      relatedUnmanaged, SelectedUnmanaged: selectedUF, ManagedGroups: groups,
		ManagedFileCount: len(refs), ManagedAllSelected: allSel, ManagedSomeSelected: some,
		SelectedManaged: selectedRefs, CanUnmonitorMovies: moviesOpt, CanUnmonitorEpisodes: episodesOpt,
		CanExcludeMovies: canExcludeMovies, CanExcludeSeries: canExcludeSeries, ExclusionMedia: exclusionMedia,
		ManagedSectionLabel: managedSectionLabel(kind), SelectedFileCount: selectedFileCount,
		SelectedLogicalBytes: selectedLogicalBytes, StorageGuidanceTitle: guidanceTitle,
		StorageGuidanceAction: guidanceAction, BackURL: fmt.Sprintf("/library/%s/%d", kind, id),
		MediaType: string(kind), MediaID: id,
	}, nil
}

func (server *Server) buildTorrentRemovalPlan(hash string, targetSelected bool, selectedManaged map[string]bool, selectedUnmanaged map[string]bool) (removalData, error) {
	h := strings.ToLower(strings.TrimSpace(hash))
	var found *model.Torrent
	for _, t := range server.inv.TorrentSnapshot() {
		if strings.EqualFold(t.Hash, h) {
			x := t
			found = &x
			break
		}
	}
	if found == nil {
		return removalData{}, fmt.Errorf("torrent not found")
	}
	files, mrefs, trefs, _, ferr := server.inv.FileSnapshot()
	if ferr != nil {
		return removalData{}, ferr
	}
	byPath := filesByPath(files)
	proven := provenManagedRefsForTorrent(files, mrefs, trefs, h)
	cm := map[string]removal.CandidateFile{}
	tf, _, _ := server.inv.TorrentFiles(h)
	for _, f := range tf {
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: f.Path, Owner: removal.TorrentOwner, OwnerKey: h, Label: found.Name, Selected: targetSelected, Selectable: true})
	}
	for _, r := range proven {
		sel := selectedManaged[managedFileKey(r)]
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: r.Path, Owner: removal.MediaOwner, OwnerKey: managedFileKey(r), Selected: sel})
	}
	selectedTorrents := map[string]bool{h: targetSelected}
	cm = physicalCandidates(files, mrefs, trefs, cm, selectedManaged, selectedTorrents, selectedUnmanaged)
	items, _, _ := server.inv.Snapshot()
	mediaMap := map[string]model.MediaRef{}
	for _, m := range items {
		mediaMap[fmt.Sprintf("%s:%d", m.Type, m.SourceID)] = model.MediaRef{Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}
	}
	byMedia := map[string][]model.MediaFileRef{}
	for _, r := range proven {
		byMedia[fmt.Sprintf("%s:%d", r.MediaType, r.MediaID)] = append(byMedia[fmt.Sprintf("%s:%d", r.MediaType, r.MediaID)], r)
	}
	related := []relatedManagedMedia{}
	for key, refs := range byMedia {
		mr, ok := mediaMap[key]
		if !ok {
			continue
		}
		groups := groupManagedFiles(refs, byPath, selectedManaged)
		fullCounts := map[string]int{}
		for _, fr := range mrefs {
			if fr.MediaType != mr.Type || fr.MediaID != mr.SourceID {
				continue
			}
			g := "Files"
			if len(fr.Parts) > 0 && fr.Parts[0].Group != "" {
				g = fr.Parts[0].Group
			}
			fullCounts[g]++
		}
		for gi := range groups {
			groups[gi].TotalFiles = fullCounts[groups[gi].Label]
			if groups[gi].TotalFiles == 0 {
				groups[gi].TotalFiles = len(groups[gi].Files)
			}
			groups[gi].Complete = len(groups[gi].Files) == groups[gi].TotalFiles
		}
		all, some := len(refs) > 0, false
		for _, r := range refs {
			if selectedManaged[managedFileKey(r)] {
				some = true
			} else {
				all = false
			}
		}
		related = append(related, relatedManagedMedia{Media: mr, Groups: groups, FileCount: len(refs), AllSelected: all, SomeSelected: some})
	}
	sort.Slice(related, func(i, j int) bool {
		return strings.ToLower(related[i].Media.Title) < strings.ToLower(related[j].Media.Title)
	})
	candidates := candidateSlice(cm)
	p := removal.Build(removal.TorrentObject, h, found.Name, server.inv.Config().Removal.DryRun, candidates)
	all := append([]removal.CandidateFile(nil), candidates...)
	for i := range all {
		all[i].Selected = true
	}
	potential := removal.Build(removal.TorrentObject, h, found.Name, server.inv.Config().Removal.DryRun, all).SelectedPathBytes
	selectedRefs := selectedManagedRefs(proven, selectedManaged)
	relatedUnmanaged := relatedUnmanagedFromCandidates(cm, byPath)
	selectedUF := selectedUnmanagedPaths(relatedUnmanaged)
	if len(selectedUF) > 0 {
		if err := server.inv.VerifyUnmanaged(selectedUF); err != nil {
			return removalData{}, err
		}
	}
	actions := len(selectedRefs) + len(selectedUF)
	if targetSelected {
		actions++
	}
	moviesOpt, episodesOpt := ownerOptions(selectedRefs)
	canExcludeMovies, canExcludeSeries, exclusionMedia := exclusionOptions(selectedRefs, items)
	selectedFileCount, selectedLogicalBytes := selectedFileSummary(p)
	return removalData{
		Plan: p, FileGroups: groupRemovalFiles(p, files), TorrentTarget: true,
		TorrentSelected: targetSelected, SelectedActions: actions, PotentialBytes: potential,
		RelatedManaged: related, RelatedUnmanaged: relatedUnmanaged, SelectedUnmanaged: selectedUF,
		SelectedManaged: selectedRefs, CanUnmonitorMovies: moviesOpt, CanUnmonitorEpisodes: episodesOpt,
		CanExcludeMovies: canExcludeMovies, CanExcludeSeries: canExcludeSeries, ExclusionMedia: exclusionMedia,
		SelectedFileCount: selectedFileCount, SelectedLogicalBytes: selectedLogicalBytes,
		BackURL: "/torrents/" + url.PathEscape(h), Hash: h,
	}, nil
}

func (server *Server) buildUnmanagedRemovalPlan(paths []string, selectedTorrents map[string]bool) (removalData, error) {
	files, mrefs, trefs, _, ferr := server.inv.FileSnapshot()
	if ferr != nil {
		return removalData{}, ferr
	}
	current, _, err := server.inv.UnmanagedSnapshot()
	if err != nil {
		return removalData{}, err
	}
	known := map[string]bool{}
	for _, f := range current {
		known[filepath.Clean(f.Path)] = true
	}
	cm := map[string]removal.CandidateFile{}
	clean := make([]string, 0, len(paths))
	selectedUF := map[string]bool{}
	for _, p := range paths {
		p = filepath.Clean(p)
		if !known[p] {
			return removalData{}, fmt.Errorf("file is no longer unmanaged: %s", p)
		}
		clean = append(clean, p)
		selectedUF[p] = true
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: p, Owner: removal.UnmanagedOwner, OwnerKey: p, Label: p, Selected: true})
	}
	if len(clean) == 0 {
		return removalData{}, fmt.Errorf("no unmanaged files selected")
	}
	if err := server.inv.VerifyUnmanaged(clean); err != nil {
		return removalData{}, err
	}
	// Physical identity is authoritative for discovering sibling paths. This is
	// what lets an Unmanaged hardlink expose the torrent that owns another path.
	cm = physicalCandidates(files, mrefs, trefs, cm, map[string]bool{}, selectedTorrents, selectedUF)
	// A torrent removal is an owner-level action over the whole torrent. Once a
	// physically related torrent is identified, include all of its paths so both
	// selected and potential space calculations describe the actual action.
	relatedHashes := map[string]bool{}
	for _, c := range cm {
		if c.Owner == removal.TorrentOwner {
			relatedHashes[strings.ToLower(c.OwnerKey)] = true
		}
	}
	for h := range relatedHashes {
		tf, _, _ := server.inv.TorrentFiles(h)
		for _, r := range tf {
			mergeRemovalCandidate(cm, removal.CandidateFile{Path: r.Path, Owner: removal.TorrentOwner, OwnerKey: h, Label: h, Selected: selectedTorrents[h]})
		}
	}
	cm = physicalCandidates(files, mrefs, trefs, cm, map[string]bool{}, selectedTorrents, selectedUF)
	torrents := relatedTorrentList(cm, server.inv.TorrentSnapshot(), "")
	p := removal.Build(removal.UnmanagedObject, "unmanaged", fmt.Sprintf("%d unmanaged file(s)", len(clean)), server.inv.Config().Removal.DryRun, candidateSlice(cm))
	all := candidateSlice(cm)
	for i := range all {
		all[i].Selected = true
	}
	potential := removal.Build(removal.UnmanagedObject, "unmanaged", p.RequestedLabel, server.inv.Config().Removal.DryRun, all).SelectedPathBytes
	actions := len(clean)
	for _, t := range torrents {
		if t.Selected {
			actions++
		}
	}
	return removalData{
		Plan: p, FileGroups: groupRemovalFiles(p, files), PotentialBytes: potential,
		BackURL: "/downloads/unmanaged", UnmanagedPaths: clean, Related: torrents,
		RelatedGroups: groupRelatedTorrents(torrents), SelectedActions: actions,
	}, nil
}

func mapFromValues(values []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" {
			m[v] = true
		}
	}
	return m
}

func selectedTorrentSet(r *http.Request) map[string]bool {
	m := map[string]bool{}
	for _, h := range r.URL.Query()["torrent"] {
		m[strings.ToLower(strings.TrimSpace(h))] = true
	}
	return m
}

func selectedManagedSet(values []string) map[string]bool {
	m := map[string]bool{}
	for _, key := range values {
		key = strings.ToLower(strings.TrimSpace(key))
		if key != "" {
			m[key] = true
		}
	}
	return m
}

func targetSelection(r *http.Request) bool {
	if r.URL.Query().Get("selection") == "1" {
		return r.URL.Query().Get("target") == "1"
	}
	return true
}

func (server *Server) removalMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	kind := model.MediaType(strings.TrimSpace(r.URL.Query().Get("type")))
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	d, err := server.buildMediaRemovalPlan(kind, id, r.URL.Query().Get("selection") == "1", selectedManagedSet(r.URL.Query()["managed_file"]), selectedTorrentSet(r), selectedUnmanagedSet(r.URL.Query()["unmanaged_path"]))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	server.renderRemoval(w, d)
}
func (server *Server) removalTorrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	d, err := server.buildTorrentRemovalPlan(r.URL.Query().Get("hash"), targetSelection(r), selectedManagedSet(r.URL.Query()["managed_file"]), selectedUnmanagedSet(r.URL.Query()["unmanaged_path"]))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	server.renderRemoval(w, d)
}
func (server *Server) removalUnmanaged(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Direct filesystem removal is disabled: these files are Unmanaged, not Connarr-owned.", http.StatusForbidden)
}

func (server *Server) executeRemoval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if server.tasks == nil {
		http.Error(w, "durable scheduler unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := parseRemovalForm(w, r); err != nil {
		log.Printf("[removal] [operation=0] rejected reason=%q", "invalid removal request: "+err.Error())
		http.Error(w, "Invalid removal request: "+err.Error(), http.StatusBadRequest)
		return
	}
	descriptor, err := server.admitRemoval(r.Form)
	if err != nil {
		auditRemovalRejected(0, r.Form, err)
		http.Error(w, "Removal request is no longer valid: "+err.Error(), http.StatusConflict)
		return
	}
	database := server.inv.Store()
	if database == nil {
		auditRemovalRejected(0, r.Form, fmt.Errorf("durable operation history unavailable"))
		http.Error(w, "Removal cannot start without durable operation history", http.StatusServiceUnavailable)
		return
	}
	server.admissionMu.Lock()
	defer server.admissionMu.Unlock()
	operationToken := strings.TrimSpace(r.Form.Get("operation_token"))
	if operationToken != "" {
		if existing, found := existingRemovalByToken(database, operationToken); found {
			server.writeRemovalAccepted(w, existing.ID, existing.DryRun)
			return
		}
	}
	command := scheduledRemovalCommand{Form: cloneForm(r.Form)}
	queuedPayload, _ := json.Marshal(queuedRemovalPayload{Command: command})
	historyID, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "queued", DryRun: descriptor.DryRun, RequestedKind: string(descriptor.Kind), RequestedKey: descriptor.Key, RequestedLabel: descriptor.Label, Payload: queuedPayload})
	if err != nil {
		auditRemovalRejected(0, r.Form, fmt.Errorf("record operation before scheduling: %w", err))
		http.Error(w, "Removal could not be recorded before scheduling: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	command.HistoryID = historyID
	payload, _ := json.Marshal(command)
	_, err = server.tasks.Submit(tasks.Request{TaskID: removalTaskID, Kind: tasks.TriggerEvent, Priority: tasks.PriorityMutation, Durable: true, CoalescingKey: fmt.Sprintf("operation:%d", historyID), Cause: descriptor.Label, Payload: payload})
	if err != nil {
		auditRemovalRejected(historyID, r.Form, fmt.Errorf("scheduler rejected removal: %w", err))
		_ = database.UpdateHistoryEvent(store.HistoryEvent{ID: historyID, EventType: "removal", Status: "failed", DryRun: descriptor.DryRun, RequestedKind: string(descriptor.Kind), RequestedKey: descriptor.Key, RequestedLabel: descriptor.Label, Payload: queuedPayload, Error: "scheduler rejected removal: " + err.Error()})
		server.publishUIChange("removal-failed")
		http.Error(w, "Removal could not be scheduled: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	server.publishUIChange("mutation-accepted")
	server.writeRemovalAccepted(w, historyID, descriptor.DryRun)
}

type removalAdmission struct {
	Kind   removal.ObjectKind
	Key    string
	Label  string
	DryRun bool
}

// admitRemoval validates only identifiers against Connarr's published state.
// Filesystem topology and owner state are intentionally revalidated inside the
// scheduler's exclusive execution boundary, not on the browser request.
func (server *Server) admitRemoval(form url.Values) (removalAdmission, error) {
	if server.inv == nil {
		return removalAdmission{}, fmt.Errorf("inventory unavailable")
	}
	dryRun := server.inv.Config().Removal.DryRun
	if err := validateRemovalScope(form); err != nil {
		return removalAdmission{}, err
	}
	switch form.Get("kind") {
	case "media":
		kind := model.MediaType(form.Get("media_type"))
		id, _ := strconv.Atoi(form.Get("media_id"))
		if (kind != model.Movie && kind != model.Series) || id <= 0 {
			return removalAdmission{}, fmt.Errorf("invalid media identity")
		}
		if len(form["managed_file"]) == 0 && len(form["torrent"]) == 0 {
			return removalAdmission{}, fmt.Errorf("nothing selected")
		}
		knownTorrents := map[string]bool{}
		for _, torrent := range server.inv.TorrentSnapshot() {
			knownTorrents[strings.ToLower(torrent.Hash)] = true
		}
		for hash := range mapFromValues(form["torrent"]) {
			if !knownTorrents[hash] {
				return removalAdmission{}, fmt.Errorf("torrent not found: %s", hash)
			}
		}
		items, _, _ := server.inv.Snapshot()
		for _, item := range items {
			if item.Type == kind && item.SourceID == id {
				return removalAdmission{Kind: removal.MediaObject, Key: fmt.Sprintf("%s:%d", kind, id), Label: item.Title, DryRun: dryRun}, nil
			}
		}
		return removalAdmission{}, fmt.Errorf("media not found")
	case "torrent":
		hash := strings.ToLower(strings.TrimSpace(form.Get("hash")))
		if hash == "" || form.Get("target") != "1" {
			return removalAdmission{}, fmt.Errorf("nothing selected")
		}
		for _, torrent := range server.inv.TorrentSnapshot() {
			if strings.EqualFold(torrent.Hash, hash) {
				return removalAdmission{Kind: removal.TorrentObject, Key: hash, Label: torrent.Name, DryRun: dryRun}, nil
			}
		}
		return removalAdmission{}, fmt.Errorf("torrent not found")
	case "unmanaged":
		paths := form["path"]
		if len(paths) == 0 {
			return removalAdmission{}, fmt.Errorf("no unmanaged files selected")
		}
		known := map[string]bool{}
		files, _, _ := server.inv.UnmanagedSnapshot()
		for _, file := range files {
			known[filepath.Clean(file.Path)] = true
		}
		for _, path := range paths {
			if !known[filepath.Clean(path)] {
				return removalAdmission{}, fmt.Errorf("file is no longer unmanaged: %s", path)
			}
		}
		return removalAdmission{Kind: removal.UnmanagedObject, Key: "unmanaged", Label: fmt.Sprintf("%d unmanaged file(s)", len(paths)), DryRun: dryRun}, nil
	default:
		return removalAdmission{}, fmt.Errorf("unknown removal kind")
	}
}

func existingRemovalByToken(database *store.Store, token string) (store.HistoryEvent, bool) {
	events, err := database.HistoryEvents(200)
	if err != nil {
		return store.HistoryEvent{}, false
	}
	for _, event := range events {
		var payload queuedRemovalPayload
		if json.Unmarshal(event.Payload, &payload) == nil && payload.Command.Form.Get("operation_token") == token {
			return event, true
		}
	}
	return store.HistoryEvent{}, false
}

func (server *Server) writeRemovalAccepted(response http.ResponseWriter, historyID int64, dryRun bool) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Location", fmt.Sprintf("/history#operation-%d", historyID))
	response.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(response).Encode(map[string]any{"operationId": historyID, "status": "queued", "dryRun": dryRun})
}

const maximumRemovalFormBytes = 2 << 20

func parseRemovalForm(response http.ResponseWriter, request *http.Request) error {
	request.Body = http.MaxBytesReader(response, request.Body, maximumRemovalFormBytes)
	if err := request.ParseMultipartForm(maximumRemovalFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return err
	}
	return nil
}

func cloneForm(source url.Values) url.Values {
	cloned := make(url.Values, len(source))
	for key, values := range source {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func (server *Server) executeRemovalNowContext(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	kind := r.FormValue("kind")
	if err := validateRemovalScope(r.Form); err != nil {
		http.Error(w, "Removal scope rejected: "+err.Error(), http.StatusConflict)
		return
	}
	var d removalData
	var err error
	switch kind {
	case "media":
		mt := model.MediaType(r.FormValue("media_type"))
		id, _ := strconv.Atoi(r.FormValue("media_id"))
		d, err = server.buildMediaRemovalPlan(mt, id, true, selectedManagedSet(r.Form["managed_file"]), mapFromValues(r.Form["torrent"]), map[string]bool{})
	case "torrent":
		d, err = server.buildTorrentRemovalPlan(r.FormValue("hash"), true, map[string]bool{}, map[string]bool{})
	case "unmanaged":
		d, err = server.buildUnmanagedRemovalPlan(r.Form["path"], map[string]bool{})
	default:
		err = fmt.Errorf("unknown removal kind")
	}
	if err != nil {
		http.Error(w, "Removal revalidation failed: "+err.Error(), 409)
		return
	}
	if d.SelectedActions == 0 {
		http.Error(w, "nothing selected", 400)
		return
	}
	results := []string{}
	errs := []string{}
	removedManaged := []model.MediaFileRef{}
	dry := d.Plan.DryRun
	historyID, _ := strconv.ParseInt(r.FormValue("_history_id"), 10, 64)
	startedPayload, _ := json.Marshal(map[string]any{
		"command": scheduledRemovalCommand{HistoryID: historyID, Form: cloneForm(r.Form)},
		"plan":    d.Plan, "managedFiles": d.SelectedManaged,
		"unmonitorMovies":   r.FormValue("unmonitor_movies") == "1",
		"unmonitorEpisodes": r.FormValue("unmonitor_episodes") == "1",
		"excludeMovies":     r.FormValue("exclude_movies") == "1",
		"excludeSeries":     r.FormValue("exclude_series") == "1",
	})
	db := server.inv.Store()
	if db == nil {
		http.Error(w, "Removal cannot start without durable operation history", http.StatusServiceUnavailable)
		return
	}
	var historyErr error
	if historyID > 0 {
		historyErr = db.UpdateHistoryEvent(store.HistoryEvent{ID: historyID, EventType: "removal", Status: "started", DryRun: dry, RequestedKind: string(d.Plan.Kind), RequestedKey: d.Plan.RequestedKey, RequestedLabel: d.Plan.RequestedLabel, ReclaimableBytes: d.Plan.ReclaimableBytes, Payload: startedPayload})
	} else {
		historyID, historyErr = db.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "started", DryRun: dry, RequestedKind: string(d.Plan.Kind), RequestedKey: d.Plan.RequestedKey, RequestedLabel: d.Plan.RequestedLabel, ReclaimableBytes: d.Plan.ReclaimableBytes, Payload: startedPayload})
	}
	if historyErr != nil {
		http.Error(w, "Removal could not be recorded before execution: "+historyErr.Error(), http.StatusServiceUnavailable)
		return
	}
	auditRemovalPlan(historyID, d, r.Form)
	recordResult := func(result string) {
		results = append(results, result)
		auditRemovalResult(historyID, result)
	}
	recordError := func(operationError string) {
		errs = append(errs, operationError)
		auditRemovalError(historyID, operationError)
	}
	mutated, mutationUncertain := false, false
	if !dry {
		switch kind {
		case "media":
			ownerFailed := false
			for _, ref := range d.SelectedManaged {
				if e := server.inv.RemoveManagedFile(ctx, ref); e != nil {
					recordError(fmt.Sprintf("managed file %s:%d: %v", ref.Source, ref.SourceFileID, e))
					ownerFailed = true
					mutationUncertain = true
				} else {
					mutated = true
					recordResult("managed file removed: " + ref.Path)
					removedManaged = append(removedManaged, ref)
				}
			}
			for _, rt := range d.Related {
				if !rt.Selected {
					continue
				}
				if e := server.inv.RemoveTorrent(ctx, rt.Torrent.Hash); e != nil {
					recordError("torrent " + rt.Torrent.Name + ": " + e.Error())
					ownerFailed = true
					mutationUncertain = true
				} else {
					mutated = true
					recordResult("torrent removed: " + rt.Torrent.Name)
				}
			}
			if !ownerFailed {
				rr, ee := server.unlinkVerified(ctx, selectedUnmanagedStates(d.Plan, d.SelectedUnmanaged))
				for _, result := range rr {
					recordResult(result)
				}
				for _, operationError := range ee {
					recordError(operationError)
				}
				mutated = mutated || len(rr) > 0
			}
		case "torrent":
			if d.TorrentSelected {
				if e := server.inv.RemoveTorrent(ctx, d.Hash); e != nil {
					recordError(e.Error())
					mutationUncertain = true
				} else {
					mutated = true
					recordResult("torrent removed by qBittorrent")
				}
			}
		case "unmanaged":
			// Owner-backed actions run before direct filesystem unlinking. If an
			// owner action fails, the unmanaged sibling is still preserved rather
			// than being removed first and leaving a surprising partial result.
			ownerFailed := false
			for _, rt := range d.Related {
				if !rt.Selected {
					continue
				}
				if e := server.inv.RemoveTorrent(ctx, rt.Torrent.Hash); e != nil {
					recordError("torrent " + rt.Torrent.Name + ": " + e.Error())
					ownerFailed = true
					mutationUncertain = true
				} else {
					mutated = true
					recordResult("torrent removed: " + rt.Torrent.Name)
				}
			}
			if !ownerFailed {
				rr, ee := server.unlinkVerified(ctx, selectedUnmanagedStates(d.Plan, d.UnmanagedPaths))
				for _, result := range rr {
					recordResult(result)
				}
				for _, operationError := range ee {
					recordError(operationError)
				}
				mutated = mutated || len(rr) > 0
			}
		}
		if r.FormValue("unmonitor_movies") == "1" {
			seen := map[int]bool{}
			for _, ref := range removedManaged {
				if strings.EqualFold(ref.Source, "radarr") && !seen[ref.MediaID] {
					seen[ref.MediaID] = true
					if e := server.inv.SetMovieMonitored(ctx, ref.MediaID, false); e != nil {
						recordError(fmt.Sprintf("unmonitor movie %d: %v", ref.MediaID, e))
						mutationUncertain = true
					} else {
						mutated = true
						recordResult(fmt.Sprintf("movie unmonitored: %d", ref.MediaID))
					}
				}
			}
		}
		if r.FormValue("unmonitor_episodes") == "1" {
			seen := map[int]bool{}
			ids := []int{}
			for _, ref := range removedManaged {
				if !strings.EqualFold(ref.Source, "sonarr") {
					continue
				}
				for _, part := range ref.Parts {
					if part.SourcePartID > 0 && !seen[part.SourcePartID] {
						seen[part.SourcePartID] = true
						ids = append(ids, part.SourcePartID)
					}
				}
			}
			if len(ids) > 0 {
				sort.Ints(ids)
				if e := server.inv.SetEpisodesMonitored(ctx, ids, false); e != nil {
					recordError("unmonitor episodes: " + e.Error())
					mutationUncertain = true
				} else {
					mutated = true
					recordResult(fmt.Sprintf("%d episode(s) unmonitored", len(ids)))
				}
			}
		}
		removedMedia := map[string]bool{}
		for _, ref := range removedManaged {
			removedMedia[fmt.Sprintf("%s:%d", ref.MediaType, ref.MediaID)] = true
		}
		for _, mediaItem := range d.ExclusionMedia {
			if !removedMedia[fmt.Sprintf("%s:%d", mediaItem.Type, mediaItem.SourceID)] {
				continue
			}
			switch mediaItem.Type {
			case model.Movie:
				if r.FormValue("exclude_movies") != "1" {
					continue
				}
				if e := server.inv.AddMovieImportListExclusion(ctx, mediaItem); e != nil {
					recordError(fmt.Sprintf("add movie %q to import-list exclusions: %v", mediaItem.Title, e))
					mutationUncertain = true
				} else {
					mutated = true
					recordResult("movie added to import-list exclusions: " + mediaItem.Title)
				}
			case model.Series:
				if r.FormValue("exclude_series") != "1" {
					continue
				}
				if e := server.inv.AddSeriesImportListExclusion(ctx, mediaItem); e != nil {
					recordError(fmt.Sprintf("add series %q to import-list exclusions: %v", mediaItem.Title, e))
					mutationUncertain = true
				} else {
					mutated = true
					recordResult("series added to import-list exclusions: " + mediaItem.Title)
				}
			}
		}
	}
	if !dry && server.tasks != nil && (mutated || mutationUncertain) {
		scope := inventory.ReconciliationScope{Full: mutationUncertain}
		if mutationUncertain {
			scope.Reasons = append(scope.Reasons, fmt.Sprintf("removal operation %d had an uncertain or partial mutation", historyID))
		}
		for _, file := range d.Plan.Files {
			scope.Paths = append(scope.Paths, file.Path)
		}
		for _, ref := range removedManaged {
			scope.Owners = append(scope.Owners, inventory.ReconciliationOwner{Type: ref.MediaType, ID: ref.MediaID})
		}
		if d.TorrentSelected && strings.TrimSpace(d.Hash) != "" {
			scope.Torrents = append(scope.Torrents, d.Hash)
		}
		for _, relatedTorrent := range d.Related {
			if relatedTorrent.Selected {
				scope.Torrents = append(scope.Torrents, relatedTorrent.Torrent.Hash)
			}
		}
		if scopeErr := server.inv.QueueReconciliation(scope); scopeErr != nil {
			mutationUncertain = true
			recordError("targeted reconciliation scope could not be persisted: " + scopeErr.Error())
		}
		if workflowErr := server.schedulePostRemovalConsistency(historyID); workflowErr != nil {
			log.Printf("[removal] [operation=%d] critical consistency workflow scheduling failure: %v", historyID, workflowErr)
			recordError("post-removal consistency could not be scheduled: " + workflowErr.Error())
		}
	}
	status := "dry_run"
	if !dry {
		if len(errs) == 0 {
			status = "success"
		} else if len(results) > 0 {
			status = "partial"
		} else {
			status = "failed"
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"command": scheduledRemovalCommand{HistoryID: historyID, Form: cloneForm(r.Form)},
		"plan":    d.Plan, "managedFiles": d.SelectedManaged,
		"unmonitorMovies":   r.FormValue("unmonitor_movies") == "1",
		"unmonitorEpisodes": r.FormValue("unmonitor_episodes") == "1",
		"excludeMovies":     r.FormValue("exclude_movies") == "1",
		"excludeSeries":     r.FormValue("exclude_series") == "1",
		"results":           results, "errors": errs,
	})
	if historyErr := db.UpdateHistoryEvent(store.HistoryEvent{ID: historyID, EventType: "removal", Status: status, DryRun: dry, RequestedKind: string(d.Plan.Kind), RequestedKey: d.Plan.RequestedKey, RequestedLabel: d.Plan.RequestedLabel, ReclaimableBytes: d.Plan.ReclaimableBytes, Payload: payload, Error: strings.Join(errs, "; ")}); historyErr != nil {
		log.Printf("[removal] [operation=%d] critical History finalization failure: %v", historyID, historyErr)
		recordError(fmt.Sprintf("removal operation %d completed but its durable result could not be finalized: %v", historyID, historyErr))
		if len(results) > 0 {
			status = "partial"
		} else {
			status = "failed"
		}
	}
	auditRemovalComplete(historyID, status, d, len(results), len(errs))
	if r.Header.Get("X-Connarr-Overlay") == "1" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "dryRun": dry, "results": results, "errors": errs})
		return
	}
	http.Redirect(w, r, "/history", http.StatusSeeOther)
}

func (server *Server) schedulePostRemovalConsistency(historyID int64) error {
	_, err := server.tasks.AdvanceWorkflow(
		"post-removal-consistency",
		"global",
		0,
		5*time.Minute,
		fmt.Sprintf("Removal operation %d", historyID),
	)
	return err
}
