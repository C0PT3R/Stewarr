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
	"sort"
	"spartarr/internal/cleanup"
	"spartarr/internal/inventory"
	"spartarr/internal/model"
	"spartarr/internal/storagecap"
	"spartarr/internal/store"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	inv              *inventory.Service
	homeTpl          *template.Template
	libraryTpl       *template.Template
	historyTpl       *template.Template
	profileTpl       *template.Template
	torrentTpl       *template.Template
	torrentDetailTpl *template.Template
}

func New(inv *inventory.Service) (*Server, error) {
	f := template.FuncMap{"human": func(n int64) string {
		if n < 0 {
			n = 0
		}
		return cleanup.Human(uint64(n))
	}, "humanU": func(n uint64) string { return cleanup.Human(n) }, "rate": func(n int64) string { return cleanup.Human(uint64(max64(n, 0))) + "/s" }, "duration": humanDuration, "unixTime": unixTime, "pct": func(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }, "fmtTime": func(t *time.Time) string {
		if t == nil {
			return "Never"
		}
		return t.Local().Format("2006-01-02")
	}, "join": strings.Join, "add": func(a, b int) int { return a + b }, "fmtUpdated": func(t time.Time) string {
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
	return &Server{inv: inv, homeTpl: homeTpl, libraryTpl: libraryTpl, historyTpl: historyTpl, profileTpl: profileTpl, torrentTpl: torrentTpl, torrentDetailTpl: torrentDetailTpl}, nil
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", s.home)
	m.HandleFunc("/library", s.library)
	m.HandleFunc("/library/", s.media)
	m.HandleFunc("/media/", s.media)
	m.HandleFunc("/torrents", s.torrents)
	m.HandleFunc("/torrents/", s.torrentDetail)
	m.HandleFunc("/history", s.history)
	m.HandleFunc("/api/media", s.apiMedia)
	m.HandleFunc("/api/torrents", s.apiTorrents)
	m.HandleFunc("/api/refresh", s.refresh)
	m.HandleFunc("/api/plan", s.plan)
	m.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	return m
}

type mediaRow struct {
	Media model.Media
	Rank  int
}

type navLink struct {
	Value int
	URL   string
}

type libraryData struct {
	Rows       []mediaRow
	Updated    time.Time
	LastErr    error
	Plan       cleanup.Plan
	Refreshing bool
	PlanErr    error
	TotalItems int
	Page       int
	PageSize   int
	TotalPages int
	HasPrev    bool
	HasNext    bool
	PrevURL    string
	NextURL    string
	PageLinks  []navLink
	Sort       string
	Order      string
	SortURLs   map[string]string
	SizeLinks  []navLink
}

type homeData struct {
	Plan                cleanup.Plan
	PlanErr             error
	Updated             time.Time
	LastErr             error
	Refreshing          bool
	TotalMedia          int
	Movies              int
	Series              int
	LibraryBytes        int64
	TotalTorrents       int
	Associated          int
	Superseded          int
	Orphaned            int
	Unassociated        int
	ObsoleteReclaimable int64
	ObsoleteKnown       int
	Services            []inventory.ServiceStatus
	Capabilities        storagecap.Capabilities
	Stats               store.CleanupStats
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	items, updated, last := s.inv.Snapshot()
	p, planErr := cleanup.Build(s.inv.Config().Storage.Path, s.inv.Config().Storage.TargetUsagePercent, s.inv.Config().Storage.CriticalUsagePercent, items)
	d := homeData{Plan: p, PlanErr: planErr, Updated: updated, LastErr: last, Refreshing: s.inv.IsRefreshing(), TotalMedia: len(items), Services: s.inv.StatusSnapshot(), Capabilities: storagecap.Inspect(s.inv.Config().Storage.Path)}
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
	if db := s.inv.Store(); db != nil {
		d.Stats, _ = db.CleanupStatistics()
	}
	if e := renderTemplate(w, s.homeTpl, d); e != nil {
		log.Printf("render home: %v", e)
	}
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/library" {
		http.NotFound(w, r)
		return
	}
	items, updated, last := s.inv.Snapshot()
	p, planErr := cleanup.Build(s.inv.Config().Storage.Path, s.inv.Config().Storage.TargetUsagePercent, s.inv.Config().Storage.CriticalUsagePercent, items)

	pageSize := allowedPageSize(queryInt(r, "page_size", 50))
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	sortKey := r.URL.Query().Get("sort")
	if !validMediaSort(sortKey) {
		sortKey = "rank"
	}
	order := strings.ToLower(r.URL.Query().Get("order"))
	if order != "asc" && order != "desc" {
		order = "asc"
	}

	// Preserve the canonical Spartarr rank (weakest first) even when the user
	// temporarily sorts the table by another field.
	rank := make(map[string]int, len(items))
	for i := range items {
		rank[mediaKey(items[i])] = i + 1
	}
	sortMediaItems(items, sortKey, order, rank)

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
		rows = append(rows, mediaRow{Media: m, Rank: rank[mediaKey(m)]})
	}

	mkURL := func(pg int, size int, sk, ord string) string {
		q := url.Values{}
		q.Set("page", strconv.Itoa(pg))
		q.Set("page_size", strconv.Itoa(size))
		q.Set("sort", sk)
		q.Set("order", ord)
		return "/library?" + q.Encode()
	}
	sortURLs := map[string]string{}
	for _, key := range []string{"rank", "strength", "title", "type", "rating", "votes", "views", "lastwatched", "requested", "size", "torrents"} {
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

	data := libraryData{Rows: rows, Updated: updated, LastErr: last, Plan: p, Refreshing: s.inv.IsRefreshing(), PlanErr: planErr,
		TotalItems: total, Page: page, PageSize: pageSize, TotalPages: totalPages, HasPrev: page > 1, HasNext: page < totalPages,
		Sort: sortKey, Order: order, SortURLs: sortURLs, SizeLinks: sizeLinks, PageLinks: pageLinks}
	if data.HasPrev {
		data.PrevURL = mkURL(page-1, pageSize, sortKey, order)
	}
	if data.HasNext {
		data.NextURL = mkURL(page+1, pageSize, sortKey, order)
	}
	if e := renderTemplate(w, s.libraryTpl, data); e != nil {
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
	case "rank", "strength", "title", "type", "rating", "votes", "views", "lastwatched", "requested", "size", "torrents":
		return true
	}
	return false
}
func defaultSortOrder(key string) string {
	switch key {
	case "rank", "strength", "title", "type":
		return "asc"
	}
	return "desc"
}
func mediaKey(m model.Media) string { return fmt.Sprintf("%s:%d", m.Type, m.SourceID) }
func sortMediaItems(items []model.Media, key, order string, rank map[string]int) {
	dir := 1
	if order == "desc" {
		dir = -1
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		cmp := 0
		switch key {
		case "rank":
			cmp = rank[mediaKey(a)] - rank[mediaKey(b)]
		case "strength":
			if a.Strength < b.Strength {
				cmp = -1
			} else if a.Strength > b.Strength {
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
			cmp = rank[mediaKey(a)] - rank[mediaKey(b)]
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
		data := struct {
			Media      model.Media
			Rank       int
			Total      int
			Updated    time.Time
			LastErr    error
			Refreshing bool
		}{m, i + 1, len(items), updated, last, s.inv.IsRefreshing()}
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
	case "status", "name", "media", "state", "size", "reclaimable", "ratio", "upload", "seeds", "leechers", "activity":
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

func (s *Server) torrents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/torrents" {
		http.NotFound(w, r)
		return
	}
	_, updated, last := s.inv.Snapshot()
	all := s.inv.TorrentSnapshot()
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
		return "/torrents?" + q.Encode()
	}
	sortURLs := map[string]string{}
	for _, k := range []string{"status", "name", "media", "state", "size", "reclaimable", "ratio", "upload", "seeds", "leechers", "activity"} {
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
	d := torrentData{Torrents: all[from:to], Updated: updated, LastErr: last, Refreshing: s.inv.IsRefreshing(), Associated: counts["ASSOCIATED"], Superseded: counts["SUPERSEDED"], Orphaned: counts["ORPHANED"], Unassociated: counts["UNASSOCIATED"], ObsoleteKnown: obsoleteKnown, ObsoleteReclaimable: obsoleteReclaimable, TotalItems: total, Page: page, PageSize: pageSize, TotalPages: pages, HasPrev: page > 1, HasNext: page < pages, PageLinks: links, SizeLinks: sizes, Sort: sortKey, Order: order, SortURLs: sortURLs}
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

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/history" {
		http.NotFound(w, r)
		return
	}
	var stats store.CleanupStats
	var runs []store.CleanupRun
	if db := s.inv.Store(); db != nil {
		stats, _ = db.CleanupStatistics()
		runs, _ = db.CleanupRuns(100)
	}
	d := struct {
		Stats store.CleanupStats
		Runs  []store.CleanupRun
	}{stats, runs}
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
	for _, torrent := range s.inv.TorrentSnapshot() {
		if !strings.EqualFold(torrent.Hash, hash) {
			continue
		}
		torrent.AssociationStatus = normalizeTorrentStatus(torrent.AssociationStatus)
		data := struct {
			Torrent    model.Torrent
			Updated    time.Time
			LastErr    error
			Refreshing bool
		}{torrent, updated, last, s.inv.IsRefreshing()}
		if e := renderTemplate(w, s.torrentDetailTpl, data); e != nil {
			log.Printf("render torrent detail: %v", e)
		}
		return
	}
	http.NotFound(w, r)
}

func (s *Server) apiMedia(w http.ResponseWriter, r *http.Request) {
	x, u, e := s.inv.Snapshot()
	writeJSON(w, map[string]any{"items": x, "updated": u, "error": errString(e), "refreshing": s.inv.IsRefreshing()})
}
func (s *Server) apiTorrents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": s.inv.TorrentSnapshot(), "refreshing": s.inv.IsRefreshing()})
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	go func() {
		if e := s.inv.Refresh(context.Background()); e != nil {
			log.Printf("refresh: %v", e)
		}
	}()
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
	log.Printf("Spartarr listening on %s", addr)
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
func unixTime(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}

const homeHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Spartarr</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.nav h1{margin-right:12px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px;margin-top:20px}.card{background:#1b1b1b;padding:16px;border-radius:10px}.card h2{margin:0 0 12px;font-size:18px}.big{font-size:32px;font-weight:800}.muted{color:#aaa}.good{color:#8fd99c}.warn{color:#ffcf66}.bad{color:#ff8f8f}.kv{display:grid;grid-template-columns:1fr auto;gap:7px 12px}.mono{font-family:ui-monospace,monospace}.tagline{margin:10px 0 0}.full{grid-column:1/-1}button{padding:8px 12px}</style></head><body><div class=nav><h1>Spartarr</h1><a href="/">Home</a><a href="/library">Library</a><a href="/torrents">Torrents</a><a href="/history">History</a><form action="/api/refresh" method=post><button{{if .Refreshing}} disabled{{end}}>{{if .Refreshing}}Refreshing…{{else}}Refresh{{end}}</button></form></div><p class=tagline><strong>Here in Spartarr, your media compete to keep the disk space they claim as their own. Only the strongest shall survive.</strong></p>{{if .LastErr}}<p class=bad>{{.LastErr}}</p>{{end}}<div class=grid><section class=card><h2>Storage</h2>{{if .Plan.Available}}<div class=big>{{printf "%.2f" .Plan.UsagePercent}}%</div><div class=muted>Target {{printf "%.1f" $.Plan.TargetUsagePercent}}</div>{{if gt .Plan.NeedBytes 0}}<p class=bad><b>Cleanup active.</b><br>{{.Plan.Message}}<br>{{len .Plan.Selected}} media currently selected · {{humanU .Plan.SelectedBytes}} planned.</p>{{else}}<p class=good><b>Cleanup inactive.</b><br>{{.Plan.Message}}</p>{{end}}{{else}}<div class=big bad>UNAVAILABLE</div><p>{{.Plan.Message}}</p>{{if .PlanErr}}<span class=bad>{{.PlanErr}}</span>{{end}}{{end}}</section><section class=card><h2>Library</h2><div class=big>{{.TotalMedia}}</div><div class=kv><span>Movies</span><b>{{.Movies}}</b><span>Series</span><b>{{.Series}}</b><span>Library size</span><b>{{human .LibraryBytes}}</b></div><p><a href="/library">Browse Library →</a></p></section><section class=card><h2>Torrents</h2><div class=big>{{.TotalTorrents}}</div><div class=kv><span>Associated</span><b>{{.Associated}}</b><span>Superseded</span><b>{{.Superseded}}</b><span>Orphaned</span><b>{{.Orphaned}}</b><span>Unassociated</span><b>{{.Unassociated}}</b>{{if gt .ObsoleteKnown 0}}<span>Known reclaimable</span><b>{{human .ObsoleteReclaimable}}</b>{{end}}</div><p><a href="/torrents">Browse Torrents →</a></p></section><section class=card><h2>Lifetime statistics</h2><div class=big>{{human .Stats.ReclaimedBytes}}</div><div class=muted>actual space reclaimed</div><div class=kv style="margin-top:12px"><span>Cleanup runs</span><b>{{.Stats.Runs}}</b><span>Media removed</span><b>{{.Stats.MediaRemoved}}</b><span>Torrents removed</span><b>{{.Stats.TorrentsRemoved}}</b><span>Library bytes removed</span><b>{{human .Stats.MediaBytes}}</b><span>Last 30 days</span><b>{{human .Stats.Last30Bytes}}</b></div><p><a href="/history">Cleanup history →</a></p></section><section class=card><h2>Services</h2><div class=kv>{{range .Services}}<span>{{.Name}}</span><b class="{{if not .Configured}}muted{{else if .OK}}good{{else}}bad{{end}}">{{if not .Configured}}Not configured{{else if .OK}}Connected{{else}}Unavailable{{end}}</b>{{end}}</div></section><section class=card><h2>Storage capabilities</h2><div class=kv><span>Path visible</span><b>{{if .Capabilities.Visible}}Yes{{else}}No{{end}}</b><span>Filesystem</span><b>{{if .Capabilities.Filesystem}}{{.Capabilities.Filesystem}}{{else}}Unknown{{end}}</b><span>File identity</span><b>{{if .Capabilities.FileIdentity}}Supported{{else}}Unavailable{{end}}</b><span>Hardlink detection</span><b>{{if .Capabilities.HardlinkDetection}}Supported{{else}}Unavailable{{end}}</b><span>Shared extents</span><b>{{if .Capabilities.SharedExtentDetection}}Supported{{else}}Not available{{end}}</b><span>Exact reclaim estimate</span><b>{{if .Capabilities.ExactReclaimEstimate}}Supported{{else}}Not yet available{{end}}</b></div>{{if .Capabilities.Error}}<p class=bad>{{.Capabilities.Error}}</p>{{end}}</section><section class="card full"><b>Last inventory refresh:</b> {{fmtUpdated .Updated}}{{if .Refreshing}} · <span class=warn>refresh in progress</span>{{end}}</section></div></body></html>`

const libraryHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Library · Spartarr</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.card{background:#1b1b1b;padding:14px;border-radius:10px;margin-top:14px}.warn{color:#ffcf66}.bad{color:#ff8f8f}table{border-collapse:collapse;width:100%;margin-top:18px}th,td{padding:8px;border-bottom:1px solid #333;text-align:left;font-size:14px}th{position:sticky;top:0;background:#111;white-space:nowrap}th a{color:#eee;text-decoration:none}.strength{font-weight:700}.pager{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:14px 0}.pager a,.pager span{padding:6px 9px;border:1px solid #333;border-radius:6px;text-decoration:none}.current{background:#2b2b2b;font-weight:700}.disabled{color:#666}.pagesize{margin-left:auto;display:flex;gap:6px;align-items:center}.active{font-weight:800;color:#fff!important;border-color:#777!important}</style></head><body><div class=nav><h1>Library</h1><a href="/">Home</a><a href="/library">Library</a><a href="/torrents">Torrents</a><a href="/history">History</a>{{if .Refreshing}}<span class=warn>Refresh in progress…</span>{{end}}{{if .LastErr}}<span class=bad>{{.LastErr}}</span>{{end}}</div><div class=card><b>{{.TotalItems}}</b> media · weakest first by default · refreshed {{fmtUpdated .Updated}}</div><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{range .PageLinks}}{{if eq .Value $.Page}}<span class=current>{{.Value}}</span>{{else}}<a href="{{.URL}}">{{.Value}}</a>{{end}}{{end}}{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}<div class=pagesize>Per page: {{range .SizeLinks}}<a class="{{if eq .Value $.PageSize}}active{{end}}" href="{{.URL}}">{{.Value}}</a>{{end}}</div></div><table><thead><tr><th><a href="{{index .SortURLs "rank"}}">#</a></th><th><a href="{{index .SortURLs "strength"}}">Strength</a></th><th><a href="{{index .SortURLs "title"}}">Media</a></th><th><a href="{{index .SortURLs "type"}}">Type</a></th><th><a href="{{index .SortURLs "rating"}}">Rating</a></th><th><a href="{{index .SortURLs "votes"}}">Votes</a></th><th><a href="{{index .SortURLs "views"}}">Views</a></th><th><a href="{{index .SortURLs "lastwatched"}}">Last watched</a></th><th><a href="{{index .SortURLs "requested"}}">Requested</a></th><th><a href="{{index .SortURLs "size"}}">Size</a></th><th><a href="{{index .SortURLs "torrents"}}">Torrents</a></th></tr></thead><tbody>{{range .Rows}}{{$m:=.Media}}<tr><td>{{.Rank}}</td><td class=strength>{{printf "%.1f" $m.Strength}}</td><td><a href="/library/{{$m.Type}}/{{$m.SourceID}}">{{$m.Title}} {{if $m.Year}}({{$m.Year}}){{end}}</a></td><td>{{$m.Type}}</td><td>{{if gt $m.Rating 0.0}}{{printf "%.1f" $m.Rating}}{{else}}—{{end}}</td><td>{{$m.VoteCount}}</td><td>{{$m.Views}}</td><td>{{fmtTime $m.LastWatched}}</td><td>{{if $m.Requested}}yes{{else}}no{{end}}</td><td>{{human $m.SizeBytes}}</td><td>{{len $m.Torrents}}</td></tr>{{end}}</tbody></table><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}</div></body></html>`

const historyHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>History · Spartarr</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center}.card{background:#1b1b1b;padding:14px;border-radius:10px;margin:16px 0}table{border-collapse:collapse;width:100%}th,td{padding:8px;border-bottom:1px solid #333;text-align:left}.muted{color:#aaa}</style></head><body><div class=nav><h1>History</h1><a href="/">Home</a><a href="/library">Library</a><a href="/torrents">Torrents</a></div><div class=card><b>{{human .Stats.ReclaimedBytes}}</b> actual space reclaimed · {{.Stats.MediaRemoved}} media removed · {{.Stats.TorrentsRemoved}} torrents removed · {{.Stats.Runs}} completed cleanup runs</div>{{if .Runs}}<table><thead><tr><th>Completed</th><th>Status</th><th>Storage</th><th>Media</th><th>Torrents</th><th>Library bytes</th><th>Reclaimed</th><th>Reason</th></tr></thead><tbody>{{range .Runs}}<tr><td>{{fmtUpdated .CompletedAt}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .UsageBefore}}% → {{printf "%.2f" .UsageAfter}}%</td><td>{{.MediaRemoved}}</td><td>{{.TorrentsRemoved}}</td><td>{{human .MediaBytes}}</td><td>{{human .ReclaimedBytes}}</td><td>{{.Reason}}</td></tr>{{end}}</tbody></table>{{else}}<p class=muted>No cleanup has been executed yet. Spartarr 0.1.4 remains non-destructive, so statistics stay at zero rather than counting previewed plans as reclaimed space.</p>{{end}}</body></html>`

const profileHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Media.Title}} · Spartarr</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee;max-width:1100px}a{color:#9cf}.muted{color:#aaa}.bad{color:#ff8f8f}.warn{color:#ffcf66}.hero{display:flex;gap:28px;align-items:flex-start;justify-content:space-between;flex-wrap:wrap;margin:18px 0}.strength{font-size:52px;font-weight:800;line-height:1}.status{font-size:14px;letter-spacing:.12em;font-weight:800}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:16px}.card{background:#1b1b1b;padding:16px;border-radius:10px}.card h2{font-size:17px;margin:0 0 12px}.kv{display:grid;grid-template-columns:max-content 1fr;gap:7px 14px}.kv div:nth-child(odd){color:#aaa}.reasons{width:100%;border-collapse:collapse}.reasons td{padding:7px 4px;border-bottom:1px solid #333}.reasons td:last-child{text-align:right;font-weight:700}.positive{color:#8fd99c}.mono{font-family:ui-monospace,monospace;overflow-wrap:anywhere}.top{display:flex;gap:12px;align-items:center;flex-wrap:wrap}</style></head><body><div class=top><a href="/library">← Library</a><a href="/">Home</a><a href="/torrents">Torrents</a>{{if .Refreshing}}<span class=warn>Inventory refresh in progress…</span>{{end}}{{if .LastErr}}<span class=bad>{{.LastErr}}</span>{{end}}</div><div class=hero><div><h1>{{.Media.Title}} {{if .Media.Year}}({{.Media.Year}}){{end}}</h1><div class=muted>{{.Media.Type}} · media #{{.Rank}} of {{.Total}} (weakest first)</div></div><div><div class=strength>{{printf "%.1f" .Media.Strength}}</div><div class=muted>Strength</div></div></div><div class=grid><section class=card><h2>Strength breakdown</h2><table class=reasons>{{if .Media.Reasons}}{{range .Media.Reasons}}<tr><td><strong>{{.Label}}</strong><br><span class=muted>{{.Value}}</span></td><td class=positive>{{printf "%+.1f" .Points}}</td></tr>{{end}}{{else}}<tr><td class=muted>No Strength contributions.</td></tr>{{end}}</table></section><section class=card><h2>Storage claim</h2><div class=kv><div>Size</div><div>{{human .Media.SizeBytes}}</div><div>Path</div><div class=mono>{{.Media.Path}}</div><div>Added</div><div>{{if .Media.AddedAt.IsZero}}Unknown{{else}}{{.Media.AddedAt.Local.Format "2006-01-02"}}{{end}}</div></div></section><section class=card><h2>Activity</h2><div class=kv><div>Views</div><div>{{.Media.Views}}</div><div>Unique viewers</div><div>{{.Media.UniqueViewers}}</div><div>Last watched</div><div>{{fmtTime .Media.LastWatched}}</div><div>Favorite</div><div>{{if .Media.Favorite}}yes{{else}}no{{end}}</div><div>Requested</div><div>{{if .Media.Requested}}yes{{else}}no{{end}}</div><div>Requested at</div><div>{{fmtTime .Media.RequestedAt}}</div></div></section><section class=card style="grid-column:1/-1"><h2>Torrents {{if .Media.Torrents}}({{len .Media.Torrents}}){{end}}</h2>{{if .Media.Torrents}}{{range .Media.Torrents}}<div style="border-top:1px solid #333;padding:12px 0;display:flex;justify-content:space-between;gap:18px;align-items:center;flex-wrap:wrap"><div><strong>{{.Client}} · {{.AssociationStatus}}</strong><br><span class=muted>{{.State}} · ratio {{printf "%.2f" .Ratio}} · {{.LeechersSwarm}} leechers · {{rate .UploadSpeed}} up · {{human .SizeBytes}}</span></div><a href="/torrents/{{.Hash}}">View torrent →</a></div>{{end}}{{else}}<div class=muted>No torrent is associated with this media.</div>{{end}}</section><section class=card><h2>Metadata</h2><div class=kv><div>Rating</div><div>{{if gt .Media.Rating 0.0}}{{printf "%.1f" .Media.Rating}} / 10{{else}}—{{end}}</div><div>Votes</div><div>{{.Media.VoteCount}}</div><div>Tags</div><div>{{if .Media.Tags}}{{join .Media.Tags ", "}}{{else}}—{{end}}</div><div>Source ID</div><div>{{.Media.SourceID}}</div><div>TMDB</div><div>{{if .Media.TMDBID}}{{.Media.TMDBID}}{{else}}—{{end}}</div><div>TVDB</div><div>{{if .Media.TVDBID}}{{.Media.TVDBID}}{{else}}—{{end}}</div><div>IMDB</div><div>{{if .Media.IMDBID}}{{.Media.IMDBID}}{{else}}—{{end}}</div></div></section></div><p class=muted>Inventory refreshed {{fmtUpdated .Updated}}.</p>{{if .Refreshing}}<script>setTimeout(()=>location.reload(),1500)</script>{{end}}</body></html>`

const torrentHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Torrents · Spartarr</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee}a{color:#9cf}.nav{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.card{background:#1b1b1b;padding:14px;border-radius:10px;margin:16px 0}.muted{color:#aaa}.warn{color:#ffcf66}.bad{color:#ff8f8f}.associated{color:#8fd99c}.superseded{color:#ff9f66}.orphaned{color:#ffcf66}.unassociated{color:#aaa}.mono{font-family:ui-monospace,monospace;overflow-wrap:anywhere}table{border-collapse:collapse;width:100%;margin-top:18px}th,td{padding:8px;border-bottom:1px solid #333;text-align:left;font-size:14px;vertical-align:top}th{position:sticky;top:0;background:#111;white-space:nowrap}th a{color:#eee;text-decoration:none}.status{font-weight:800}.pager{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:14px 0}.pager a,.pager span{padding:6px 9px;border:1px solid #333;border-radius:6px;text-decoration:none}.current{background:#2b2b2b;font-weight:700}.disabled{color:#666}.pagesize{margin-left:auto;display:flex;gap:6px;align-items:center}.active{font-weight:800;color:#fff!important;border-color:#777!important}</style></head><body><div class=nav><h1>Torrents</h1><a href="/">Home</a><a href="/library">Library</a><a href="/torrents">Torrents</a><a href="/history">History</a>{{if .Refreshing}}<span class=warn>Refresh in progress…</span>{{end}}{{if .LastErr}}<span class=bad>{{.LastErr}}</span>{{end}}</div><div class=card><b>{{.TotalItems}}</b> torrents · <span class=associated><b>{{.Associated}}</b> associated</span> · <span class=superseded><b>{{.Superseded}}</b> superseded</span> · <span class=orphaned><b>{{.Orphaned}}</b> orphaned</span> · <span class=unassociated><b>{{.Unassociated}}</b> unassociated</span>{{if gt .ObsoleteKnown 0}}<br><b>{{human .ObsoleteReclaimable}}</b> estimated reclaimable from {{.ObsoleteKnown}} inspected superseded/orphaned torrents{{end}}<br><span class=muted>Unassociated means Spartarr cannot prove a relationship to current media; it does not mean the torrent is safe to remove. Reclaimable estimates use visible filesystem link counts and do not account for snapshots, reflinks, or deduplication.</span></div><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{range .PageLinks}}{{if eq .Value $.Page}}<span class=current>{{.Value}}</span>{{else}}<a href="{{.URL}}">{{.Value}}</a>{{end}}{{end}}{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}<div class=pagesize>Per page: {{range .SizeLinks}}<a class="{{if eq .Value $.PageSize}}active{{end}}" href="{{.URL}}">{{.Value}}</a>{{end}}</div></div><table><thead><tr><th><a href="{{index .SortURLs "status"}}">Status</a></th><th><a href="{{index .SortURLs "name"}}">Torrent</a></th><th><a href="{{index .SortURLs "media"}}">Media</a></th><th><a href="{{index .SortURLs "state"}}">State</a></th><th><a href="{{index .SortURLs "size"}}">Size</a></th><th><a href="{{index .SortURLs "reclaimable"}}">Reclaimable</a></th><th><a href="{{index .SortURLs "ratio"}}">Ratio</a></th><th><a href="{{index .SortURLs "upload"}}">Upload</a></th><th><a href="{{index .SortURLs "seeds"}}">Seeds</a></th><th><a href="{{index .SortURLs "leechers"}}">Leechers</a></th><th><a href="{{index .SortURLs "activity"}}">Last activity</a></th></tr></thead><tbody>{{range .Torrents}}<tr><td><span class="status {{if eq .AssociationStatus "ASSOCIATED"}}associated{{else if eq .AssociationStatus "SUPERSEDED"}}superseded{{else if eq .AssociationStatus "ORPHANED"}}orphaned{{else}}unassociated{{end}}">{{.AssociationStatus}}</span></td><td><a href="/torrents/{{.Hash}}"><strong>{{.Name}}</strong></a><br><span class=mono>{{.Hash}}</span></td><td>{{if .MediaItems}}{{range $i,$m:=.MediaItems}}{{if $i}}<br>{{end}}<a href="/library/{{$m.Type}}/{{$m.SourceID}}">{{$m.Title}} {{if $m.Year}}({{$m.Year}}){{end}}</a>{{end}}{{else}}—{{end}}</td><td>{{.State}} · {{pct .Progress}}</td><td>{{human .SizeBytes}}</td><td>{{if .ReclaimableKnown}}{{human .ReclaimableBytes}}{{else}}—{{end}}</td><td>{{printf "%.2f" .Ratio}}</td><td>{{rate .UploadSpeed}}</td><td>{{.SeedsSwarm}}</td><td>{{.LeechersSwarm}}</td><td>{{unixTime .LastActivity}}</td></tr>{{end}}</tbody></table><div class=pager>{{if .HasPrev}}<a href="{{.PrevURL}}">← Previous</a>{{else}}<span class=disabled>← Previous</span>{{end}}<span>Page {{.Page}} of {{.TotalPages}}</span>{{if .HasNext}}<a href="{{.NextURL}}">Next →</a>{{else}}<span class=disabled>Next →</span>{{end}}</div><p class=muted>Inventory refreshed {{fmtUpdated .Updated}}.</p></body></html>`

const torrentDetailHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Torrent.Name}} · Torrent · Spartarr</title><style>body{font-family:system-ui,sans-serif;margin:24px;background:#111;color:#eee;max-width:1100px}a{color:#9cf}.top{display:flex;gap:14px;align-items:center;flex-wrap:wrap}.card{background:#1b1b1b;padding:16px;border-radius:10px;margin-top:18px}.muted{color:#aaa}.warn{color:#ffcf66}.bad{color:#ff8f8f}.mono{font-family:ui-monospace,monospace;overflow-wrap:anywhere}.status{font-weight:800;letter-spacing:.08em}.kv{display:grid;grid-template-columns:max-content 1fr;gap:8px 14px}.kv div:nth-child(odd){color:#aaa}</style></head><body><div class=top><a href="/torrents">← All torrents</a><a href="/">Home</a><a href="/library">Library</a>{{if .Refreshing}}<span class=warn>Inventory refresh in progress…</span>{{end}}{{if .LastErr}}<span class=bad>{{.LastErr}}</span>{{end}}</div><h1>{{.Torrent.Name}}</h1><p><span class=status>{{.Torrent.AssociationStatus}}</span> · {{.Torrent.Client}} · {{.Torrent.State}} · {{pct .Torrent.Progress}}</p>{{if .Torrent.AssociationReason}}<p class=muted>{{.Torrent.AssociationReason}}</p>{{end}}{{if or .Torrent.ReclaimableKnown .Torrent.StorageError}}<div class=card><strong>Storage</strong><div class=kv style="margin-top:10px"><div>Reclaimable if torrent data is removed</div><div>{{if .Torrent.ReclaimableKnown}}{{human .Torrent.ReclaimableBytes}}{{else}}Unknown{{end}}</div><div>Shared through hardlinks</div><div>{{if .Torrent.ReclaimableKnown}}{{human .Torrent.SharedBytes}}{{else}}—{{end}}</div><div>Files inspected</div><div>{{.Torrent.InspectedFiles}}</div><div>Files with multiple hardlinks</div><div>{{.Torrent.SharedFiles}}</div>{{if .Torrent.StorageError}}<div>Inspection</div><div class=bad>{{.Torrent.StorageError}}</div>{{end}}</div><p class=muted>This is a hardlink-aware filesystem estimate. Snapshots, reflinks, deduplication and storage outside Spartarr's visible mount may change the actual space freed.</p></div>{{end}}{{if .Torrent.MediaItems}}<div class=card><strong>Current media{{if gt (len .Torrent.MediaItems) 1}} items{{end}}</strong><br>{{range .Torrent.MediaItems}}<a href="/library/{{.Type}}/{{.SourceID}}">{{.Title}} {{if .Year}}({{.Year}}){{end}}</a><br>{{end}}</div>{{else if .Torrent.FormerMediaItems}}<div class=card><strong>Historical media relationship</strong><br>{{range .Torrent.FormerMediaItems}}<a href="/library/{{.Type}}/{{.SourceID}}">{{.Title}} {{if .Year}}({{.Year}}){{end}}</a><br>{{end}}{{if .Torrent.SupersededByHash}}<span class=muted>Superseded by torrent hash <span class=mono>{{.Torrent.SupersededByHash}}</span></span>{{end}}</div>{{end}}<div class=card><div class=kv><div>Hash</div><div class=mono>{{.Torrent.Hash}}</div><div>Tracker</div><div class=mono>{{if .Torrent.Tracker}}{{.Torrent.Tracker}}{{else}}—{{end}}</div><div>Category</div><div>{{if .Torrent.Category}}{{.Torrent.Category}}{{else}}—{{end}}</div><div>Tags</div><div>{{if .Torrent.Tags}}{{.Torrent.Tags}}{{else}}—{{end}}</div><div>Private</div><div>{{if .Torrent.Private}}yes{{else}}no{{end}}</div><div>Selected size</div><div>{{human .Torrent.SizeBytes}}</div><div>Total size</div><div>{{human .Torrent.TotalSizeBytes}}</div><div>Completed</div><div>{{human .Torrent.CompletedBytes}}</div><div>Remaining</div><div>{{human .Torrent.AmountLeftBytes}}</div><div>Availability</div><div>{{printf "%.3f" .Torrent.Availability}}</div><div>Seeds</div><div>{{.Torrent.SeedsConnected}} connected · {{.Torrent.SeedsSwarm}} in swarm</div><div>Leechers</div><div>{{.Torrent.LeechersConnected}} connected · {{.Torrent.LeechersSwarm}} in swarm</div><div>Upload</div><div>{{rate .Torrent.UploadSpeed}} · {{human .Torrent.UploadedBytes}} total · {{human .Torrent.UploadedSession}} this session</div><div>Download</div><div>{{rate .Torrent.DownloadSpeed}} · {{human .Torrent.DownloadedBytes}} total · {{human .Torrent.DownloadedSession}} this session</div><div>Ratio</div><div>{{printf "%.2f" .Torrent.Ratio}}</div><div>Active time</div><div>{{duration .Torrent.TimeActive}}</div><div>Seeding time</div><div>{{duration .Torrent.SeedingTime}}</div><div>Added</div><div>{{unixTime .Torrent.AddedOn}}</div><div>Completed</div><div>{{unixTime .Torrent.CompletionOn}}</div><div>Last activity</div><div>{{unixTime .Torrent.LastActivity}}</div><div>Seen complete</div><div>{{unixTime .Torrent.SeenComplete}}</div><div>ETA</div><div>{{duration .Torrent.ETA}}</div><div>Reannounce</div><div>{{duration .Torrent.Reannounce}}</div><div>Save path</div><div class=mono>{{.Torrent.SavePath}}</div><div>Content path</div><div class=mono>{{.Torrent.ContentPath}}</div><div>Flags</div><div>force={{.Torrent.ForceStart}} · auto-TMM={{.Torrent.AutoTMM}} · sequential={{.Torrent.Sequential}} · super-seeding={{.Torrent.SuperSeeding}}</div></div></div><p class=muted>Inventory refreshed {{fmtUpdated .Updated}}.</p></body></html>`
