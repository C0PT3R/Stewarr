package httpui

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"stewarr/internal/inventory"
	"stewarr/internal/model"
)

type torrentData struct {
	Torrents                                    []model.Torrent
	Updated                                     time.Time
	LastErr                                     error
	Refreshing                                  bool
	Current, Superseded, Orphaned, Unassociated int
	ObsoleteReclaimable                         int64
	ObsoleteKnown                               int
	TotalItems                                  int
	Page                                        int
	PageSize                                    int
	TotalPages                                  int
	HasPrev, HasNext                            bool
	PrevURL, NextURL                            string
	PageLinks                                   []navLink
	SizeLinks                                   []navLink
	Sort, Order                                 string
	SortURLs                                    map[string]string
	AllItems                                    int
	Query                                       string
	StatusFilter                                string
	ReclaimableFilter                           string
	ActivityFilter                              string
	ClearURL                                    string
	HasTorrentClient                            bool
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
	case "ORPHANED":
		return model.TorrentOrphaned
	case "UNASSOCIATED", "UNMATCHED":
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
	counts := map[string]int{model.TorrentCurrent: 0, model.TorrentSuperseded: 0, model.TorrentOrphaned: 0, model.TorrentUnassociated: 0}
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
	d := torrentData{Torrents: all[from:to], Updated: updated, LastErr: last, Refreshing: server.inv.IsRefreshing(), Current: counts[model.TorrentCurrent], Superseded: counts[model.TorrentSuperseded], Orphaned: counts[model.TorrentOrphaned], Unassociated: counts[model.TorrentUnassociated], ObsoleteKnown: obsoleteKnown, ObsoleteReclaimable: obsoleteReclaimable, TotalItems: total, AllItems: allItems, Page: page, PageSize: pageSize, TotalPages: pages, HasPrev: page > 1, HasNext: page < pages, PageLinks: links, SizeLinks: sizes, Sort: sortKey, Order: order, SortURLs: sortURLs, Query: r.URL.Query().Get("q"), StatusFilter: statusFilter, ReclaimableFilter: reclaimableFilter, ActivityFilter: activityFilter, ClearURL: "/torrents", HasTorrentClient: len(server.inv.Config().ServicesOfType("qbittorrent")) > 0}
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

func (server *Server) torrentDetail(w http.ResponseWriter, r *http.Request) {
	hash := strings.Trim(strings.TrimPrefix(r.URL.Path, "/torrents/"), "/")
	if hash == "" || strings.Contains(hash, "/") {
		http.NotFound(w, r)
		return
	}
	serviceID := strings.TrimSpace(r.URL.Query().Get("service_id"))
	if notice, pending := server.pendingProjection().torrentOperation(hash); pending {
		server.renderOperation(w, operationPageData{Active: "torrents", FragmentID: "torrent-detail", Label: notice.Label, Notice: notice, BackURL: "/torrents", BackLabel: "Torrents"})
		return
	}
	_, updated, last := server.inv.Snapshot()
	torrent, detailErr := server.inv.TorrentDetail(hash, serviceID)
	if serviceID == "" && detailErr != nil && strings.Contains(strings.ToLower(detailErrString(detailErr)), "more than one configured instance") {
		http.Error(w, detailErr.Error(), http.StatusConflict)
		return
	}
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
