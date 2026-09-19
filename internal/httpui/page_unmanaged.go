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

	"stewarr/internal/model"
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
	// RootKeys is every distinct storage root this group's paths fall
	// under (see unmanagedRootFor), for the Root filter. A group can carry
	// more than one when its paths are hardlinked across different known
	// roots; a group can also carry none, when its physical root wasn't a
	// specific configured/discovered service root but a broader shared
	// ancestor directory that still had to be walked.
	RootKeys []string
}

// unmanagedRemovalURL builds the removal overlay URL for one physical
// file's trash icon — every hardlinked path of the group, not just the
// first, so a single-click removal reclaims the same space the bulk
// checkbox-select path does rather than leaving a dangling hardlink.
func unmanagedRemovalURL(group unmanagedFileGroup) string {
	query := url.Values{}
	for _, file := range group.Paths {
		query.Add("path", file.Path)
	}
	return "/removal/unmanaged?" + query.Encode()
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
	StatusFilter                              string
	RootFilter                                string
	RootOptions                               []mediaSource
	MinSizeMiB                                string
}

// unmanagedRootSource identifies one storage root an unmanaged file falls
// under, for the Root filter — same shape and labeling rule as
// mediaSourceFor (Library's Source filter): "Service · Root" unless the
// root's own label already matches the service name.
func unmanagedRootSource(ctx model.StorageContext) mediaSource {
	label := ctx.ServiceName
	if ctx.RootLabel != "" && !strings.EqualFold(ctx.RootLabel, ctx.ServiceName) {
		label = ctx.ServiceName + " · " + ctx.RootLabel
	}
	return mediaSource{Key: ctx.ServiceName + "|" + ctx.Root, Label: label}
}

func groupUnmanagedFiles(items []model.UnmanagedFile) []unmanagedFileGroup {
	type groupKey struct {
		device uint64
		inode  uint64
		path   string
	}
	groups := map[groupKey]*unmanagedFileGroup{}
	rootSets := map[groupKey]map[string]bool{}
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
			rootSets[key] = map[string]bool{}
			order = append(order, key)
		}
		if f.ModifiedAt.After(g.ModifiedAt) {
			g.ModifiedAt = f.ModifiedAt
		}
		if f.Links > g.Links {
			g.Links = f.Links
		}
		g.ReclaimableKnown = g.ReclaimableKnown && f.ReclaimableKnown
		for _, ctx := range f.StorageContexts {
			rootSets[key][unmanagedRootSource(ctx).Key] = true
		}
		f.Path = cp
		g.Paths = append(g.Paths, f)
	}
	out := make([]unmanagedFileGroup, 0, len(order))
	for _, key := range order {
		g := groups[key]
		for rootKey := range rootSets[key] {
			g.RootKeys = append(g.RootKeys, rootKey)
		}
		sort.Strings(g.RootKeys)
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

// availableUnmanagedRoots returns the distinct storage roots present across
// every group, sorted by label and deduplicated by key — same shape as
// availableMediaSources (Library's Source filter). Groups with no
// StorageContexts at all (see unmanagedFileGroup.RootKeys) contribute
// nothing here; they're only reachable through "Any."
func availableUnmanagedRoots(items []unmanagedFileGroup) []mediaSource {
	seen := map[string]mediaSource{}
	for _, g := range items {
		for _, f := range g.Paths {
			for _, ctx := range f.StorageContexts {
				source := unmanagedRootSource(ctx)
				seen[source.Key] = source
			}
		}
	}
	out := make([]mediaSource, 0, len(seen))
	for _, source := range seen {
		out = append(out, source)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// normalizeUnmanagedStatusFilter validates the query param against the
// three well-known reclaimability states a group can be in (see
// groupUnmanagedFiles) — always available, unlike Root/Type which only
// offer options actually present in the data.
func normalizeUnmanagedStatusFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "reclaimable":
		return "reclaimable"
	case "shared":
		return "shared"
	case "unknown":
		return "unknown"
	default:
		return "any"
	}
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
	if r.Header.Get("X-Stewarr-Scan") == "1" {
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
	http.Redirect(w, r, "/unmanaged", http.StatusSeeOther)
}

func (server *Server) unmanagedDownloads(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/unmanaged" {
		http.NotFound(w, r)
		return
	}
	raw, updated, scanErr := server.inv.UnmanagedSnapshot()
	projection := server.pendingProjection()
	all := groupUnmanagedFiles(raw)
	live := make([]unmanagedFileGroup, 0, len(all))
	for _, group := range all {
		paths := make([]string, len(group.Paths))
		for i, file := range group.Paths {
			paths[i] = file.Path
		}
		if !projection.unmanagedPending(paths) {
			live = append(live, group)
		}
	}
	all = live
	allItems := len(all)
	var totalBytes, reclaimableBytes, sharedBytes int64
	for _, f := range all {
		totalBytes += f.SizeBytes
		reclaimableBytes += f.ReclaimableBytes
		sharedBytes += f.SharedBytes
	}
	rootOptions := availableUnmanagedRoots(all)
	qtext := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	statusFilter := normalizeUnmanagedStatusFilter(r.URL.Query().Get("status"))
	rootFilter := r.URL.Query().Get("root")
	if rootFilter != "" {
		found := false
		for _, opt := range rootOptions {
			if opt.Key == rootFilter {
				found = true
				break
			}
		}
		if !found {
			rootFilter = ""
		}
	}
	minSizeMiBText := strings.TrimSpace(r.URL.Query().Get("min_size_mib"))
	var minSizeBytes int64
	if v, err := strconv.ParseFloat(minSizeMiBText, 64); err == nil && v > 0 {
		minSizeBytes = int64(v * 1024 * 1024)
	} else {
		minSizeMiBText = ""
	}
	if qtext != "" || statusFilter != "any" || rootFilter != "" || minSizeBytes > 0 {
		filtered := make([]unmanagedFileGroup, 0, len(all))
		for _, f := range all {
			if qtext != "" {
				match := false
				for _, p := range f.Paths {
					if strings.Contains(strings.ToLower(p.Path), qtext) {
						match = true
						break
					}
				}
				if !match {
					continue
				}
			}
			switch statusFilter {
			case "reclaimable":
				if f.ReclaimableBytes <= 0 {
					continue
				}
			case "shared":
				if f.SharedBytes <= 0 {
					continue
				}
			case "unknown":
				if f.ReclaimableKnown {
					continue
				}
			}
			if rootFilter != "" {
				match := false
				for _, k := range f.RootKeys {
					if k == rootFilter {
						match = true
						break
					}
				}
				if !match {
					continue
				}
			}
			if minSizeBytes > 0 && f.SizeBytes < minSizeBytes {
				continue
			}
			filtered = append(filtered, f)
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
		if statusFilter != "any" {
			q.Set("status", statusFilter)
		}
		if rootFilter != "" {
			q.Set("root", rootFilter)
		}
		if minSizeMiBText != "" {
			q.Set("min_size_mib", minSizeMiBText)
		}
		return "/unmanaged?" + q.Encode()
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
	d := unmanagedData{Files: all[from:to], Updated: updated, ScanErr: scanErr, TotalItems: total, AllItems: allItems, TotalBytes: totalBytes, ReclaimableBytes: reclaimableBytes, SharedBytes: sharedBytes, Page: page, PageSize: pageSize, TotalPages: pages, HasPrev: page > 1, HasNext: page < pages, PageLinks: links, SizeLinks: sizes, Sort: sortKey, Order: order, Query: r.URL.Query().Get("q"), SortURLs: sortURLs, StatusFilter: statusFilter, RootFilter: rootFilter, RootOptions: rootOptions, MinSizeMiB: minSizeMiBText}
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
