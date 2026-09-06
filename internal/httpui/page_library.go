package httpui

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"connarr/internal/inventory"
	"connarr/internal/model"
)

type mediaRow struct {
	Media     model.Media
	Removable bool
}

type navLink struct {
	Value int
	URL   string
}

type libraryData struct {
	Rows             []mediaRow
	Updated          time.Time
	LastErr          error
	Refreshing       bool
	TotalItems       int
	Page             int
	PageSize         int
	TotalPages       int
	HasPrev          bool
	HasNext          bool
	PrevURL          string
	NextURL          string
	PageLinks        []navLink
	Sort             string
	Order            string
	SortURLs         map[string]string
	SizeLinks        []navLink
	AllItems         int
	Query            string
	TypeFilter       string
	TypeOptions      []mediaTypeOption
	SourceFilter     string
	SourceOptions    []mediaSource
	RequestedFilter  string
	WatchedFilter    string
	TorrentFilter    string
	ShowNoFiles      bool
	ClearURL         string
	HasMediaLibrary  bool
	HasTorrentClient bool
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

// mediaTypeOption is one entry in the Library page's Type select.
type mediaTypeOption struct {
	Value, Label string
}

// mediaTypeLabel returns a display label for a MediaType. Movie/Series get
// their established labels; an unrecognized future type (Lidarr's Artist,
// Readarr's Book, ...) falls back to a naive title-case-plus-s, since the
// Type filter must not assume the library only ever holds these two types.
func mediaTypeLabel(t model.MediaType) string {
	switch t {
	case model.Movie:
		return "Movies"
	case model.Series:
		return "Series"
	default:
		s := string(t)
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:] + "s"
	}
}

// availableMediaTypes returns the distinct types actually present in items,
// in a stable order (Movie, Series, then anything else alphabetically) —
// the Type filter only ever offers types that exist, and disappears
// entirely when the library only ever holds one.
func availableMediaTypes(items []model.Media) []model.MediaType {
	seen := map[model.MediaType]bool{}
	for _, m := range items {
		seen[m.Type] = true
	}
	preferred := []model.MediaType{model.Movie, model.Series}
	out := make([]model.MediaType, 0, len(seen))
	for _, t := range preferred {
		if seen[t] {
			out = append(out, t)
			delete(seen, t)
		}
	}
	rest := make([]model.MediaType, 0, len(seen))
	for t := range seen {
		rest = append(rest, t)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i] < rest[j] })
	return append(out, rest...)
}

// normalizeMediaTypeFilter validates the query param against the types
// actually present, rather than a hardcoded movie/series pair — a
// future-proofing requirement given Lidarr/Readarr are planned.
func normalizeMediaTypeFilter(v string, available []model.MediaType) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, t := range available {
		if string(t) == v {
			return v
		}
	}
	return "any"
}

// mediaSource identifies where one Media item comes from for the Source
// filter: its service, and — only when that service has more than one
// configured root — which root, mirroring removal_plan.go's displayPath
// suppression rule so a single-root service never shows a redundant
// "Radarr · Radarr" label.
type mediaSource struct {
	Key   string
	Label string
}

func mediaSourceFor(m model.Media, serviceRoots map[string][]string) mediaSource {
	if len(serviceRoots[m.ServiceName]) <= 1 {
		return mediaSource{Key: m.ServiceName, Label: m.ServiceName}
	}
	best := ""
	for _, root := range serviceRoots[m.ServiceName] {
		if pathUnder(root, m.Path) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return mediaSource{Key: m.ServiceName, Label: m.ServiceName}
	}
	label := filepath.Base(best)
	if strings.EqualFold(label, m.ServiceName) {
		return mediaSource{Key: m.ServiceName + "|" + best, Label: m.ServiceName}
	}
	return mediaSource{Key: m.ServiceName + "|" + best, Label: m.ServiceName + " · " + label}
}

func pathUnder(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// availableMediaSources returns the distinct sources present in items,
// sorted by label, deduplicated by Key.
func availableMediaSources(items []model.Media, serviceRoots map[string][]string) []mediaSource {
	seen := map[string]mediaSource{}
	for _, m := range items {
		source := mediaSourceFor(m, serviceRoots)
		seen[source.Key] = source
	}
	out := make([]mediaSource, 0, len(seen))
	for _, source := range seen {
		out = append(out, source)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
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

func filterMedia(items []model.Media, q, typeFilter, sourceFilter string, serviceRoots map[string][]string, requestedFilter, watchedFilter, torrentFilter string, showNoFiles bool) []model.Media {
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
		if sourceFilter != "any" && mediaSourceFor(m, serviceRoots).Key != sourceFilter {
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
	cfg := server.inv.Config()
	hasMediaLibrary := len(cfg.ServicesOfType("radarr")) > 0 || len(cfg.ServicesOfType("sonarr")) > 0
	hasTorrentClient := len(cfg.ServicesOfType("qbittorrent")) > 0
	serviceRoots := server.inv.ServiceRootPaths()

	availableTypes := availableMediaTypes(items)
	typeOptions := make([]mediaTypeOption, len(availableTypes))
	for i, t := range availableTypes {
		typeOptions[i] = mediaTypeOption{Value: string(t), Label: mediaTypeLabel(t)}
	}

	qtext := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	typeFilter := normalizeMediaTypeFilter(r.URL.Query().Get("type"), availableTypes)
	requestedFilter := normalizeBoolFilter(r.URL.Query().Get("requested"))
	watchedFilter := normalizeBoolFilter(r.URL.Query().Get("watched"))
	torrentFilter := "any"
	if hasTorrentClient {
		torrentFilter = normalizeBoolFilter(r.URL.Query().Get("torrent"))
	}
	showNoFiles := r.URL.Query().Get("show_no_files") == "1"

	// Source options are scoped to whatever Type is currently selected —
	// picking "Movies" should never offer a Sonarr source to filter by.
	typeScopedItems := items
	if typeFilter != "any" {
		typeScopedItems = make([]model.Media, 0, len(items))
		for _, m := range items {
			if string(m.Type) == typeFilter {
				typeScopedItems = append(typeScopedItems, m)
			}
		}
	}
	sourceOptions := availableMediaSources(typeScopedItems, serviceRoots)
	// The filter itself only makes sense with more than one source across
	// the whole library, independent of the current Type selection —
	// otherwise "Source: Movies" would be the only option and do nothing.
	showSourceFilter := len(availableMediaSources(items, serviceRoots)) > 1
	sourceFilter := "any"
	if showSourceFilter {
		sourceFilter = r.URL.Query().Get("source")
		valid := false
		for _, source := range sourceOptions {
			if source.Key == sourceFilter {
				valid = true
				break
			}
		}
		if !valid {
			sourceFilter = "any"
		}
	}

	items = filterMedia(items, qtext, typeFilter, sourceFilter, serviceRoots, requestedFilter, watchedFilter, torrentFilter, showNoFiles)

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
		if sourceFilter != "any" {
			q.Set("source", sourceFilter)
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
	d := libraryData{Rows: rows, Updated: updated, LastErr: last, Refreshing: server.inv.IsRefreshing(), TotalItems: total, AllItems: allItems, Page: page, PageSize: pageSize, TotalPages: totalPages, HasPrev: page > 1, HasNext: page < totalPages, Sort: sortKey, Order: order, SortURLs: sortURLs, SizeLinks: sizeLinks, PageLinks: pageLinks, Query: r.URL.Query().Get("q"), TypeFilter: typeFilter, TypeOptions: typeOptions, RequestedFilter: requestedFilter, WatchedFilter: watchedFilter, TorrentFilter: torrentFilter, ShowNoFiles: showNoFiles, ClearURL: "/library", HasMediaLibrary: hasMediaLibrary, HasTorrentClient: hasTorrentClient}
	if showSourceFilter {
		d.SourceFilter = sourceFilter
		d.SourceOptions = sourceOptions
	}
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
	serviceID := strings.TrimSpace(r.URL.Query().Get("service_id"))
	projection := server.pendingProjection()
	mediaType := model.MediaType(parts[0])

	items, updated, last := server.inv.Snapshot()
	reliability := server.inv.ReliabilitySnapshot()
	if !reliability.Valuation && last == nil {
		last = fmt.Errorf("%s Jellyfin: %s; Seerr: %s", reliability.Message, reliability.Jellyfin, reliability.Seerr)
	}
	resolved, resolvedOK := mediaRefFor(items, mediaType, sourceID, serviceID)
	if resolvedOK {
		if notice, pending := projection.mediaOperation(mediaType, sourceID, resolved.ServiceID); pending {
			server.renderOperation(w, operationPageData{Active: "library", FragmentID: "media-detail", Label: notice.Label, Notice: notice, BackURL: "/library", BackLabel: "Library"})
			return
		}
	}
	if serviceID == "" && !resolvedOK {
		matches := 0
		for _, m := range items {
			if string(m.Type) == parts[0] && m.SourceID == sourceID {
				matches++
			}
		}
		if matches > 1 {
			http.Error(w, "this media id exists in more than one configured instance; an service_id is required", http.StatusConflict)
			return
		}
	}

	for i := range items {
		m := items[i]
		if string(m.Type) != parts[0] || m.SourceID != sourceID {
			continue
		}
		if serviceID != "" {
			if m.ServiceID != serviceID {
				continue
			}
		} else if resolvedOK && m.ServiceID != resolved.ServiceID {
			continue
		}
		m.Torrents = projection.filterTorrents(m.Torrents)
		current, superseded, unassociated := groupMediaTorrents(m.Torrents)
		storageView, filesUpdated, filesErr := server.inv.MediaStorage(m.Type, m.ServiceID, m.SourceID)
		files := storageView.Files
		if len(projection.ManagedFiles) > 0 {
			refs, _, _ := server.inv.ManagedFileRefs(m.Type, m.SourceID, m.ServiceID)
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
	if notice, found := server.removalHistory("media", mediaOperationKey(mediaType, sourceID, serviceID)); found {
		server.renderOperation(w, operationPageData{Active: "library", FragmentID: "media-detail", Label: notice.Label, Notice: notice, BackURL: "/library", BackLabel: "Library"})
		return
	}
	http.NotFound(w, r)
}
