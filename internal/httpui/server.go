package httpui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"togetharr/internal/cleanup"
	"togetharr/internal/inventory"
	"togetharr/internal/model"
	"togetharr/internal/removal"
	"togetharr/internal/storagecap"
	"togetharr/internal/store"
	"togetharr/internal/tasks"
)

type AppInfo struct {
	Name    string
	Version string
}

var appInfo = AppInfo{Name: "Togetharr", Version: "0.1.27"}

type Server struct {
	inv              *inventory.Service
	tasks            *tasks.Manager
	homeTpl          *template.Template
	libraryTpl       *template.Template
	historyTpl       *template.Template
	profileTpl       *template.Template
	torrentTpl       *template.Template
	torrentDetailTpl *template.Template
	unclaimedTpl     *template.Template
	tasksTpl         *template.Template
	removalTpl       *template.Template
}

func New(inv *inventory.Service, taskManager *tasks.Manager) (*Server, error) {
	f := template.FuncMap{"appName": func() string { return appInfo.Name }, "appVersion": func() string { return appInfo.Version }, "chrome": func(active string) template.HTML { return template.HTML(appChrome(active)) }, "human": func(n int64) string {
		if n < 0 {
			n = 0
		}
		return cleanup.Human(uint64(n))
	}, "humanU": func(n uint64) string { return cleanup.Human(n) }, "rate": func(n int64) string { return cleanup.Human(uint64(max64(n, 0))) + "/s" }, "duration": humanDuration, "durationGo": func(d time.Duration) string { return humanDuration(int64(d / time.Second)) }, "unixTime": unixTime, "pct": func(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }, "fmtTime": func(t *time.Time) string {
		if t == nil {
			return "Never"
		}
		return t.Local().Format("2006-01-02")
	}, "join": strings.Join, "add": func(a, b int) int { return a + b }, "managedKey": managedFileKey, "shortPath": shortPath, "fmtUpdated": func(t time.Time) string {
		if t.IsZero() {
			return "Never"
		}
		return t.Local().Format("2006-01-02 15:04:05")
	}}
	homeTpl, e := template.New("home.html").Funcs(f).Parse(homeHTML)
	if e != nil {
		return nil, e
	}
	libraryTpl, e := template.New("library.html").Funcs(f).Parse(libraryHTML)
	if e != nil {
		return nil, e
	}
	historyTpl, e := template.New("history.html").Funcs(f).Parse(historyHTML)
	if e != nil {
		return nil, e
	}
	profileTpl, e := template.New("media.html").Funcs(f).Parse(profileHTML)
	if e != nil {
		return nil, e
	}
	torrentTpl, e := template.New("torrents.html").Funcs(f).Parse(torrentHTML)
	if e != nil {
		return nil, e
	}
	torrentDetailTpl, e := template.New("torrent.html").Funcs(f).Parse(torrentDetailHTML)
	if e != nil {
		return nil, e
	}
	unclaimedTpl, e := template.New("unclaimed.html").Funcs(f).Parse(unclaimedHTML)
	if e != nil {
		return nil, e
	}
	tasksTpl, e := template.New("tasks.html").Funcs(f).Parse(tasksHTML)
	if e != nil {
		return nil, e
	}
	removalTpl, e := template.New("removal.html").Funcs(f).Parse(removalHTML)
	if e != nil {
		return nil, e
	}
	return &Server{inv: inv, tasks: taskManager, homeTpl: homeTpl, libraryTpl: libraryTpl, historyTpl: historyTpl, profileTpl: profileTpl, torrentTpl: torrentTpl, torrentDetailTpl: torrentDetailTpl, unclaimedTpl: unclaimedTpl, tasksTpl: tasksTpl, removalTpl: removalTpl}, nil
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", s.home)
	m.HandleFunc("/library", s.library)
	m.HandleFunc("/library/", s.media)
	m.HandleFunc("/media/", s.media)
	m.HandleFunc("/torrents", s.torrents)
	m.HandleFunc("/torrents/", s.torrentDetail)
	m.HandleFunc("/downloads/unclaimed", s.unclaimedDownloads)
	m.HandleFunc("/downloads/unclaimed/scan", s.scanUnclaimedNow)
	m.HandleFunc("/history", s.history)
	m.HandleFunc("/tasks", s.tasksPage)
	m.HandleFunc("/tasks/run", s.runTask)
	m.HandleFunc("/removal/media", s.removalMedia)
	m.HandleFunc("/removal/torrent", s.removalTorrent)
	m.HandleFunc("/removal/unclaimed", s.removalUnclaimed)
	m.HandleFunc("/removal/execute", s.executeRemoval)
	m.HandleFunc("/api/media", s.apiMedia)
	m.HandleFunc("/api/torrents", s.apiTorrents)
	m.HandleFunc("/api/unclaimed", s.apiUnclaimed)
	m.HandleFunc("/api/files", s.apiFiles)
	m.HandleFunc("/api/refresh", s.refresh)
	m.HandleFunc("/api/plan", s.plan)
	m.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	return m
}

type tasksData struct {
	Tasks []tasks.Status
}

func (s *Server) tasksPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tasks" {
		http.NotFound(w, r)
		return
	}
	if s.tasks == nil {
		http.Error(w, "task manager unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := renderTemplate(w, s.tasksTpl, tasksData{Tasks: s.tasks.Snapshot()}); err != nil {
		log.Printf("render tasks: %v", err)
	}
}

func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.tasks == nil {
		http.Error(w, "task manager unavailable", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}
	s.tasks.RunAsync(context.Background(), id)
	http.Redirect(w, r, "/tasks", http.StatusSeeOther)
}

type mediaRow struct {
	Media     model.Media
	Removable bool
}

type navLink struct {
	Value int
	URL   string
}

type libraryData struct {
	Rows            []mediaRow
	Updated         time.Time
	LastErr         error
	Plan            cleanup.Plan
	Refreshing      bool
	PlanErr         error
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

type homeData struct {
	Plan                 cleanup.Plan
	PlanErr              error
	Updated              time.Time
	LastErr              error
	Refreshing           bool
	TotalMedia           int
	Movies               int
	Series               int
	LibraryBytes         int64
	TotalTorrents        int
	Associated           int
	Superseded           int
	Orphaned             int
	Unassociated         int
	ObsoleteReclaimable  int64
	ObsoleteKnown        int
	UnclaimedFiles       int
	UnclaimedBytes       int64
	UnclaimedReclaimable int64
	UnclaimedAvailable   bool
	UnclaimedError       string
	Services             []inventory.ServiceStatus
	Capabilities         storagecap.Capabilities
	Stats                store.CleanupStats
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	items, updated, last := s.inv.Snapshot()
	p, planErr := cleanup.Build(s.inv.Config().Storage.Path, s.inv.Config().Storage.TargetUsagePercent, s.inv.Config().Storage.CriticalUsagePercent, items)
	d := homeData{Plan: p, PlanErr: planErr, Updated: updated, LastErr: last, Refreshing: s.inv.IsRefreshing(), TotalMedia: len(items), Capabilities: storagecap.Inspect(s.inv.Config().Storage.Path)}
	for _, svc := range s.inv.StatusSnapshot() {
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
	ts := s.inv.TorrentSnapshot()
	d.TotalTorrents = len(ts)
	for _, t := range ts {
		switch normalizeTorrentStatus(t.AssociationStatus) {
		case "ASSOCIATED":
			d.Associated++
		case "SUPERSEDED":
			d.Superseded++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += t.ReclaimableBytes
			}
		case "ORPHANED":
			d.Orphaned++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += t.ReclaimableBytes
			}
		default:
			d.Unassociated++
		}
	}
	ufs, unclaimedUpdated, ue := s.inv.UnclaimedSnapshot()
	applyUnclaimedSummary(&d, ufs, unclaimedUpdated, ue)
	if db := s.inv.Store(); db != nil {
		d.Stats, _ = db.CleanupStatistics()
	}
	if e := renderTemplate(w, s.homeTpl, d); e != nil {
		log.Printf("render home: %v", e)
	}
}

func applyUnclaimedSummary(d *homeData, files []model.UnclaimedFile, updated time.Time, scanErr error) {
	if scanErr != nil {
		d.UnclaimedError = scanErr.Error()
		return
	}
	if updated.IsZero() {
		// No successful scan has completed yet. This is a normal startup state,
		// not an error and not an empty authoritative result.
		return
	}
	d.UnclaimedAvailable = true
	d.UnclaimedFiles = len(files)
	for _, f := range files {
		d.UnclaimedBytes += f.SizeBytes
		d.UnclaimedReclaimable += f.ReclaimableBytes
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
		hasTorrent := len(m.Torrents) > 0
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

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/library" {
		http.NotFound(w, r)
		return
	}
	items, updated, last := s.inv.Snapshot()
	p, planErr := cleanup.Build(s.inv.Config().Storage.Path, s.inv.Config().Storage.TargetUsagePercent, s.inv.Config().Storage.CriticalUsagePercent, items)

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
	d := libraryData{Rows: rows, Updated: updated, LastErr: last, Plan: p, Refreshing: s.inv.IsRefreshing(), PlanErr: planErr, TotalItems: total, AllItems: allItems, Page: page, PageSize: pageSize, TotalPages: totalPages, HasPrev: page > 1, HasNext: page < totalPages, Sort: sortKey, Order: order, SortURLs: sortURLs, SizeLinks: sizeLinks, PageLinks: pageLinks, Query: r.URL.Query().Get("q"), TypeFilter: typeFilter, RequestedFilter: requestedFilter, WatchedFilter: watchedFilter, TorrentFilter: torrentFilter, ShowNoFiles: showNoFiles, ClearURL: "/library"}
	if d.HasPrev {
		d.PrevURL = mkURL(page-1, pageSize, sortKey, order)
	}
	if d.HasNext {
		d.NextURL = mkURL(page+1, pageSize, sortKey, order)
	}
	if e := renderTemplate(w, s.libraryTpl, d); e != nil {
		log.Printf("render library: %v", e)
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
func mediaKey(m model.Media) string { return fmt.Sprintf("%s:%d", m.Type, m.SourceID) }
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
			if a.Value < b.Value {
				cmp = -1
			} else if a.Value > b.Value {
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

func groupMediaTorrents(items []model.Torrent) (current, superseded, orphaned []model.Torrent) {
	for _, t := range items {
		switch normalizeTorrentStatus(t.AssociationStatus) {
		case "ASSOCIATED":
			current = append(current, t)
		case "SUPERSEDED":
			superseded = append(superseded, t)
		case "ORPHANED":
			orphaned = append(orphaned, t)
		}
	}
	byName := func(items []model.Torrent) {
		sort.SliceStable(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	}
	byName(current)
	byName(superseded)
	byName(orphaned)
	return
}

func (s *Server) media(w http.ResponseWriter, r *http.Request) {
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

	items, updated, last := s.inv.Snapshot()
	for i := range items {
		m := items[i]
		if string(m.Type) != parts[0] || m.SourceID != sourceID {
			continue
		}
		current, superseded, orphaned := groupMediaTorrents(m.Torrents)
		storageView, filesUpdated, filesErr := s.inv.MediaStorage(m.Type, m.SourceID)
		files := storageView.Files
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
			Orphaned          []model.Torrent
			Files             []inventory.FileView
			FileCount         int
			FilesUpdated      time.Time
			FilesErr          error
			RemoveMedia       inventory.RemovalEstimate
			RemoveWithCurrent inventory.RemovalEstimate
		}{m, updated, last, s.inv.IsRefreshing(), current, superseded, orphaned, files, fileCount, filesUpdated, filesErr, storageView.RemoveMedia, storageView.RemoveWithCurrent}
		if e := renderTemplate(w, s.profileTpl, data); e != nil {
			log.Printf("render media profile: %v", e)
		}
		return
	}
	http.NotFound(w, r)
}

type torrentData struct {
	Torrents                                       []model.Torrent
	Updated                                        time.Time
	LastErr                                        error
	Refreshing                                     bool
	Associated, Superseded, Orphaned, Unassociated int
	ObsoleteReclaimable                            int64
	ObsoleteKnown                                  int
	TotalItems                                     int
	Page                                           int
	PageSize                                       int
	TotalPages                                     int
	HasPrev, HasNext                               bool
	PrevURL, NextURL                               string
	PageLinks                                      []navLink
	SizeLinks                                      []navLink
	Sort, Order                                    string
	SortURLs                                       map[string]string
	AllItems                                       int
	Query                                          string
	StatusFilter                                   string
	ReclaimableFilter                              string
	ActivityFilter                                 string
	ClearURL                                       string
}

func normalizeTorrentStatus(v string) string {
	switch strings.ToUpper(v) {
	case "OPEN", "ASSOCIATED":
		return "ASSOCIATED"
	case "SUPERSEDED":
		return "SUPERSEDED"
	case "ORPHANED":
		return "ORPHANED"
	case "UNMATCHED", "UNASSOCIATED":
		return "UNASSOCIATED"
	default:
		return "UNASSOCIATED"
	}
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
			if a.Value < b.Value {
				cmp = -1
			} else if a.Value > b.Value {
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
	case "ASSOCIATED", "SUPERSEDED", "ORPHANED", "UNASSOCIATED":
		return strings.ToUpper(strings.TrimSpace(v))
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

func (s *Server) torrents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/torrents" {
		http.NotFound(w, r)
		return
	}
	_, updated, last := s.inv.Snapshot()
	all := s.inv.TorrentSnapshot()
	allItems := len(all)
	counts := map[string]int{"ASSOCIATED": 0, "SUPERSEDED": 0, "ORPHANED": 0, "UNASSOCIATED": 0}
	var obsoleteReclaimable int64
	obsoleteKnown := 0
	for i := range all {
		all[i].AssociationStatus = normalizeTorrentStatus(all[i].AssociationStatus)
		counts[all[i].AssociationStatus]++
		if (all[i].AssociationStatus == "SUPERSEDED" || all[i].AssociationStatus == "ORPHANED") && all[i].ReclaimableKnown {
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
	d := torrentData{Torrents: all[from:to], Updated: updated, LastErr: last, Refreshing: s.inv.IsRefreshing(), Associated: counts["ASSOCIATED"], Superseded: counts["SUPERSEDED"], Orphaned: counts["ORPHANED"], Unassociated: counts["UNASSOCIATED"], ObsoleteKnown: obsoleteKnown, ObsoleteReclaimable: obsoleteReclaimable, TotalItems: total, AllItems: allItems, Page: page, PageSize: pageSize, TotalPages: pages, HasPrev: page > 1, HasNext: page < pages, PageLinks: links, SizeLinks: sizes, Sort: sortKey, Order: order, SortURLs: sortURLs, Query: r.URL.Query().Get("q"), StatusFilter: statusFilter, ReclaimableFilter: reclaimableFilter, ActivityFilter: activityFilter, ClearURL: "/torrents"}
	if d.HasPrev {
		d.PrevURL = mk(page-1, pageSize, sortKey, order)
	}
	if d.HasNext {
		d.NextURL = mk(page+1, pageSize, sortKey, order)
	}
	if e := renderTemplate(w, s.torrentTpl, d); e != nil {
		log.Printf("render torrents: %v", e)
	}
}

type unclaimedFileGroup struct {
	Paths            []model.UnclaimedFile
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

type unclaimedData struct {
	Files                                     []unclaimedFileGroup
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

func groupUnclaimedFiles(items []model.UnclaimedFile) []unclaimedFileGroup {
	type groupKey struct {
		device uint64
		inode  uint64
		path   string
	}
	groups := map[groupKey]*unclaimedFileGroup{}
	order := make([]groupKey, 0, len(items))
	for _, f := range items {
		cp := filepath.Clean(f.Path)
		key := groupKey{path: cp}
		if f.Device != 0 || f.Inode != 0 {
			key = groupKey{device: f.Device, inode: f.Inode}
		}
		g := groups[key]
		if g == nil {
			g = &unclaimedFileGroup{SizeBytes: f.SizeBytes, ModifiedAt: f.ModifiedAt, Device: f.Device, Inode: f.Inode, Links: f.Links, ReclaimableKnown: f.ReclaimableKnown}
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
	out := make([]unclaimedFileGroup, 0, len(order))
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

func validUnclaimedSort(v string) bool {
	switch v {
	case "path", "size", "reclaimable", "links", "modified":
		return true
	}
	return false
}
func sortUnclaimed(items []unclaimedFileGroup, key, order string) {
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

func (s *Server) scanUnclaimedNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var err error
	if s.tasks != nil {
		err = s.inv.ScanUnclaimed(r.Context())
	} else {
		err = s.inv.ScanUnclaimed(r.Context())
	}
	if r.Header.Get("X-Togetharr-Scan") == "1" {
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
	http.Redirect(w, r, "/downloads/unclaimed", http.StatusSeeOther)
}

func (s *Server) unclaimedDownloads(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/downloads/unclaimed" {
		http.NotFound(w, r)
		return
	}
	raw, updated, scanErr := s.inv.UnclaimedSnapshot()
	all := groupUnclaimedFiles(raw)
	allItems := len(all)
	var totalBytes, reclaimableBytes, sharedBytes int64
	for _, f := range all {
		totalBytes += f.SizeBytes
		reclaimableBytes += f.ReclaimableBytes
		sharedBytes += f.SharedBytes
	}
	qtext := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if qtext != "" {
		filtered := make([]unclaimedFileGroup, 0, len(all))
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
	if !validUnclaimedSort(sortKey) {
		sortKey = "reclaimable"
	}
	order := strings.ToLower(r.URL.Query().Get("order"))
	if order != "asc" && order != "desc" {
		order = "desc"
	}
	sortUnclaimed(all, sortKey, order)
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
		return "/downloads/unclaimed?" + q.Encode()
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
	d := unclaimedData{Files: all[from:to], Updated: updated, ScanErr: scanErr, TotalItems: total, AllItems: allItems, TotalBytes: totalBytes, ReclaimableBytes: reclaimableBytes, SharedBytes: sharedBytes, Page: page, PageSize: pageSize, TotalPages: pages, HasPrev: page > 1, HasNext: page < pages, PageLinks: links, SizeLinks: sizes, Sort: sortKey, Order: order, Query: r.URL.Query().Get("q"), SortURLs: sortURLs}
	if d.HasPrev {
		d.PrevURL = mk(page-1, pageSize, sortKey, order)
	}
	if d.HasNext {
		d.NextURL = mk(page+1, pageSize, sortKey, order)
	}
	if e := renderTemplate(w, s.unclaimedTpl, d); e != nil {
		log.Printf("render unclaimed downloads: %v", e)
	}
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/history" {
		http.NotFound(w, r)
		return
	}
	var stats store.CleanupStats
	var runs []store.CleanupRun
	var events []store.HistoryEvent
	if db := s.inv.Store(); db != nil {
		stats, _ = db.CleanupStatistics()
		runs, _ = db.CleanupRuns(100)
		events, _ = db.HistoryEvents(200)
	}
	d := struct {
		Stats  store.CleanupStats
		Runs   []store.CleanupRun
		Events []store.HistoryEvent
	}{stats, runs, events}
	if e := renderTemplate(w, s.historyTpl, d); e != nil {
		log.Printf("render history: %v", e)
	}
}

func (s *Server) torrentDetail(w http.ResponseWriter, r *http.Request) {
	hash := strings.Trim(strings.TrimPrefix(r.URL.Path, "/torrents/"), "/")
	if hash == "" || strings.Contains(hash, "/") {
		http.NotFound(w, r)
		return
	}
	_, updated, last := s.inv.Snapshot()
	torrent, detailErr := s.inv.TorrentDetail(hash)
	if strings.Contains(strings.ToLower(detailErrString(detailErr)), "not found") {
		http.NotFound(w, r)
		return
	}
	torrent.AssociationStatus = normalizeTorrentStatus(torrent.AssociationStatus)
	storageView, filesUpdated, filesErr := s.inv.TorrentStorage(hash)
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
	}{torrent, updated, last, s.inv.IsRefreshing(), detailErr, files, fileCount, filesUpdated, filesErr, storageView.RemoveTorrent}
	if e := renderTemplate(w, s.torrentDetailTpl, data); e != nil {
		log.Printf("render torrent detail: %v", e)
	}
}

func (s *Server) apiMedia(w http.ResponseWriter, r *http.Request) {
	x, u, e := s.inv.Snapshot()
	writeJSON(w, map[string]any{"items": x, "updated": u, "error": errString(e), "refreshing": s.inv.IsRefreshing()})
}
func (s *Server) apiTorrents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": s.inv.TorrentSnapshot(), "refreshing": s.inv.IsRefreshing()})
}
func (s *Server) apiUnclaimed(w http.ResponseWriter, r *http.Request) {
	x, u, e := s.inv.UnclaimedSnapshot()
	writeJSON(w, map[string]any{"items": x, "updated": u, "error": errString(e), "refreshing": s.inv.IsRefreshing()})
}
func (s *Server) apiFiles(w http.ResponseWriter, r *http.Request) {
	x, mr, tr, u, e := s.inv.FileSnapshot()
	writeJSON(w, map[string]any{"items": x, "mediaRefs": mr, "torrentRefs": tr, "updated": u, "error": errString(e)})
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if s.tasks != nil {
		s.tasks.RunAsync(context.Background(), "inventory")
	} else {
		go func() {
			if e := s.inv.Refresh(context.Background()); e != nil {
				log.Printf("refresh: %v", e)
			}
		}()
	}
	http.Redirect(w, r, "/", 303)
}
func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	x, _, _ := s.inv.Snapshot()
	p, e := cleanup.Build(s.inv.Config().Storage.Path, s.inv.Config().Storage.TargetUsagePercent, s.inv.Config().Storage.CriticalUsagePercent, x)
	if e != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(p)
		return
	}
	writeJSON(w, p)
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
	log.Printf("%s listening on %s", appInfo.Name, addr)
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
	Torrent  model.Torrent
	Selected bool
}

type relatedUnclaimedFile struct {
	Path      string
	Selected  bool
	SizeBytes int64
}

type managedRemovalFile struct {
	Ref      model.MediaFileRef
	File     model.File
	Selected bool
	Label    string
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
	Label string
	Path  string
	Text  string
}

type removalPhysicalFileGroup struct {
	Paths         []string
	DisplayPaths  []removalDisplayPath
	SizeBytes     int64
	Exists        bool
	IdentityKnown bool
	Links         uint64
	MissingLinks  uint64
	Error         string
}

type removalData struct {
	Plan                 removal.RemovalPlan
	FileGroups           []removalPhysicalFileGroup
	TorrentTarget        bool
	TorrentSelected      bool
	SelectedActions      int
	PotentialBytes       int64
	Related              []relatedRemovalTorrent
	RelatedUnclaimed     []relatedUnclaimedFile
	ManagedGroups        []managedRemovalGroup
	ManagedFileCount     int
	ManagedAllSelected   bool
	ManagedSomeSelected  bool
	RelatedManaged       []relatedManagedMedia
	SelectedManaged      []model.MediaFileRef
	CanUnmonitorMovies   bool
	CanUnmonitorEpisodes bool
	BackURL              string
	MediaType            string
	MediaID              int
	Hash                 string
	UnclaimedPaths       []string
	SelectedUnclaimed    []string
}

func selectedUnclaimedSet(values []string) map[string]bool {
	m := map[string]bool{}
	for _, p := range values {
		p = filepath.Clean(strings.TrimSpace(p))
		if p != "" && p != "." {
			m[p] = true
		}
	}
	return m
}

func physicalCandidates(allFiles []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef, initial map[string]removal.CandidateFile, selectedManaged map[string]bool, selectedTorrents map[string]bool, selectedUnclaimed map[string]bool) map[string]removal.CandidateFile {
	byPath := filesByPath(allFiles)
	ids := map[physicalID]bool{}
	for p := range initial {
		if f, ok := byPath[filepath.Clean(p)]; ok && f.Exists && f.IdentityKnown {
			ids[physicalID{f.Device, f.Inode}] = true
		}
	}
	mediaByPath := map[string]model.MediaFileRef{}
	for _, r := range mediaRefs {
		mediaByPath[filepath.Clean(r.Path)] = r
	}
	torrentByPath := map[string]model.TorrentFileRef{}
	for _, r := range torrentRefs {
		torrentByPath[filepath.Clean(r.Path)] = r
	}
	for _, f := range allFiles {
		if !f.Exists || !f.IdentityKnown || !ids[physicalID{f.Device, f.Inode}] {
			continue
		}
		p := filepath.Clean(f.Path)
		if old, ok := initial[p]; ok { // refresh selection from authoritative requested sets
			switch old.Owner {
			case removal.MediaOwner:
				old.Selected = selectedManaged[old.OwnerKey]
			case removal.TorrentOwner:
				old.Selected = selectedTorrents[strings.ToLower(old.OwnerKey)]
			case removal.UnclaimedOwner:
				old.Selected = selectedUnclaimed[p] || old.Selected
			}
			initial[p] = old
			continue
		}
		if r, ok := mediaByPath[p]; ok {
			key := managedFileKey(r)
			initial[p] = removal.CandidateFile{Path: p, Owner: removal.MediaOwner, OwnerKey: key, Label: filepath.Base(p), Selected: selectedManaged[key]}
			continue
		}
		if r, ok := torrentByPath[p]; ok {
			h := strings.ToLower(r.Hash)
			initial[p] = removal.CandidateFile{Path: p, Owner: removal.TorrentOwner, OwnerKey: h, Label: filepath.Base(p), Selected: selectedTorrents[h]}
			continue
		}
		initial[p] = removal.CandidateFile{Path: p, Owner: removal.UnclaimedOwner, OwnerKey: p, Label: filepath.Base(p), Selected: selectedUnclaimed[p]}
	}
	return initial
}

func relatedUnclaimedFromCandidates(cm map[string]removal.CandidateFile, files map[string]model.File) []relatedUnclaimedFile {
	out := []relatedUnclaimedFile{}
	for p, c := range cm {
		if c.Owner != removal.UnclaimedOwner {
			continue
		}
		f := files[filepath.Clean(p)]
		out = append(out, relatedUnclaimedFile{Path: p, Selected: c.Selected, SizeBytes: f.SizeBytes})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out
}

func selectedUnclaimedPaths(xs []relatedUnclaimedFile) []string {
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
	if owner == removal.UnclaimedOwner {
		if best.IntegrationName != "" {
			label += " · Unclaimed"
		} else {
			label = "Unclaimed"
		}
	}
	return removalDisplayPath{Label: label, Path: path, Text: p}
}

func underPath(root, p string) bool {
	rel, e := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func groupRemovalFiles(files []removal.FileState, inventoryFiles []model.File) []removalPhysicalFileGroup {
	invByPath := filesByPath(inventoryFiles)
	counts := rootCounts(inventoryFiles)
	type physicalKey struct {
		device uint64
		inode  uint64
	}
	groups := make([]removalPhysicalFileGroup, 0, len(files))
	byPhysical := make(map[physicalKey]int)
	for _, f := range files {
		if f.Exists && f.IdentityKnown {
			key := physicalKey{device: f.Device, inode: f.Inode}
			if idx, ok := byPhysical[key]; ok {
				g := &groups[idx]
				seen := false
				for _, path := range g.Paths {
					if path == f.Path {
						seen = true
						break
					}
				}
				if !seen {
					g.Paths = append(g.Paths, f.Path)
				}
				if f.Links > g.Links {
					g.Links = f.Links
				}
				if g.Error == "" && f.Error != "" {
					g.Error = f.Error
				}
				continue
			}
			byPhysical[key] = len(groups)
			groups = append(groups, removalPhysicalFileGroup{
				Paths: []string{f.Path}, SizeBytes: f.SizeBytes, Exists: true,
				IdentityKnown: true, Links: f.Links, Error: f.Error,
			})
			continue
		}
		groups = append(groups, removalPhysicalFileGroup{
			Paths: []string{f.Path}, SizeBytes: f.SizeBytes, Exists: f.Exists,
			IdentityKnown: f.IdentityKnown, Links: f.Links, Error: f.Error,
		})
	}
	for i := range groups {
		sort.Strings(groups[i].Paths)
		if groups[i].IdentityKnown && groups[i].Links > uint64(len(groups[i].Paths)) {
			groups[i].MissingLinks = groups[i].Links - uint64(len(groups[i].Paths))
		}
		for _, p := range groups[i].Paths {
			f := invByPath[filepath.Clean(p)]
			owner := removal.UnclaimedOwner
			for _, st := range files {
				if filepath.Clean(st.Path) == filepath.Clean(p) {
					owner = st.Owner
					break
				}
			}
			groups[i].DisplayPaths = append(groups[i].DisplayPaths, displayPath(p, f, owner, "", counts))
		}
	}
	return groups
}

func mergeRemovalCandidate(m map[string]removal.CandidateFile, c removal.CandidateFile) {
	c.Path = filepath.Clean(c.Path)
	if old, ok := m[c.Path]; ok {
		old.Selected = old.Selected || c.Selected
		if old.Label == "" {
			old.Label = c.Label
		}
		m[c.Path] = old
		return
	}
	m[c.Path] = c
}

func candidateSlice(m map[string]removal.CandidateFile) []removal.CandidateFile {
	out := make([]removal.CandidateFile, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func managedFileKey(r model.MediaFileRef) string {
	return strings.ToLower(strings.TrimSpace(r.Source)) + ":" + strconv.Itoa(r.SourceFileID)
}

func mediaRefKey(m model.MediaRef) string { return fmt.Sprintf("%s:%d", m.Type, m.SourceID) }

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
		byGroup[group] = append(byGroup[group], managedRemovalFile{Ref: r, File: f, Selected: selected[managedFileKey(r)], Label: label})
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

func (s *Server) buildMediaRemovalPlan(kind model.MediaType, id int, selectionExplicit bool, selectedManaged map[string]bool, selectedTorrents map[string]bool, selectedUnclaimed map[string]bool) (removalData, error) {
	items, _, _ := s.inv.Snapshot()
	mr, ok := mediaRefFor(items, kind, id)
	if !ok {
		return removalData{}, fmt.Errorf("media not found")
	}
	refs, _, ferr := s.inv.ManagedFileRefs(kind, id)
	if ferr != nil {
		return removalData{}, ferr
	}
	if !selectionExplicit {
		for _, r := range refs {
			selectedManaged[managedFileKey(r)] = true
		}
	}
	files, allMediaRefs, allTorrentRefs, _, _ := s.inv.FileSnapshot()
	byPath := filesByPath(files)
	cm := map[string]removal.CandidateFile{}
	for _, r := range refs {
		if _, ok := byPath[filepath.Clean(r.Path)]; !ok {
			continue
		}
		sel := selectedManaged[managedFileKey(r)]
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: r.Path, Owner: removal.MediaOwner, OwnerKey: managedFileKey(r), Label: mr.Title, Selected: sel})
	}
	var found *model.Media
	for i := range items {
		if items[i].Type == kind && items[i].SourceID == id {
			found = &items[i]
			break
		}
	}
	related := []relatedRemovalTorrent{}
	if found != nil {
		for _, t := range found.Torrents {
			sel := selectedTorrents[strings.ToLower(t.Hash)]
			related = append(related, relatedRemovalTorrent{Torrent: t, Selected: sel})
			tf, _, _ := s.inv.TorrentFiles(t.Hash)
			for _, f := range tf {
				mergeRemovalCandidate(cm, removal.CandidateFile{Path: f.Path, Owner: removal.TorrentOwner, OwnerKey: strings.ToLower(t.Hash), Label: t.Name, Selected: sel})
			}
		}
	}
	cm = physicalCandidates(files, allMediaRefs, allTorrentRefs, cm, selectedManaged, selectedTorrents, selectedUnclaimed)
	relatedUnclaimed := relatedUnclaimedFromCandidates(cm, byPath)
	selectedUF := selectedUnclaimedPaths(relatedUnclaimed)
	if len(selectedUF) > 0 {
		if err := s.inv.VerifyUnclaimed(selectedUF); err != nil {
			return removalData{}, err
		}
	}
	if len(refs) == 0 && len(related) == 0 {
		return removalData{}, fmt.Errorf("nothing to remove")
	}
	sort.Slice(related, func(i, j int) bool {
		return strings.ToLower(related[i].Torrent.Name) < strings.ToLower(related[j].Torrent.Name)
	})
	key := fmt.Sprintf("%s:%d", kind, id)
	candidates := candidateSlice(cm)
	p := removal.Build(removal.MediaObject, key, mr.Title, s.inv.Config().Removal.DryRun, candidates)
	all := append([]removal.CandidateFile(nil), candidates...)
	for i := range all {
		all[i].Selected = true
	}
	potential := removal.Build(removal.MediaObject, key, mr.Title, s.inv.Config().Removal.DryRun, all).SelectedPathBytes
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
	for _, t := range related {
		if t.Selected {
			actions++
		}
	}
	moviesOpt, episodesOpt := ownerOptions(selectedRefs)
	return removalData{Plan: p, FileGroups: groupRemovalFiles(p.Files, files), SelectedActions: actions, PotentialBytes: potential, Related: related, RelatedUnclaimed: relatedUnclaimed, SelectedUnclaimed: selectedUF, ManagedGroups: groups, ManagedFileCount: len(refs), ManagedAllSelected: allSel, ManagedSomeSelected: some, SelectedManaged: selectedRefs, CanUnmonitorMovies: moviesOpt, CanUnmonitorEpisodes: episodesOpt, BackURL: fmt.Sprintf("/library/%s/%d", kind, id), MediaType: string(kind), MediaID: id}, nil
}

func (s *Server) buildTorrentRemovalPlan(hash string, targetSelected bool, selectedManaged map[string]bool, selectedUnclaimed map[string]bool) (removalData, error) {
	h := strings.ToLower(strings.TrimSpace(hash))
	var found *model.Torrent
	for _, t := range s.inv.TorrentSnapshot() {
		if strings.EqualFold(t.Hash, h) {
			x := t
			found = &x
			break
		}
	}
	if found == nil {
		return removalData{}, fmt.Errorf("torrent not found")
	}
	files, mrefs, trefs, _, ferr := s.inv.FileSnapshot()
	if ferr != nil {
		return removalData{}, ferr
	}
	byPath := filesByPath(files)
	proven := provenManagedRefsForTorrent(files, mrefs, trefs, h)
	cm := map[string]removal.CandidateFile{}
	tf, _, _ := s.inv.TorrentFiles(h)
	for _, f := range tf {
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: f.Path, Owner: removal.TorrentOwner, OwnerKey: h, Label: found.Name, Selected: targetSelected})
	}
	for _, r := range proven {
		sel := selectedManaged[managedFileKey(r)]
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: r.Path, Owner: removal.MediaOwner, OwnerKey: managedFileKey(r), Selected: sel})
	}
	selectedTorrents := map[string]bool{h: targetSelected}
	cm = physicalCandidates(files, mrefs, trefs, cm, selectedManaged, selectedTorrents, selectedUnclaimed)
	items, _, _ := s.inv.Snapshot()
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
	p := removal.Build(removal.TorrentObject, h, found.Name, s.inv.Config().Removal.DryRun, candidates)
	all := append([]removal.CandidateFile(nil), candidates...)
	for i := range all {
		all[i].Selected = true
	}
	potential := removal.Build(removal.TorrentObject, h, found.Name, s.inv.Config().Removal.DryRun, all).SelectedPathBytes
	selectedRefs := selectedManagedRefs(proven, selectedManaged)
	relatedUnclaimed := relatedUnclaimedFromCandidates(cm, byPath)
	selectedUF := selectedUnclaimedPaths(relatedUnclaimed)
	if len(selectedUF) > 0 {
		if err := s.inv.VerifyUnclaimed(selectedUF); err != nil {
			return removalData{}, err
		}
	}
	actions := len(selectedRefs) + len(selectedUF)
	if targetSelected {
		actions++
	}
	moviesOpt, episodesOpt := ownerOptions(selectedRefs)
	return removalData{Plan: p, FileGroups: groupRemovalFiles(p.Files, files), TorrentTarget: true, TorrentSelected: targetSelected, SelectedActions: actions, PotentialBytes: potential, RelatedManaged: related, RelatedUnclaimed: relatedUnclaimed, SelectedUnclaimed: selectedUF, SelectedManaged: selectedRefs, CanUnmonitorMovies: moviesOpt, CanUnmonitorEpisodes: episodesOpt, BackURL: "/torrents/" + url.PathEscape(h), Hash: h}, nil
}

func (s *Server) buildUnclaimedRemovalPlan(paths []string, selectedTorrents map[string]bool) (removalData, error) {
	files, mrefs, trefs, _, ferr := s.inv.FileSnapshot()
	if ferr != nil {
		return removalData{}, ferr
	}
	current, _, err := s.inv.UnclaimedSnapshot()
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
			return removalData{}, fmt.Errorf("file is no longer unclaimed: %s", p)
		}
		clean = append(clean, p)
		selectedUF[p] = true
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: p, Owner: removal.UnclaimedOwner, OwnerKey: p, Label: p, Selected: true})
	}
	if len(clean) == 0 {
		return removalData{}, fmt.Errorf("no unclaimed files selected")
	}
	if err := s.inv.VerifyUnclaimed(clean); err != nil {
		return removalData{}, err
	}
	// Physical identity is authoritative for discovering sibling paths. This is
	// what lets an Unclaimed hardlink expose the torrent that owns another path.
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
		tf, _, _ := s.inv.TorrentFiles(h)
		for _, r := range tf {
			mergeRemovalCandidate(cm, removal.CandidateFile{Path: r.Path, Owner: removal.TorrentOwner, OwnerKey: h, Label: h, Selected: selectedTorrents[h]})
		}
	}
	cm = physicalCandidates(files, mrefs, trefs, cm, map[string]bool{}, selectedTorrents, selectedUF)
	torrents := relatedTorrentList(cm, s.inv.TorrentSnapshot(), "")
	p := removal.Build(removal.UnclaimedObject, "unclaimed", fmt.Sprintf("%d unclaimed file(s)", len(clean)), s.inv.Config().Removal.DryRun, candidateSlice(cm))
	all := candidateSlice(cm)
	for i := range all {
		all[i].Selected = true
	}
	potential := removal.Build(removal.UnclaimedObject, "unclaimed", p.RequestedLabel, s.inv.Config().Removal.DryRun, all).SelectedPathBytes
	actions := len(clean)
	for _, t := range torrents {
		if t.Selected {
			actions++
		}
	}
	return removalData{Plan: p, FileGroups: groupRemovalFiles(p.Files, files), PotentialBytes: potential, BackURL: "/downloads/unclaimed", UnclaimedPaths: clean, Related: torrents, SelectedActions: actions}, nil
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

func (s *Server) removalMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	kind := model.MediaType(strings.TrimSpace(r.URL.Query().Get("type")))
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	d, err := s.buildMediaRemovalPlan(kind, id, r.URL.Query().Get("selection") == "1", selectedManagedSet(r.URL.Query()["managed_file"]), selectedTorrentSet(r), selectedUnclaimedSet(r.URL.Query()["unclaimed_path"]))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if e := renderTemplate(w, s.removalTpl, d); e != nil {
		log.Printf("render removal: %v", e)
	}
}
func (s *Server) removalTorrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	d, err := s.buildTorrentRemovalPlan(r.URL.Query().Get("hash"), targetSelection(r), selectedManagedSet(r.URL.Query()["managed_file"]), selectedUnclaimedSet(r.URL.Query()["unclaimed_path"]))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if e := renderTemplate(w, s.removalTpl, d); e != nil {
		log.Printf("render removal: %v", e)
	}
}
func (s *Server) removalUnclaimed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	d, err := s.buildUnclaimedRemovalPlan(r.URL.Query()["path"], selectedTorrentSet(r))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if e := renderTemplate(w, s.removalTpl, d); e != nil {
		log.Printf("render removal: %v", e)
	}
}

func (s *Server) executeRemoval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	kind := r.FormValue("kind")
	var d removalData
	var err error
	switch kind {
	case "media":
		mt := model.MediaType(r.FormValue("media_type"))
		id, _ := strconv.Atoi(r.FormValue("media_id"))
		torrents := map[string]bool{}
		for _, h := range r.Form["torrent"] {
			torrents[strings.ToLower(h)] = true
		}
		d, err = s.buildMediaRemovalPlan(mt, id, true, selectedManagedSet(r.Form["managed_file"]), torrents, selectedUnclaimedSet(r.Form["unclaimed_path"]))
	case "torrent":
		d, err = s.buildTorrentRemovalPlan(r.FormValue("hash"), r.FormValue("target") == "1", selectedManagedSet(r.Form["managed_file"]), selectedUnclaimedSet(r.Form["unclaimed_path"]))
	case "unclaimed":
		d, err = s.buildUnclaimedRemovalPlan(r.Form["path"], mapFromValues(r.Form["torrent"]))
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
	if !dry {
		switch kind {
		case "media":
			ownerFailed := false
			for _, ref := range d.SelectedManaged {
				if e := s.inv.RemoveManagedFile(ref); e != nil {
					errs = append(errs, fmt.Sprintf("managed file %s:%d: %v", ref.Source, ref.SourceFileID, e))
					ownerFailed = true
				} else {
					results = append(results, "managed file removed: "+ref.Path)
					removedManaged = append(removedManaged, ref)
				}
			}
			for _, rt := range d.Related {
				if !rt.Selected {
					continue
				}
				if e := s.inv.RemoveTorrent(rt.Torrent.Hash); e != nil {
					errs = append(errs, "torrent "+rt.Torrent.Name+": "+e.Error())
					ownerFailed = true
				} else {
					results = append(results, "torrent removed: "+rt.Torrent.Name)
				}
			}
			if !ownerFailed {
				for _, p := range d.SelectedUnclaimed {
					if e := os.Remove(p); e != nil {
						errs = append(errs, p+": "+e.Error())
					} else {
						results = append(results, "filesystem removed: "+p)
					}
				}
			}
		case "torrent":
			ownerFailed := false
			if d.TorrentSelected {
				if e := s.inv.RemoveTorrent(d.Hash); e != nil {
					errs = append(errs, e.Error())
					ownerFailed = true
				} else {
					results = append(results, "torrent removed by qBittorrent")
				}
			}
			for _, ref := range d.SelectedManaged {
				if e := s.inv.RemoveManagedFile(ref); e != nil {
					errs = append(errs, fmt.Sprintf("managed file %s:%d: %v", ref.Source, ref.SourceFileID, e))
					ownerFailed = true
				} else {
					results = append(results, "managed file removed: "+ref.Path)
					removedManaged = append(removedManaged, ref)
				}
			}
			if !ownerFailed {
				for _, p := range d.SelectedUnclaimed {
					if e := os.Remove(p); e != nil {
						errs = append(errs, p+": "+e.Error())
					} else {
						results = append(results, "filesystem removed: "+p)
					}
				}
			}
		case "unclaimed":
			// Owner-backed actions run before direct filesystem unlinking. If an
			// owner action fails, the unclaimed sibling is still preserved rather
			// than being removed first and leaving a surprising partial result.
			ownerFailed := false
			for _, rt := range d.Related {
				if !rt.Selected {
					continue
				}
				if e := s.inv.RemoveTorrent(rt.Torrent.Hash); e != nil {
					errs = append(errs, "torrent "+rt.Torrent.Name+": "+e.Error())
					ownerFailed = true
				} else {
					results = append(results, "torrent removed: "+rt.Torrent.Name)
				}
			}
			if !ownerFailed {
				for _, p := range d.UnclaimedPaths {
					if e := os.Remove(p); e != nil {
						errs = append(errs, p+": "+e.Error())
					} else {
						results = append(results, "filesystem removed: "+p)
					}
				}
			}
		}
		if r.FormValue("unmonitor_movies") == "1" {
			seen := map[int]bool{}
			for _, ref := range removedManaged {
				if strings.EqualFold(ref.Source, "radarr") && !seen[ref.MediaID] {
					seen[ref.MediaID] = true
					if e := s.inv.SetMovieMonitored(ref.MediaID, false); e != nil {
						errs = append(errs, fmt.Sprintf("unmonitor movie %d: %v", ref.MediaID, e))
					} else {
						results = append(results, fmt.Sprintf("movie unmonitored: %d", ref.MediaID))
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
				if e := s.inv.SetEpisodesMonitored(ids, false); e != nil {
					errs = append(errs, "unmonitor episodes: "+e.Error())
				} else {
					results = append(results, fmt.Sprintf("%d episode(s) unmonitored", len(ids)))
				}
			}
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
	payload, _ := json.Marshal(map[string]any{"plan": d.Plan, "managedFiles": d.SelectedManaged, "unmonitorMovies": r.FormValue("unmonitor_movies") == "1", "unmonitorEpisodes": r.FormValue("unmonitor_episodes") == "1", "results": results, "errors": errs})
	if db := s.inv.Store(); db != nil {
		_, _ = db.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: status, DryRun: dry, RequestedKind: string(d.Plan.Kind), RequestedKey: d.Plan.RequestedKey, RequestedLabel: d.Plan.RequestedLabel, ReclaimableBytes: d.Plan.ReclaimableBytes, Payload: payload, Error: strings.Join(errs, "; ")})
	}
	if !dry && s.tasks != nil {
		s.tasks.RunAsync(context.Background(), "inventory")
		s.tasks.RunAsync(context.Background(), "files")
		s.tasks.RunAsync(context.Background(), "files")
	}
	if r.Header.Get("X-Togetharr-Overlay") == "1" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "dryRun": dry, "results": results, "errors": errs})
		return
	}
	http.Redirect(w, r, "/history", http.StatusSeeOther)
}

func appChrome(active string) string {
	links := []struct{ Key, Label, URL string }{
		{"home", "Home", "/"},
		{"library", "Library", "/library"},
		{"torrents", "Torrents", "/torrents"},
		{"tasks", "Tasks", "/tasks"},
		{"history", "History", "/history"},
	}
	var b strings.Builder
	b.WriteString(`<style>
.appbar{display:flex;align-items:center;gap:18px;flex-wrap:wrap;padding:14px 0;border-bottom:1px solid #2b2b2b;background:#141414;position:relative;z-index:10}.appbrand{font-size:22px;font-weight:800;color:#eee;text-decoration:none;margin-right:8px}.appnav{display:flex;gap:14px;align-items:center;flex-wrap:wrap}.appnav a{color:#bbb;text-decoration:none}.appnav a.active{color:#fff;font-weight:750}.page-title{margin:22px 0 14px}.trash{border:0;background:transparent;color:#d66;cursor:pointer;font-size:18px;padding:2px 5px}.trash:disabled{color:#666;cursor:not-allowed}.modal-root:empty{display:none}.removal-overlay{position:fixed;inset:0;background:rgba(0,0,0,.72);backdrop-filter:blur(2px);display:grid;place-items:center;padding:24px;z-index:1000}.removal-dialog{width:min(900px,95vw);max-height:90vh;overflow:auto;background:#181818;border:1px solid #3a3a3a;border-radius:12px;padding:20px;box-shadow:0 20px 80px #000}.removal-dialog .top{display:flex;justify-content:space-between;gap:16px;align-items:start}.removal-dialog .summary{display:flex;gap:40px;flex-wrap:wrap;padding:14px 0;border-top:1px solid #333;border-bottom:1px solid #333;margin:14px 0}.removal-dialog .big{font-size:24px;font-weight:800}.removal-dialog .row{padding:9px 0;border-bottom:1px solid #2d2d2d}.removal-dialog .actions{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.removal-dialog .actions button{padding:9px 14px;border-radius:7px;border:1px solid #555;background:#222;color:#eee}.removal-dialog .actions button:disabled{opacity:.45;cursor:not-allowed}.disabled-tip{display:inline-block;cursor:not-allowed}.disabled-tip button{pointer-events:none}.managed-tree,.managed-media{margin:8px 0 4px 20px}.managed-group{margin:6px 0 0 18px}.managed-file{margin-left:18px;display:flex!important;gap:6px!important;padding:5px 0!important}.managed-file label{min-width:0;display:flex;align-items:center;gap:6px;flex:1}.managed-name{display:block;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.managed-size{white-space:nowrap;color:#aaa}.group-label{display:block;padding:5px 0}.managed-season{margin:3px 0!important;border-top:0!important;padding-top:0!important}.managed-season>summary{cursor:pointer;padding:5px 0;line-height:1.25}.managed-season>summary .group-label{display:inline-flex;align-items:center;gap:6px;padding:0}.managed-season>div{margin-left:18px}.removal-dialog .danger{background:#722!important;border-color:#933!important}.removal-dialog .simulate{background:#604d13!important}.removal-dialog .dry{display:inline-block;padding:5px 8px;border:1px solid #9b7b24;border-radius:6px;color:#ffcf66;font-weight:700}.removal-dialog .hardlink{color:#9fd3ff;font-weight:600}.removal-dialog details{margin-top:18px;border-top:1px solid #333;padding-top:12px}.removal-dialog summary{cursor:pointer;font-weight:700}.toast{position:fixed;right:20px;bottom:20px;background:#222;border:1px solid #444;border-radius:8px;padding:10px 14px;z-index:1200}
</style>`)
	b.WriteString(`<header class="appbar"><a class="appbrand" href="/">` + appInfo.Name + `</a><nav class="appnav">`)
	for _, l := range links {
		cls := ""
		if l.Key == active {
			cls = ` class="active"`
		}
		b.WriteString(`<a` + cls + ` href="` + l.URL + `">` + l.Label + `</a>`)
	}
	b.WriteString(`</nav></header><div id="removal-modal" class="modal-root"></div><script>
(function(){
 const root=document.getElementById('removal-modal');
 async function load(url, opts){const r=await fetch(url,opts);if(!r.ok)throw new Error(await r.text());return await r.text()}
 function close(){root.innerHTML='';document.body.style.overflow=''}
 function toast(msg){const x=document.createElement('div');x.className='toast';x.textContent=msg;document.body.appendChild(x);setTimeout(()=>x.remove(),2600)}
 function wire(){
   const cancel=root.querySelector('[data-removal-cancel]'); if(cancel)cancel.onclick=close;
   const overlay=root.querySelector('.removal-overlay'); if(overlay)overlay.addEventListener('click',e=>{if(e.target===overlay)close()});
   const sel=root.querySelector('[data-removal-selection]');
   if(sel){
     let timer; const refresh=()=>{clearTimeout(timer);timer=setTimeout(async()=>{try{const q=new URLSearchParams(new FormData(sel));root.innerHTML=await load(sel.action+'?'+q.toString(),{headers:{'X-Togetharr-Overlay':'1'}});wire()}catch(e){toast(e.message)}},90)};
     const syncBox=(all,picks)=>{if(!all)return;all.checked=picks.length>0&&picks.every(x=>x.checked);all.indeterminate=picks.some(x=>x.checked)&&!all.checked};
     const sync=()=>{
       sel.querySelectorAll('[data-managed-group]').forEach(g=>syncBox(g.querySelector('[data-managed-group-all]'),[...g.querySelectorAll('[data-managed-pick]')]));
       sel.querySelectorAll('[data-managed-scope]').forEach(g=>syncBox(g.querySelector('[data-managed-all]'),[...g.querySelectorAll('[data-managed-pick]')]));
       sel.querySelectorAll('[data-linked-scope]').forEach(g=>syncBox(g.querySelector('[data-select-all]'),[...g.querySelectorAll('[data-linked-pick]')]));
       sel.querySelectorAll('[data-unclaimed-scope]').forEach(g=>syncBox(g.querySelector('[data-unclaimed-all]'),[...g.querySelectorAll('[data-unclaimed-pick]')]));
     };
     sel.querySelectorAll('[data-managed-group]').forEach(g=>{const all=g.querySelector('[data-managed-group-all]');const picks=[...g.querySelectorAll('[data-managed-pick]')];if(all){all.onclick=e=>e.stopPropagation();all.onchange=()=>{picks.forEach(x=>x.checked=all.checked);sync();refresh()}}});
     sel.querySelectorAll('[data-managed-scope]').forEach(g=>{const all=g.querySelector('[data-managed-all]');const picks=[...g.querySelectorAll('[data-managed-pick]')];if(all){all.onclick=e=>e.stopPropagation();all.onchange=()=>{picks.forEach(x=>x.checked=all.checked);sync();refresh()}}});
     sel.querySelectorAll('[data-linked-scope]').forEach(g=>{const all=g.querySelector('[data-select-all]');const picks=[...g.querySelectorAll('[data-linked-pick]')];if(all){all.onclick=e=>e.stopPropagation();all.onchange=()=>{picks.forEach(x=>x.checked=all.checked);sync();refresh()}}});
     sel.querySelectorAll('[data-unclaimed-scope]').forEach(g=>{const all=g.querySelector('[data-unclaimed-all]');const picks=[...g.querySelectorAll('[data-unclaimed-pick]')];if(all){all.onclick=e=>e.stopPropagation();all.onchange=()=>{picks.forEach(x=>x.checked=all.checked);sync();refresh()}}});
     sel.querySelectorAll('[data-managed-pick],[data-linked-pick],[data-unclaimed-pick]').forEach(x=>x.onchange=()=>{sync();refresh()});
     const target=sel.querySelector('[data-target-pick]');if(target)target.onchange=refresh;sync();
   }
   const exec=root.querySelector('[data-removal-execute]');
   if(exec)exec.onsubmit=async e=>{e.preventDefault();try{const r=await fetch(exec.action,{method:'POST',body:new FormData(exec),headers:{'X-Togetharr-Overlay':'1'}});if(!r.ok)throw new Error(await r.text());const j=await r.json();close();toast(j.dryRun?'Removal simulation recorded':'Removal completed');if(!j.dryRun)location.reload()}catch(err){toast(err.message)}};
 }
 async function open(url){try{root.innerHTML=await load(url,{headers:{'X-Togetharr-Overlay':'1'}});document.body.style.overflow='hidden';wire()}catch(e){toast(e.message)}}
 document.addEventListener('click',e=>{const b=e.target.closest('[data-removal-url]');if(!b||b.disabled)return;e.preventDefault();open(b.dataset.removalUrl)});
 document.addEventListener('submit',e=>{const f=e.target.closest('[data-removal-launch]');if(!f)return;e.preventDefault();const q=new URLSearchParams(new FormData(f));if(!q.toString())return;open(f.action+'?'+q.toString())});
 let filterTimer,filterRequest;
 function filterURL(f){const q=new URLSearchParams(new FormData(f));q.delete('page');return f.action+(q.toString()?'?'+q.toString():'')}
 function syncFilterClear(f){const clear=f.querySelector('[data-filter-clear]');if(!clear)return;let active=false;for(const el of f.querySelectorAll('input[type=search],select,input[type=checkbox]')){if(el.type==='search'&&el.value.trim()){active=true;break}if(el.type==='checkbox'&&el.checked){active=true;break}if(el.tagName==='SELECT'&&el.value.toLowerCase()!=='any'){active=true;break}}clear.hidden=!active}
 function syncUnclaimedSelection(){
   const ua=document.getElementById('unclaimedAll'),up=[...document.querySelectorAll('.unclaimedPick')],ub=document.getElementById('unclaimedRemoveButton');
   up.forEach(x=>{const id=x.dataset.unclaimedGroup;document.querySelectorAll('.unclaimedGroupPath[data-unclaimed-group="'+id+'"]').forEach(h=>h.disabled=!x.checked)});
   const any=up.some(x=>x.checked);if(ub){ub.disabled=!any;const w=ub.closest('.remove-wrap');if(w){w.title=any?'':'No files selected';w.style.cursor=any?'default':'not-allowed'}}if(ua){ua.checked=up.length>0&&up.every(x=>x.checked);ua.indeterminate=any&&!ua.checked}
 }
 function wireDynamic(){ syncUnclaimedSelection() }
 document.addEventListener('change',e=>{if(e.target.id==='unclaimedAll'){document.querySelectorAll('.unclaimedPick').forEach(x=>x.checked=e.target.checked);syncUnclaimedSelection()}else if(e.target.classList.contains('unclaimedPick'))syncUnclaimedSelection()});
 document.addEventListener('click',async e=>{const b=e.target.closest('[data-unclaimed-scan]');if(!b)return;e.preventDefault();b.disabled=true;const old=b.textContent;b.textContent='Scanning…';try{const r=await fetch('/downloads/unclaimed/scan',{method:'POST',headers:{'X-Togetharr-Scan':'1'}});const j=await r.json();if(!r.ok||!j.ok)throw new Error(j.error||'Scan failed');toast('Unclaimed scan complete');location.reload()}catch(err){toast(err.message)}finally{b.disabled=false;b.textContent=old}});
 async function refreshFilter(f){
   clearTimeout(filterTimer);const url=filterURL(f);syncFilterClear(f);
   if(filterRequest)filterRequest.abort();filterRequest=new AbortController();
   try{const r=await fetch(url,{signal:filterRequest.signal,headers:{'X-Togetharr-Filter':'1'}});if(!r.ok)throw new Error(await r.text());const doc=new DOMParser().parseFromString(await r.text(),'text/html');const fresh=doc.querySelector('[data-filter-results]'),current=document.querySelector('[data-filter-results]');if(!fresh||!current)throw new Error('Filtered results unavailable');current.replaceWith(fresh);const fc=doc.querySelector('[data-filter-count]'),cc=document.querySelector('[data-filter-count]');if(fc&&cc)cc.textContent=fc.textContent;history.replaceState(null,'',url);wireDynamic()}catch(e){if(e.name!=='AbortError')toast(e.message)}
 }
 function scheduleFilter(f,delay){clearTimeout(filterTimer);filterTimer=setTimeout(()=>refreshFilter(f),delay)}
 document.addEventListener('submit',e=>{const f=e.target.closest('form[data-auto-filter]');if(!f)return;e.preventDefault();scheduleFilter(f,0)});
 document.addEventListener('change',e=>{const f=e.target.closest('form[data-auto-filter]');if(!f)return;if(e.target.matches('select,input[type=checkbox]'))scheduleFilter(f,0)});
 document.addEventListener('input',e=>{const f=e.target.closest('form[data-auto-filter]');if(!f||!e.target.matches('input[type=search]'))return;scheduleFilter(f,260)});
 document.addEventListener('click',e=>{const a=e.target.closest('[data-filter-clear]');if(!a)return;const f=a.closest('form[data-auto-filter]');if(!f)return;e.preventDefault();for(const el of f.querySelectorAll('input[type=search]'))el.value='';for(const el of f.querySelectorAll('select'))el.selectedIndex=0;for(const el of f.querySelectorAll('input[type=checkbox]'))el.checked=false;scheduleFilter(f,0)});
 document.querySelectorAll('form[data-auto-filter]').forEach(syncFilterClear);wireDynamic();
 window.TogetharrRemoval={open,close};
})();
</script>`)
	return b.String()
}

const homeHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{appName}}</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.nav h1{margin-right:12px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px;margin-top:20px}.card{background:#1b1b1b;padding:16px;border-radius:10px}.card h2{margin:0 0 12px;font-size:18px}.big{font-size:32px;font-weight:800}.muted{color:#aaa}.good{color:#8fd99c}.warn{color:#ffcf66}.bad{color:#ff8f8f}.kv{display:grid;grid-template-columns:1fr auto;gap:7px 12px}.mono{font-family:ui-monospace,monospace}.full{grid-column:1/-1}button{padding:8px 12px}</style></head><body>{{chrome "home"}}<h1 class=page-title>Home</h1>{{if .LastErr}}<p class=bad>{{.LastErr}}</p>{{end}}<div class=grid><section class=card><h2>Storage</h2>{{if .Plan.Available}}<div class=big>{{printf "%.2f" .Plan.UsagePercent}}%</div><div class=muted>Target {{printf "%.1f" $.Plan.TargetUsagePercent}}</div>{{if gt .Plan.NeedBytes 0}}<p class=bad><b>Cleanup active.</b><br>{{.Plan.Message}}<br>{{len .Plan.Selected}} media currently selected · {{humanU .Plan.SelectedBytes}} planned.</p>{{else}}<p class=good><b>Cleanup inactive.</b><br>{{.Plan.Message}}</p>{{end}}{{else}}<div class=big bad>UNAVAILABLE</div><p>{{.Plan.Message}}</p>{{if .PlanErr}}<span class=bad>{{.PlanErr}}</span>{{end}}{{end}}{{if .UnclaimedAvailable}}<p><a href="/downloads/unclaimed">Unclaimed files →</a></p>{{end}}</section><section class=card><h2>Library</h2><div class=big>{{.TotalMedia}}</div><div class=kv><span>Movies</span><b>{{.Movies}}</b><span>Series</span><b>{{.Series}}</b><span>Library size</span><b>{{human .LibraryBytes}}</b></div><p><a href="/library">Browse Library →</a></p></section><section class=card><h2>Torrents</h2><div class=big>{{.TotalTorrents}}</div><div class=kv><span>Associated</span><b>{{.Associated}}</b><span>Superseded</span><b>{{.Superseded}}</b><span>Orphaned</span><b>{{.Orphaned}}</b><span>Unassociated</span><b>{{.Unassociated}}</b>{{if gt .ObsoleteKnown 0}}<span>Known reclaimable</span><b>{{human .ObsoleteReclaimable}}</b>{{end}}</div><p><a href="/torrents">Browse Torrents →</a></p>{{if .UnclaimedError}}<p class=warn>Unclaimed scan unavailable: {{.UnclaimedError}}</p>{{end}}</section><section class=card><h2>Lifetime statistics</h2><div class=big>{{human .Stats.ReclaimedBytes}}</div><div class=muted>actual space reclaimed</div><div class=kv style="margin-top:12px"><span>Cleanup runs</span><b>{{.Stats.Runs}}</b><span>Media removed</span><b>{{.Stats.MediaRemoved}}</b><span>Torrents removed</span><b>{{.Stats.TorrentsRemoved}}</b><span>Library bytes removed</span><b>{{human .Stats.MediaBytes}}</b><span>Last 30 days</span><b>{{human .Stats.Last30Bytes}}</b></div><p><a href="/history">Cleanup history →</a></p></section>{{if .Services}}<section class=card><h2>Services</h2><div class=kv>{{range .Services}}<span>{{.Name}}</span><b class="{{if .OK}}good{{else}}bad{{end}}">{{if .OK}}Connected{{else}}Unavailable{{end}}</b>{{end}}</div></section>{{end}}</div></body></html>`

const libraryHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Library · {{appName}}</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.card{background:#1b1b1b;padding:14px;border-radius:10px;margin-top:14px}.filters{display:flex;gap:10px;align-items:end;flex-wrap:wrap}.filters label{display:flex;flex-direction:column;gap:4px;font-size:13px;color:#bbb}.filters input,.filters select,.filters button{background:#161616;color:#eee;border:1px solid #444;border-radius:6px;padding:7px 9px}.filters input[type=search]{min-width:260px}.filters input[type=checkbox]{width:auto;margin:0}.filters .toggle{flex-direction:row;align-items:center;gap:7px;padding-bottom:8px;white-space:nowrap}.filters .clear{padding-bottom:7px}.warn{color:#ffcf66}.bad{color:#ff8f8f}.muted{color:#aaa}table{border-collapse:collapse;width:100%;margin-top:18px}th,td{padding:8px;border-bottom:1px solid #333;text-align:left;font-size:14px}th{position:sticky;top:0;background:#111;white-space:nowrap}th a{color:#eee;text-decoration:none}.value{font-weight:700}.pager{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:14px 0}.pager a,.pager span{padding:6px 9px;border:1px solid #333;border-radius:6px;text-decoration:none}.current{background:#2b2b2b;font-weight:700}.disabled{color:#666}.pagesize{margin-left:auto;display:flex;gap:6px;align-items:center}.active{font-weight:800;color:#fff!important;border-color:#777!important}.inventory-summary{margin:0 0 12px;line-height:1.7}.result-count{margin:10px 0 0}</style></head><body>{{chrome "library"}}<h1 class=page-title>Library</h1>{{if .Refreshing}}<span class=warn>Refresh in progress…</span>{{end}}{{if .LastErr}}<span class=bad>{{.LastErr}}</span>{{end}}<div class=card><form class=filters data-auto-filter method=get action="/library"><label>Search<input type=search name=q value="{{.Query}}" placeholder="Title, path, tag…"></label><label>Type<select name=type><option value="any" {{if eq .TypeFilter "any"}}selected{{end}}>Any</option><option value="movie" {{if eq .TypeFilter "movie"}}selected{{end}}>Movies</option><option value="series" {{if eq .TypeFilter "series"}}selected{{end}}>Series</option></select></label><label>Requested<select name=requested><option value="any" {{if eq .RequestedFilter "any"}}selected{{end}}>Any</option><option value="yes" {{if eq .RequestedFilter "yes"}}selected{{end}}>Yes</option><option value="no" {{if eq .RequestedFilter "no"}}selected{{end}}>No</option></select></label><label>Watched<select name=watched><option value="any" {{if eq .WatchedFilter "any"}}selected{{end}}>Any</option><option value="yes" {{if eq .WatchedFilter "yes"}}selected{{end}}>Watched</option><option value="no" {{if eq .WatchedFilter "no"}}selected{{end}}>Unwatched</option></select></label><label>Torrent<select name=torrent><option value="any" {{if eq .TorrentFilter "any"}}selected{{end}}>Any</option><option value="yes" {{if eq .TorrentFilter "yes"}}selected{{end}}>Associated</option><option value="no" {{if eq .TorrentFilter "no"}}selected{{end}}>None</option></select></label><label class=toggle><input type=checkbox name=show_no_files value=1 {{if .ShowNoFiles}}checked{{end}}> Show media with no files</label><input type=hidden name=sort value="{{.Sort}}"><input type=hidden name=order value="{{.Order}}"><input type=hidden name=page_size value="{{.PageSize}}"><a class=clear data-filter-clear href="{{.ClearURL}}">Clear</a></form></div><div data-filter-results><div class=muted style="margin-top:10px"><b>{{.TotalItems}}</b>{{if ne .TotalItems .AllItems}} of {{.AllItems}}{{end}} media</div><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{range .PageLinks}}{{if eq .Value $.Page}}<span class=current>{{.Value}}</span>{{else}}<a href="{{.URL}}">{{.Value}}</a>{{end}}{{end}}{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}<div class=pagesize>Per page: {{range .SizeLinks}}<a class="{{if eq .Value $.PageSize}}active{{end}}" href="{{.URL}}">{{.Value}}</a>{{end}}</div></div><table><thead><tr><th><a href="{{index .SortURLs "title"}}">Media</a></th><th><a href="{{index .SortURLs "value"}}">Value</a></th><th><a href="{{index .SortURLs "type"}}">Type</a></th><th><a href="{{index .SortURLs "rating"}}">Rating</a></th><th><a href="{{index .SortURLs "votes"}}">Votes</a></th><th><a href="{{index .SortURLs "views"}}">Views</a></th><th><a href="{{index .SortURLs "lastwatched"}}">Last watched</a></th><th><a href="{{index .SortURLs "requested"}}">Requested</a></th><th><a href="{{index .SortURLs "size"}}">Size</a></th><th><a href="{{index .SortURLs "torrents"}}">Torrents</a></th><th></th></tr></thead><tbody>{{range .Rows}}{{$m:=.Media}}<tr><td><a href="/library/{{$m.Type}}/{{$m.SourceID}}">{{$m.Title}} {{if $m.Year}}({{$m.Year}}){{end}}</a></td><td class=value>{{printf "%.1f" $m.Value}}</td><td>{{$m.Type}}</td><td>{{if gt $m.Rating 0.0}}{{printf "%.1f" $m.Rating}}{{else}}—{{end}}</td><td>{{$m.VoteCount}}</td><td>{{$m.Views}}</td><td>{{fmtTime $m.LastWatched}}</td><td>{{if $m.Requested}}yes{{else}}no{{end}}</td><td>{{human $m.SizeBytes}}</td><td>{{len $m.Torrents}}</td><td>{{if .Removable}}<button class=trash title="Remove" data-removal-url="/removal/media?type={{$m.Type}}&id={{$m.SourceID}}">🗑</button>{{else}}<button class=trash disabled title="No files to remove">🗑</button>{{end}}</td></tr>{{else}}<tr><td colspan=11 class=muted>No media match these filters.</td></tr>{{end}}</tbody></table><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}</div></div></body></html>`

const historyHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>History · {{appName}}</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.card{background:#1b1b1b;padding:14px;border-radius:10px;margin:16px 0}table{border-collapse:collapse;width:100%}th,td{padding:8px;border-bottom:1px solid #333;text-align:left;vertical-align:top}.muted{color:#aaa}.dry{color:#ffcf66}.bad{color:#ff8f8f}</style></head><body>{{chrome "history"}}<h1 class=page-title>History</h1><h2>Removal events</h2>{{if .Events}}<table><thead><tr><th>When</th><th>Object</th><th>Mode</th><th>Status</th><th>Reclaimable</th><th>Error</th></tr></thead><tbody>{{range .Events}}<tr><td>{{fmtUpdated .CreatedAt}}</td><td><b>{{.RequestedLabel}}</b><br><span class=muted>{{.RequestedKind}}</span></td><td>{{if .DryRun}}<span class=dry>dry run</span>{{else}}live{{end}}</td><td>{{.Status}}</td><td>{{human .ReclaimableBytes}}</td><td class=bad>{{.Error}}</td></tr>{{end}}</tbody></table>{{else}}<p class=muted>No removal events yet.</p>{{end}}{{if .Runs}}<h2>Legacy cleanup history</h2><table><thead><tr><th>Completed</th><th>Status</th><th>Storage</th><th>Media</th><th>Torrents</th><th>Reclaimed</th></tr></thead><tbody>{{range .Runs}}<tr><td>{{fmtUpdated .CompletedAt}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .UsageBefore}}% → {{printf "%.2f" .UsageAfter}}%</td><td>{{.MediaRemoved}}</td><td>{{.TorrentsRemoved}}</td><td>{{human .ReclaimedBytes}}</td></tr>{{end}}</tbody></table>{{end}}</body></html>`

const profileHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Media.Title}} · {{appName}}</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.muted{color:#aaa}.bad{color:#ff8f8f}.warn{color:#ffcf66}.positive{color:#8fd99c}.mono{font-family:ui-monospace,monospace;overflow-wrap:anywhere}.page{max-width:1380px;margin:0 auto;padding:20px 0 32px}.top{display:flex;gap:14px;align-items:center;flex-wrap:wrap;margin-bottom:18px}.hero{display:flex;align-items:flex-start;justify-content:space-between;gap:28px;border-bottom:1px solid #2e2e2e;padding-bottom:18px}.hero h1{font-size:30px;margin:0 0 6px}.value{font-size:52px;font-weight:800;line-height:.95;text-align:right}.summary{display:grid;grid-template-columns:minmax(300px,1.2fr) repeat(2,minmax(220px,.8fr));gap:14px;margin-top:14px}.panel,.section{background:#1b1b1b;border:1px solid #242424;border-radius:9px;padding:14px 16px}.panel h2,.section h2{font-size:16px;margin:0 0 10px}.kv{display:grid;grid-template-columns:max-content minmax(0,1fr);gap:6px 12px;font-size:14px}.kv div:nth-child(odd){color:#aaa}.reasons{width:100%;border-collapse:collapse;font-size:14px}.reasons td{padding:6px 2px;border-bottom:1px solid #303030;vertical-align:top}.reasons td:last-child{text-align:right;font-weight:700;padding-left:12px}.section{margin-top:14px}.section-head{display:flex;justify-content:space-between;gap:12px;align-items:baseline;margin-bottom:4px}.section-head h2{margin:0}.count{color:#aaa;font-weight:400}.row{border-top:1px solid #303030;padding:9px 0;display:grid;grid-template-columns:minmax(0,1fr) auto;gap:16px;align-items:center}.row-title{font-weight:650;overflow-wrap:anywhere}.row-meta{font-size:13px;color:#aaa;margin-top:2px}.row-action{white-space:nowrap}.group-title{font-size:14px;margin:14px 0 2px;color:#ddd}.footer{margin-top:16px;font-size:13px;color:#888}@media(max-width:900px){.summary{grid-template-columns:1fr}.hero{align-items:flex-end}.value{font-size:42px}.row{grid-template-columns:1fr}.row-action{justify-self:start}}</style></head><body>{{chrome "library"}}<div class=page>{{if .Refreshing}}<span class=warn>Refreshing…</span>{{end}}{{if .LastErr}}<span class=bad>{{.LastErr}}</span>{{end}}<div class=hero><div><h1>{{.Media.Title}} {{if .Media.Year}}({{.Media.Year}}){{end}}</h1><div class=muted>{{.Media.Type}}</div></div><div><div class=value>{{printf "%.1f" .Media.Value}}</div><div class=muted style="text-align:right">Value</div></div><button class=trash title="{{if or (gt .FileCount 0) .Current .Superseded .Orphaned}}Remove{{else}}Nothing to remove{{end}}" {{if or (gt .FileCount 0) .Current .Superseded .Orphaned}}data-removal-url="/removal/media?type={{.Media.Type}}&id={{.Media.SourceID}}"{{else}}disabled{{end}}>🗑</button></div><div class=summary><section class=panel><h2>Value breakdown</h2><table class=reasons>{{if .Media.Reasons}}{{range .Media.Reasons}}<tr><td><strong>{{.Label}}</strong><br><span class=muted>{{.Value}}</span></td><td class=positive>{{printf "%+.1f" .Points}}</td></tr>{{end}}{{else}}<tr><td class=muted>No Value contributions.</td></tr>{{end}}</table></section><section class=panel><h2>Media</h2><div class=kv><div>Size</div><div>{{human .Media.SizeBytes}}</div><div>Path</div><div class=mono>{{.Media.Path}}</div><div>Added</div><div>{{if .Media.AddedAt.IsZero}}Unknown{{else}}{{.Media.AddedAt.Local.Format "2006-01-02"}}{{end}}</div><div>Rating</div><div>{{if gt .Media.Rating 0.0}}{{printf "%.1f" .Media.Rating}} / 10{{else}}—{{end}}</div><div>Votes</div><div>{{.Media.VoteCount}}</div><div>Requested</div><div>{{if .Media.Requested}}yes{{else}}no{{end}}</div><div>Favorite</div><div>{{if .Media.Favorite}}yes{{else}}no{{end}}</div></div></section><section class=panel><h2>Activity & IDs</h2><div class=kv><div>Views</div><div>{{.Media.Views}}</div><div>Unique viewers</div><div>{{.Media.UniqueViewers}}</div><div>Last watched</div><div>{{fmtTime .Media.LastWatched}}</div><div>TMDB</div><div>{{if .Media.TMDBID}}{{.Media.TMDBID}}{{else}}—{{end}}</div><div>TVDB</div><div>{{if .Media.TVDBID}}{{.Media.TVDBID}}{{else}}—{{end}}</div><div>IMDB</div><div>{{if .Media.IMDBID}}{{.Media.IMDBID}}{{else}}—{{end}}</div></div></section></div><section class=section><div class=section-head><h2>Files</h2><span class=count>{{.FileCount}}</span></div>{{if .FilesErr}}<p class=warn>Files unavailable: {{.FilesErr}}</p>{{else if .FilesUpdated.IsZero}}<p class=muted>No file information yet.</p>{{else if not .Files}}<p class=muted>No files.</p>{{else}}{{range .Files}}<div class=row><div><div class="row-title mono" title="{{.File.Path}}">{{shortPath .File.Path}}</div>{{if and .File.Exists .File.IdentityKnown (gt .File.Links 1)}}{{range .SharedWith}}<div class="row-title mono" title="{{.Path}}">{{shortPath .Path}}</div>{{end}}{{end}}<div class=row-meta>{{human .File.SizeBytes}}{{if .File.Exists}}{{if .File.IdentityKnown}}{{if gt .File.Links 1}} · <strong>hardlinked · {{.File.Links}} paths / 1 physical file</strong>{{end}}{{end}}{{else}} · missing{{end}}</div></div></div>{{end}}{{if gt .FileCount (len .Files)}}<p class=muted>Showing first {{len .Files}} of {{.FileCount}} files.</p>{{end}}<div class=row-meta style="margin-top:10px">{{if .RemoveMedia.Known}}Removing media frees <strong>{{human .RemoveMedia.ReclaimableBytes}}</strong>{{end}}{{if .RemoveWithCurrent.Known}} · Removing media and current torrents frees <strong>{{human .RemoveWithCurrent.ReclaimableBytes}}</strong>{{end}}</div><div class=row-meta></div>{{end}}</section><section class=section><div class=section-head><h2>Torrents</h2></div>{{if .Current}}<h3 class=group-title>Current <span class=count>({{len .Current}})</span></h3>{{range .Current}}<div class=row><div><div class=row-title>{{.Name}}</div><div class=row-meta>{{human .SizeBytes}} · ratio {{printf "%.2f" .Ratio}} · {{.SeedsSwarm}} seeds · {{.LeechersSwarm}} leechers · {{rate .UploadSpeed}} up</div></div><a class=row-action href="/torrents/{{.Hash}}">View →</a></div>{{end}}{{end}}{{if .Superseded}}<h3 class=group-title>Superseded <span class=count>({{len .Superseded}})</span></h3>{{range .Superseded}}<div class=row><div><div class=row-title>{{.Name}}</div><div class=row-meta>{{human .SizeBytes}} · ratio {{printf "%.2f" .Ratio}} · {{.SeedsSwarm}} seeds · {{.LeechersSwarm}} leechers{{if .ReclaimableKnown}} · {{human .ReclaimableBytes}} reclaimable{{end}}</div></div><a class=row-action href="/torrents/{{.Hash}}">View →</a></div>{{end}}{{end}}{{if .Orphaned}}<h3 class=group-title>Orphaned <span class=count>({{len .Orphaned}})</span></h3>{{range .Orphaned}}<div class=row><div><div class=row-title>{{.Name}}</div><div class=row-meta>{{human .SizeBytes}} · ratio {{printf "%.2f" .Ratio}} · {{.SeedsSwarm}} seeds · {{.LeechersSwarm}} leechers{{if .ReclaimableKnown}} · {{human .ReclaimableBytes}} reclaimable{{end}}</div></div><a class=row-action href="/torrents/{{.Hash}}">View →</a></div>{{end}}{{end}}{{if and (not .Current) (not .Superseded) (not .Orphaned)}}<p class=muted>No torrents.</p>{{end}}</section><div class=footer></div>{{if .Refreshing}}<script>setTimeout(()=>location.reload(),1500)</script>{{end}}</div></body></html>`

const torrentHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Torrents · {{appName}}</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.card{background:#1b1b1b;padding:14px;border-radius:10px;margin:16px 0}.filters{display:flex;gap:10px;align-items:end;flex-wrap:wrap}.filters label{display:flex;flex-direction:column;gap:4px;font-size:13px;color:#bbb}.filters input,.filters select,.filters button{background:#161616;color:#eee;border:1px solid #444;border-radius:6px;padding:7px 9px}.filters input[type=search]{min-width:280px}.filters .clear{padding-bottom:7px}.muted{color:#aaa}.warn{color:#ffcf66}.bad{color:#ff8f8f}.associated{color:#8fd99c}.superseded{color:#ff9f66}.orphaned{color:#ffcf66}.unassociated{color:#aaa}.mono{font-family:ui-monospace,monospace;overflow-wrap:anywhere}table{border-collapse:collapse;width:100%;margin-top:18px}th,td{padding:8px;border-bottom:1px solid #333;text-align:left;font-size:14px;vertical-align:top}th{position:sticky;top:0;background:#111;white-space:nowrap}th a{color:#eee;text-decoration:none}.status{font-weight:800}.pager{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:14px 0}.pager a,.pager span{padding:6px 9px;border:1px solid #333;border-radius:6px;text-decoration:none}.current{background:#2b2b2b;font-weight:700}.disabled{color:#666}.pagesize{margin-left:auto;display:flex;gap:6px;align-items:center}.active{font-weight:800;color:#fff!important;border-color:#777!important}</style></head><body>{{chrome "torrents"}}<h1 class=page-title>Torrents</h1>{{if .Refreshing}}<span class=warn>Refreshing…</span>{{end}}{{if .LastErr}}<span class=bad>{{.LastErr}}</span>{{end}}<div class="inventory-summary muted"><span><b>{{.AllItems}}</b> torrents · <span class=associated>{{.Associated}} associated</span> · <span class=superseded>{{.Superseded}} superseded</span> · <span class=orphaned>{{.Orphaned}} orphaned</span> · <span class=unassociated>{{.Unassociated}} unassociated</span></span>{{if gt .ObsoleteKnown 0}}<br><span><b>{{human .ObsoleteReclaimable}}</b> reclaimable across {{.ObsoleteKnown}} torrents</span>{{end}}</div><div class=card><form class=filters data-auto-filter method=get action="/torrents"><label>Search<input type=search name=q value="{{.Query}}" placeholder="Name, hash, tracker, category, media…"></label><label>Status<select name=status><option value="any" {{if eq .StatusFilter "ANY"}}selected{{end}}>Any</option><option value="associated" {{if eq .StatusFilter "ASSOCIATED"}}selected{{end}}>Associated</option><option value="superseded" {{if eq .StatusFilter "SUPERSEDED"}}selected{{end}}>Superseded</option><option value="orphaned" {{if eq .StatusFilter "ORPHANED"}}selected{{end}}>Orphaned</option><option value="unassociated" {{if eq .StatusFilter "UNASSOCIATED"}}selected{{end}}>Unassociated</option></select></label><label>Reclaimable<select name=reclaimable><option value="any" {{if eq .ReclaimableFilter "any"}}selected{{end}}>Any</option><option value="positive" {{if eq .ReclaimableFilter "positive"}}selected{{end}}>Greater than 0</option><option value="known" {{if eq .ReclaimableFilter "known"}}selected{{end}}>Known</option><option value="unknown" {{if eq .ReclaimableFilter "unknown"}}selected{{end}}>Unknown</option></select></label><label>Activity<select name=activity><option value="any" {{if eq .ActivityFilter "any"}}selected{{end}}>Any</option><option value="active" {{if eq .ActivityFilter "active"}}selected{{end}}>Active</option><option value="inactive" {{if eq .ActivityFilter "inactive"}}selected{{end}}>Inactive</option></select></label><input type=hidden name=sort value="{{.Sort}}"><input type=hidden name=order value="{{.Order}}"><input type=hidden name=page_size value="{{.PageSize}}"><a class=clear data-filter-clear href="{{.ClearURL}}">Clear</a></form></div><div data-filter-results><div class="result-count muted"><b>{{.TotalItems}}</b>{{if ne .TotalItems .AllItems}} results{{else}} torrents{{end}}</div><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{range .PageLinks}}{{if eq .Value $.Page}}<span class=current>{{.Value}}</span>{{else}}<a href="{{.URL}}">{{.Value}}</a>{{end}}{{end}}{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}<div class=pagesize>Per page: {{range .SizeLinks}}<a class="{{if eq .Value $.PageSize}}active{{end}}" href="{{.URL}}">{{.Value}}</a>{{end}}</div></div><table><thead><tr><th><a href="{{index .SortURLs "status"}}">Status</a></th><th><a href="{{index .SortURLs "value"}}">Value</a></th><th><a href="{{index .SortURLs "name"}}">Torrent</a></th><th><a href="{{index .SortURLs "media"}}">Media</a></th><th><a href="{{index .SortURLs "state"}}">State</a></th><th><a href="{{index .SortURLs "size"}}">Size</a></th><th><a href="{{index .SortURLs "reclaimable"}}">Reclaimable</a></th><th><a href="{{index .SortURLs "ratio"}}">Ratio</a></th><th><a href="{{index .SortURLs "upload"}}">Upload</a></th><th><a href="{{index .SortURLs "seeds"}}">Seeds</a></th><th><a href="{{index .SortURLs "leechers"}}">Leechers</a></th><th><a href="{{index .SortURLs "activity"}}">Last activity</a></th><th></th></tr></thead><tbody>{{range .Torrents}}<tr><td><span class="status {{if eq .AssociationStatus "ASSOCIATED"}}associated{{else if eq .AssociationStatus "SUPERSEDED"}}superseded{{else if eq .AssociationStatus "ORPHANED"}}orphaned{{else}}unassociated{{end}}">{{.AssociationStatus}}</span></td><td><strong>{{printf "%.1f" .Value}}</strong></td><td><a href="/torrents/{{.Hash}}"><strong>{{.Name}}</strong></a></td><td>{{if .MediaItems}}{{range $i,$m:=.MediaItems}}{{if $i}}<br>{{end}}<a href="/library/{{$m.Type}}/{{$m.SourceID}}">{{$m.Title}} {{if $m.Year}}({{$m.Year}}){{end}}</a>{{end}}{{else if .FormerMediaItems}}{{range $i,$m:=.FormerMediaItems}}{{if $i}}<br>{{end}}<a href="/library/{{$m.Type}}/{{$m.SourceID}}">{{$m.Title}} {{if $m.Year}}({{$m.Year}}){{end}}</a> <span class=muted>(historical)</span>{{end}}{{else}}—{{end}}</td><td>{{.State}} · {{pct .Progress}}</td><td>{{human .SizeBytes}}</td><td>{{if .ReclaimableKnown}}{{human .ReclaimableBytes}}{{else}}—{{end}}</td><td>{{printf "%.2f" .Ratio}}</td><td>{{rate .UploadSpeed}}</td><td>{{.SeedsSwarm}}</td><td>{{.LeechersSwarm}}</td><td>{{unixTime .LastActivity}}</td><td><button class=trash title="Remove" data-removal-url="/removal/torrent?hash={{.Hash}}">🗑</button></td></tr>{{else}}<tr><td colspan=13 class=muted>No torrents match these filters.</td></tr>{{end}}</tbody></table><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}</div><p class=muted></p></div></body></html>`

const unclaimedHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Unclaimed files · {{appName}}</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.muted{color:#aaa}.warn{color:#ffcf66}.bad{color:#ff8f8f}.mono{font-family:ui-monospace,monospace}.summary-line{margin:0 0 14px;color:#aaa}.toolbar{display:flex;align-items:end;gap:12px;flex-wrap:wrap;margin:12px 0 8px}.filters{display:flex;align-items:end;gap:10px;flex-wrap:wrap}.filters label{display:flex;flex-direction:column;gap:4px;font-size:13px;color:#bbb}.filters input[type=search]{padding:7px 9px;background:#161616;color:#eee;border:1px solid #444;border-radius:6px;min-width:320px}.toolbar .count{color:#aaa;padding-bottom:7px}.scan{margin-left:auto;padding:8px 11px}.pager{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:14px 0}.pager a,.pager span{padding:6px 9px;border:1px solid #333;border-radius:6px;text-decoration:none}.current{background:#2b2b2b;font-weight:700}.disabled{color:#666}.pagesize{margin-left:auto;display:flex;gap:6px;align-items:center}.active{font-weight:800;color:#fff!important;border-color:#777!important}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:9px;border-bottom:1px solid #333;vertical-align:top;font-size:14px}th a{color:#eee;text-decoration:none}.path{max-width:720px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.remove-wrap{display:inline-block;cursor:not-allowed}.remove-wrap button:disabled{cursor:not-allowed;opacity:.45}</style></head><body>{{chrome ""}}<h1 class=page-title>Unclaimed files</h1>{{if .ScanErr}}<p class=warn>Scan unavailable: {{.ScanErr}}</p>{{end}}<div class=summary-line><b>{{.AllItems}}</b> files · <b>{{human .TotalBytes}}</b>{{if gt .ReclaimableBytes 0}} · <b>{{human .ReclaimableBytes}}</b> reclaimable{{end}}{{if gt .SharedBytes 0}} · {{human .SharedBytes}} shared through hardlinks{{end}}</div><div class=toolbar><form class=filters data-auto-filter method=get action="/downloads/unclaimed"><label>Search<input type=search name=q value="{{.Query}}" placeholder="Path or filename…"></label><input type=hidden name=sort value="{{.Sort}}"><input type=hidden name=order value="{{.Order}}"><input type=hidden name=page_size value="{{.PageSize}}">{{if .Query}}<a data-filter-clear href="/downloads/unclaimed">Clear</a>{{else}}<a data-filter-clear hidden href="/downloads/unclaimed">Clear</a>{{end}}</form><span class=count data-filter-count><b>{{.TotalItems}}</b>{{if ne .TotalItems .AllItems}} of {{.AllItems}}{{end}} files</span><button class=scan type=button data-unclaimed-scan>Scan for unclaimed files</button></div><div data-filter-results><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{range .PageLinks}}{{if eq .Value $.Page}}<span class=current>{{.Value}}</span>{{else}}<a href="{{.URL}}">{{.Value}}</a>{{end}}{{end}}{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}<div class=pagesize>Per page: {{range .SizeLinks}}<a class="{{if eq .Value $.PageSize}}active{{end}}" href="{{.URL}}">{{.Value}}</a>{{end}}</div></div><form id=unclaimedRemove data-removal-launch method=get action="/removal/unclaimed"><table><thead><tr><th><input id=unclaimedAll type=checkbox title="Select all on this page"></th><th><a href="{{index .SortURLs "path"}}">Path</a></th><th><a href="{{index .SortURLs "size"}}">Size</a></th><th><a href="{{index .SortURLs "reclaimable"}}">Reclaimable</a></th><th><a href="{{index .SortURLs "links"}}">Links</a></th><th><a href="{{index .SortURLs "modified"}}">Modified</a></th><th></th></tr></thead><tbody>{{range $gi,$f := .Files}}<tr><td><input class=unclaimedPick type=checkbox data-unclaimed-group="{{$gi}}">{{range $f.Paths}}<input type=hidden class=unclaimedGroupPath data-unclaimed-group="{{$gi}}" name=path value="{{.Path}}" disabled>{{end}}</td><td>{{range $f.Paths}}<div class="mono path" title="{{.Path}}">{{shortPath .Path}}</div>{{end}}</td><td>{{human $f.SizeBytes}}</td><td>{{if $f.ReclaimableKnown}}{{human $f.ReclaimableBytes}}{{else}}—{{end}}</td><td>{{if gt (len $f.Paths) 1}}hardlinked · {{len $f.Paths}} paths to the same physical file{{else if gt $f.Links 1}}hardlinked{{else}}—{{end}}{{if gt $f.MissingLinks 0}} · {{$f.MissingLinks}} hardlink{{if gt $f.MissingLinks 1}}s{{end}} missing{{end}}</td><td>{{fmtUpdated $f.ModifiedAt}}</td><td><button type=button class=trash title="Remove" data-removal-url="/removal/unclaimed?{{range $pi,$p := $f.Paths}}{{if $pi}}&{{end}}path={{urlquery $p.Path}}{{end}}">🗑</button></td></tr>{{else}}<tr><td colspan=7 class=muted>No unclaimed files found.</td></tr>{{end}}</tbody></table><div style="margin:12px 0"><span class=remove-wrap title="No files selected"><button id=unclaimedRemoveButton type=submit disabled>Remove</button></span></div></form><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}</div></div></body></html>`

const torrentDetailHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Torrent.Name}} · Torrent · {{appName}}</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.top{display:flex;gap:14px;align-items:center;flex-wrap:wrap}.card{background:#1b1b1b;padding:16px;border-radius:10px;margin-top:18px}.muted{color:#aaa}.warn{color:#ffcf66}.bad{color:#ff8f8f}.mono{font-family:ui-monospace,monospace;overflow-wrap:anywhere}.status{font-weight:800;letter-spacing:.08em}.kv{display:grid;grid-template-columns:max-content 1fr;gap:8px 14px}.kv div:nth-child(odd){color:#aaa}</style></head><body>{{chrome "torrents"}}<div style="display:flex;align-items:center;gap:12px"><h1 class=page-title style="flex:1">{{.Torrent.Name}}</h1><button class=trash title="Remove" data-removal-url="/removal/torrent?hash={{.Torrent.Hash}}">🗑</button></div><div style="margin:0 24px">{{if .Refreshing}}<span class=warn>Refreshing…</span>{{end}}{{if .LastErr}}<span class=bad>{{.LastErr}}</span>{{end}}</div>{{if .DetailErr}}<p class=warn>Some torrent details are unavailable.</p>{{end}}<p><span class=status>{{.Torrent.AssociationStatus}}</span> · {{.Torrent.Client}} · {{.Torrent.State}} · {{pct .Torrent.Progress}}</p><div class=card><strong>Value · {{printf "%.1f" .Torrent.Value}}</strong>{{if .Torrent.ValueReasons}}<div class=kv style="margin-top:10px">{{range .Torrent.ValueReasons}}<div>{{.Label}}</div><div>{{.Value}} · {{printf "%+.1f" .Points}}</div>{{end}}</div>{{else}}<p class=muted>No current swarm-value signals.</p>{{end}}</div>{{if or .Torrent.ReclaimableKnown .Torrent.StorageError}}<div class=card><strong>Storage</strong><div class=kv style="margin-top:10px"><div>Reclaimable</div><div>{{if .Torrent.ReclaimableKnown}}{{human .Torrent.ReclaimableBytes}}{{else}}Unknown{{end}}</div>{{if and .Torrent.ReclaimableKnown (gt .Torrent.SharedBytes 0)}}<div>Hardlinked</div><div>{{human .Torrent.SharedBytes}}</div>{{end}}{{if .Torrent.StorageError}}<div>Status</div><div class=bad>Storage information unavailable</div>{{end}}</div></div>{{end}}{{if .Torrent.MediaItems}}<div class=card><strong>Current media{{if gt (len .Torrent.MediaItems) 1}} items{{end}}</strong><br>{{range .Torrent.MediaItems}}<a href="/library/{{.Type}}/{{.SourceID}}">{{.Title}} {{if .Year}}({{.Year}}){{end}}</a><br>{{end}}</div>{{else if .Torrent.FormerMediaItems}}<div class=card><strong>Historical media relationship</strong><br>{{range .Torrent.FormerMediaItems}}<a href="/library/{{.Type}}/{{.SourceID}}">{{.Title}} {{if .Year}}({{.Year}}){{end}}</a><br>{{end}}{{if .Torrent.SupersededByHash}}<span class=muted>Superseded by torrent hash <span class=mono>{{.Torrent.SupersededByHash}}</span></span>{{end}}</div>{{end}}{{if .FilesErr}}<div class=card><strong>Files</strong><p class=warn>Files unavailable: {{.FilesErr}}</p></div>{{else if not .FilesUpdated.IsZero}}<div class=card><strong>Files {{if .FileCount}}({{.FileCount}}){{end}}</strong>{{range .Files}}<div style="padding:8px 0;border-top:1px solid #333"><div class=mono title="{{.File.Path}}">{{shortPath .File.Path}}</div>{{if and .File.Exists .File.IdentityKnown (gt .File.Links 1)}}{{range .SharedWith}}<div class=mono title="{{.Path}}">{{shortPath .Path}}</div>{{end}}{{end}}<span class=muted>{{human .File.SizeBytes}}{{if .File.Exists}}{{if .File.IdentityKnown}}{{if gt .File.Links 1}} · <strong>hardlinked · {{.File.Links}} paths / 1 physical file</strong>{{end}}{{end}}{{else}} · missing{{end}}</span></div>{{else}}<p class=muted>No files.</p>{{end}}{{if gt .FileCount (len .Files)}}<p class=muted>Showing first {{len .Files}} of {{.FileCount}} files.</p>{{end}}<p class=muted>{{if .RemoveTorrent.Known}}Removing this torrent frees <strong>{{human .RemoveTorrent.ReclaimableBytes}}</strong>.{{end}}</p><p class=muted></p></div>{{end}}<details class=card><summary>More details</summary><div class=kv style="margin-top:12px"><div>Hash</div><div class=mono>{{.Torrent.Hash}}</div>{{if .Torrent.Tracker}}<div>Tracker</div><div class=mono>{{.Torrent.Tracker}}</div>{{end}}{{if .Torrent.Category}}<div>Category</div><div>{{.Torrent.Category}}</div>{{end}}{{if .Torrent.Tags}}<div>Tags</div><div>{{.Torrent.Tags}}</div>{{end}}<div>Ratio</div><div>{{printf "%.2f" .Torrent.Ratio}}</div><div>Seeds</div><div>{{.Torrent.SeedsSwarm}}</div><div>Leechers</div><div>{{.Torrent.LeechersSwarm}}</div><div>Upload</div><div>{{rate .Torrent.UploadSpeed}}</div><div>Download</div><div>{{rate .Torrent.DownloadSpeed}}</div><div>Added</div><div>{{unixTime .Torrent.AddedOn}}</div><div>Last activity</div><div>{{unixTime .Torrent.LastActivity}}</div>{{if .Torrent.SavePath}}<div>Save path</div><div class=mono>{{.Torrent.SavePath}}</div>{{end}}{{if .Torrent.ContentPath}}<div>Content path</div><div class=mono>{{.Torrent.ContentPath}}</div>{{end}}</div></details><p class=muted></p></body></html>`

const tasksHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Tasks · {{appName}}</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.card{background:#1b1b1b;padding:16px;border-radius:10px;margin:16px 0}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:10px;border-bottom:1px solid #333;vertical-align:top}.muted{color:#aaa}.good{color:#8fd99c}.bad{color:#ff8f8f}.warn{color:#ffcf66}button{padding:7px 11px}</style></head><body>{{chrome "tasks"}}<h1 class=page-title>Tasks</h1><table><thead><tr><th>Task</th><th>Schedule</th><th>Last run</th><th>Duration</th><th>Next run</th><th>Status</th><th></th></tr></thead><tbody>{{range .Tasks}}<tr><td><b>{{.Name}}</b><br><span class=muted>{{.Description}}</span></td><td>Every {{durationGo .Interval}}</td><td>{{fmtUpdated .LastFinished}}</td><td>{{if .LastFinished.IsZero}}—{{else}}{{durationGo .LastDuration}}{{end}}</td><td>{{fmtUpdated .NextRun}}</td><td>{{if .Running}}<span class=warn>Running…</span>{{else if .LastError}}<span class=bad>{{.LastError}}</span>{{else if .LastFinished.IsZero}}<span class=muted>Not run yet</span>{{else}}<span class=good>OK</span>{{end}}</td><td><form method=post action="/tasks/run"><input type=hidden name=id value="{{.ID}}"><button{{if .Running}} disabled{{end}}>Run now</button></form></td></tr>{{end}}</tbody></table></body></html>`

const removalHTML = `<div class="removal-overlay"><main class="removal-dialog"><div class=top><div><h2 style="margin:0">{{if eq (printf "%s" .Plan.Kind) "media"}}Remove media{{else if eq (printf "%s" .Plan.Kind) "torrent"}}Remove torrent{{else}}Remove files{{end}}</h2><div class=muted>{{.Plan.RequestedLabel}}</div></div>{{if .Plan.DryRun}}<span class=dry>DRY RUN</span>{{end}}</div>{{if .Plan.DryRun}}<p class=warn>Dry run — nothing will be removed.</p>{{end}}
<div class=summary><div><div class=big>Removing {{human .Plan.SelectedPathBytes}} of {{human .PotentialBytes}}</div></div></div>
{{range .Plan.Warnings}}<p class=bad>{{.}}</p>{{end}}
{{if or .ManagedFileCount .TorrentTarget .RelatedManaged .Related .RelatedUnclaimed}}<form data-removal-selection method=get action="{{if eq (printf "%s" .Plan.Kind) "media"}}/removal/media{{else if eq (printf "%s" .Plan.Kind) "torrent"}}/removal/torrent{{else}}/removal/unclaimed{{end}}"><input type=hidden name=selection value=1>{{if eq (printf "%s" .Plan.Kind) "media"}}<input type=hidden name=type value="{{.MediaType}}"><input type=hidden name=id value="{{.MediaID}}">{{else if eq (printf "%s" .Plan.Kind) "torrent"}}<input type=hidden name=hash value="{{.Hash}}">{{else}}{{range .UnclaimedPaths}}<input type=hidden name=path value="{{.}}">{{end}}{{end}}
{{if eq (printf "%s" .Plan.Kind) "media"}}{{if .ManagedFileCount}}<section class=optional data-managed-scope><h3>Media</h3>{{if eq .ManagedFileCount 1}}{{range .ManagedGroups}}{{range .Files}}<div class="row managed-file"><label><input data-managed-pick type=checkbox name=managed_file value="{{managedKey .Ref}}" {{if .Selected}}checked{{end}}> <strong class=managed-name title="{{$.Plan.RequestedLabel}}">{{$.Plan.RequestedLabel}}</strong><span class=managed-size>· {{human .File.SizeBytes}}</span></label></div>{{end}}{{end}}{{else}}<div class=row><label><input data-managed-all type=checkbox> <strong>{{.Plan.RequestedLabel}}</strong></label><div class=muted>{{.ManagedFileCount}} managed files</div></div><div class=managed-tree>{{range .ManagedGroups}}{{if and .Complete (ne .Label "Files") (gt (len .Files) 1)}}<details class="managed-group managed-season" data-managed-group><summary><label class=group-label><input data-managed-group-all type=checkbox> {{.Label}} · {{len .Files}} episodes · {{human .SizeBytes}}</label></summary><div>{{range .Files}}<div class="row managed-file"><label><input data-managed-pick type=checkbox name=managed_file value="{{managedKey .Ref}}" {{if .Selected}}checked{{end}}><span class=managed-name title="{{.Label}}">{{.Label}}</span><span class=managed-size>· {{human .File.SizeBytes}}</span></label></div>{{end}}</div></details>{{else}}<div class=managed-group data-managed-group><label class=group-label><input data-managed-group-all type=checkbox> {{.Label}}{{if gt .TotalFiles 0}} · {{len .Files}}/{{.TotalFiles}} episodes{{end}} · {{human .SizeBytes}}</label>{{range .Files}}<div class="row managed-file"><label><input data-managed-pick type=checkbox name=managed_file value="{{managedKey .Ref}}" {{if .Selected}}checked{{end}}><span class=managed-name title="{{.Label}}">{{.Label}}</span><span class=managed-size>· {{human .File.SizeBytes}}</span></label></div>{{end}}</div>{{end}}{{end}}</div>{{end}}</section>{{end}}{{else if .TorrentTarget}}<section class=optional><h3>Torrent</h3><div class=row><label><input data-target-pick type=checkbox name=target value=1 {{if .TorrentSelected}}checked{{end}}> <strong>{{.Plan.RequestedLabel}}</strong></label></div></section>{{end}}
{{if .RelatedManaged}}<section class=optional><h3>Managed files</h3>{{range .RelatedManaged}}<div class=managed-media data-managed-scope><label class=group-label><input data-managed-all type=checkbox> <strong>{{.Media.Title}}{{if .Media.Year}} ({{.Media.Year}}){{end}}</strong> <span class=muted>· {{.FileCount}} proven file{{if ne .FileCount 1}}s{{end}}</span></label>{{range .Groups}}{{if and .Complete (ne .Label "Files") (gt (len .Files) 1)}}<details class="managed-group managed-season" data-managed-group><summary><label class=group-label><input data-managed-group-all type=checkbox> {{.Label}} · {{len .Files}} episodes · {{human .SizeBytes}}</label></summary><div>{{range .Files}}<div class="row managed-file"><label><input data-managed-pick type=checkbox name=managed_file value="{{managedKey .Ref}}" {{if .Selected}}checked{{end}}><span class=managed-name title="{{.Label}}">{{.Label}}</span><span class=managed-size>· {{human .File.SizeBytes}}</span></label></div>{{end}}</div></details>{{else}}<div class=managed-group data-managed-group><label class=group-label><input data-managed-group-all type=checkbox> {{.Label}}{{if gt .TotalFiles 0}} · {{len .Files}}/{{.TotalFiles}} episodes{{end}} · {{human .SizeBytes}}</label>{{range .Files}}<div class="row managed-file"><label><input data-managed-pick type=checkbox name=managed_file value="{{managedKey .Ref}}" {{if .Selected}}checked{{end}}><span class=managed-name title="{{.Label}}">{{.Label}}</span><span class=managed-size>· {{human .File.SizeBytes}}</span></label></div>{{end}}</div>{{end}}{{end}}</div>{{end}}</section>{{end}}
{{if .Related}}<section class=optional data-linked-scope><h3>Associated torrents</h3><label><input data-select-all type=checkbox> Select all</label>{{range .Related}}<div class=row><label><input data-linked-pick type=checkbox name=torrent value="{{.Torrent.Hash}}" {{if .Selected}}checked{{end}}> <strong>{{.Torrent.Name}}</strong></label><div class=muted>Value {{printf "%.1f" .Torrent.Value}} · {{human .Torrent.SizeBytes}}{{if .Torrent.ReclaimableKnown}} · {{human .Torrent.ReclaimableBytes}} freed if fully removed{{end}}</div></div>{{end}}</section>{{end}}{{if .RelatedUnclaimed}}<section class=optional data-unclaimed-scope><h3>Associated unclaimed files</h3><label><input data-unclaimed-all type=checkbox> Select all</label>{{range .RelatedUnclaimed}}<div class=row><label><input data-unclaimed-pick type=checkbox name=unclaimed_path value="{{.Path}}" {{if .Selected}}checked{{end}}> <strong class=mono title="{{.Path}}">{{shortPath .Path}}</strong> <span class=muted>· {{human .SizeBytes}}</span></label></div>{{end}}</section>{{end}}</form>{{end}}
<details><summary>Files and storage ({{len .FileGroups}})</summary>{{range .FileGroups}}<div class=row><div>{{range .DisplayPaths}}<div class=mono title="{{.Path}}"><strong>{{.Label}}:</strong> {{shortPath .Text}}</div>{{end}}<div class=muted>{{if .Exists}}{{human .SizeBytes}}{{if and .IdentityKnown (gt .Links 1)}} · <span class=hardlink>hardlinked{{if gt .MissingLinks 0}} · <span class=bad>{{.MissingLinks}} hardlink{{if ne .MissingLinks 1}}s{{end}} missing</span>{{else}} · {{len .Paths}} paths to the same physical file{{end}}</span>{{end}}{{else}}missing{{end}}{{if .Error}} · {{.Error}}{{end}}</div></div></div>{{else}}<p class=muted>No files.</p>{{end}}</details>
<form data-removal-execute method=post action="/removal/execute"><input type=hidden name=kind value="{{.Plan.Kind}}">{{if eq (printf "%s" .Plan.Kind) "media"}}<input type=hidden name=media_type value="{{.MediaType}}"><input type=hidden name=media_id value="{{.MediaID}}">{{range .SelectedManaged}}<input type=hidden name=managed_file value="{{managedKey .}}">{{end}}{{range .Related}}{{if .Selected}}<input type=hidden name=torrent value="{{.Torrent.Hash}}">{{end}}{{end}}{{range .SelectedUnclaimed}}<input type=hidden name=unclaimed_path value="{{.}}">{{end}}{{else if eq (printf "%s" .Plan.Kind) "torrent"}}<input type=hidden name=hash value="{{.Hash}}">{{if .TorrentSelected}}<input type=hidden name=target value=1>{{end}}{{range .SelectedManaged}}<input type=hidden name=managed_file value="{{managedKey .}}">{{end}}{{range .SelectedUnclaimed}}<input type=hidden name=unclaimed_path value="{{.}}">{{end}}{{else}}{{range .UnclaimedPaths}}<input type=hidden name=path value="{{.}}">{{end}}{{range .Related}}{{if .Selected}}<input type=hidden name=torrent value="{{.Torrent.Hash}}">{{end}}{{end}}{{end}}{{if or .CanUnmonitorMovies .CanUnmonitorEpisodes}}<section class=owner-options><h3>After removal</h3>{{if .CanUnmonitorMovies}}<label><input type=checkbox name=unmonitor_movies value=1> Unmonitor affected movies</label><br>{{end}}{{if .CanUnmonitorEpisodes}}<label><input type=checkbox name=unmonitor_episodes value=1> Unmonitor affected episodes</label>{{end}}</section>{{end}}<div class=actions><button type=button data-removal-cancel>Cancel</button>{{if eq .SelectedActions 0}}<span class=disabled-tip title="Nothing selected"><button type=submit disabled>{{if .Plan.DryRun}}Simulate{{else}}Remove{{end}}</button></span>{{else}}<button class="{{if .Plan.DryRun}}simulate{{else}}danger{{end}}" type=submit>{{if .Plan.DryRun}}Simulate{{else}}Remove{{end}}</button>{{end}}</div></form></main></div>`
