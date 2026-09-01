package httpui

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"connarr/internal/model"
)

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
