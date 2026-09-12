package httpui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"stewarr/internal/model"
	"stewarr/internal/removal"
)

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

// relatedTorrentMeta describes why a torrent related to a media removal is
// shown the way it is: what physically ties it to the media (if anything),
// or why it's only ever a historical fact rather than proof.
func relatedTorrentMeta(rt relatedRemovalTorrent) string {
	if rt.PhysicallyBacks {
		return "physically backs this media"
	}
	switch model.NormalizeTorrentStatus(rt.Torrent.AssociationStatus) {
	case model.TorrentCurrent:
		return "current import relationship · not hardlinked"
	case model.TorrentSuperseded:
		return "superseded by a later import"
	case model.TorrentOrphaned:
		return "historical import relationship · media no longer tracked"
	default:
		return "preserved historical relationship"
	}
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
	TorrentServiceID      string
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
		if err := forbidden("managed_file", "unmanaged_path", "target", "torrent", "unmonitor_movies", "unmonitor_episodes", "exclude_movies", "exclude_series"); err != nil {
			return err
		}
		if len(form["path"]) == 0 {
			return fmt.Errorf("unmanaged removal requires at least one path")
		}
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
		case model.TorrentOrphaned:
			label = "Orphaned"
		case model.TorrentUnassociated:
			label = "Unassociated"
		}
		groupsByLabel[label] = append(groupsByLabel[label], relatedTorrent)
	}
	groups := make([]relatedRemovalTorrentGroup, 0, 4)
	for _, label := range []string{"Current", "Superseded", "Orphaned", "Unassociated", "Other"} {
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
			key := c.ServiceID
			if key == "" {
				key = strings.ToLower(c.ServiceName)
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
		label = best.ServiceName
	}
	if label == "" {
		label = "Storage"
	}
	if best.Root != "" {
		key := best.ServiceID
		if key == "" {
			key = strings.ToLower(best.ServiceName)
		}
		if roots := counts[key]; len(roots) > 1 && best.RootLabel != "" && !strings.EqualFold(best.RootLabel, best.ServiceName) {
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
		if best.ServiceName != "" {
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
		case removal.UnmanagedOwner:
			if plan.Kind == removal.UnmanagedObject && state.Selectable {
				display.ActionName, display.ActionValue = "path", state.Path
				display.ActionLabel, display.Selectable = "Remove file", true
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

// mediaRefFor resolves one media item by (kind, id). serviceID
// disambiguates SourceID across multiple configured instances of the same
// service type; when empty, a match is only accepted if it is unique
// across all instances — a real collision fails closed rather than
// silently guessing which instance was meant.
func mediaRefFor(items []model.Media, kind model.MediaType, id int, serviceID string) (model.MediaRef, bool) {
	var found *model.Media
	for i := range items {
		m := &items[i]
		if m.Type != kind || m.SourceID != id {
			continue
		}
		if serviceID != "" {
			if m.ServiceID == serviceID {
				return model.MediaRef{ServiceID: m.ServiceID, Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}, true
			}
			continue
		}
		if found != nil {
			return model.MediaRef{}, false
		}
		found = m
	}
	if found == nil {
		return model.MediaRef{}, false
	}
	return model.MediaRef{ServiceID: found.ServiceID, Type: found.Type, SourceID: found.SourceID, Title: found.Title, Year: found.Year}, true
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
		selectedMedia[fmt.Sprintf("%s:%s:%d", ref.MediaType, ref.ServiceID, ref.MediaID)] = true
	}
	canExcludeMovies, canExcludeSeries := false, false
	targets := []model.Media{}
	for _, mediaItem := range mediaItems {
		if !selectedMedia[fmt.Sprintf("%s:%s:%d", mediaItem.Type, mediaItem.ServiceID, mediaItem.SourceID)] {
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

func (server *Server) buildMediaRemovalPlan(kind model.MediaType, id int, serviceID string, selectionExplicit bool, selectedManaged map[string]bool, selectedTorrents map[string]bool, selectedUnmanaged map[string]bool) (removalData, error) {
	items, _, _ := server.inv.Snapshot()
	mr, ok := mediaRefFor(items, kind, id, serviceID)
	if !ok {
		return removalData{}, fmt.Errorf("media not found")
	}
	refs, _, ferr := server.inv.ManagedFileRefs(kind, id, mr.ServiceID)
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
		if items[itemIndex].Type == kind && items[itemIndex].ServiceID == mr.ServiceID && items[itemIndex].SourceID == id {
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
			// Every torrent Stewarr has ever related to this media — Current,
			// Superseded, or Orphaned — is disclosed and selectable here, so
			// removing the media can also clean up its old releases in one
			// action. Only a torrent with no relationship to this media at all
			// (Unassociated) is excluded. Default selection still comes from
			// physicalTorrentHashes below: only a torrent proven to physically
			// back the media is pre-checked, everything else starts unchecked.
			if model.NormalizeTorrentStatus(torrent.AssociationStatus) == model.TorrentUnassociated {
				continue
			}
			hash := strings.ToLower(torrent.Hash)
			contextByHash[hash] = relatedRemovalTorrent{Torrent: torrent, Selectable: true, PhysicallyBacks: torrent.MediaHardlinked}
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
	key := mediaOperationKey(kind, id, mr.ServiceID)
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

func (server *Server) buildTorrentRemovalPlan(hash, serviceID string, targetSelected bool, selectedManaged map[string]bool, selectedUnmanaged map[string]bool) (removalData, error) {
	h := strings.ToLower(strings.TrimSpace(hash))
	var found *model.Torrent
	for _, t := range server.inv.TorrentSnapshot() {
		if !strings.EqualFold(t.Hash, h) {
			continue
		}
		if serviceID != "" {
			if t.ServiceID == serviceID {
				x := t
				found = &x
				break
			}
			continue
		}
		if found != nil {
			return removalData{}, fmt.Errorf("torrent hash %q exists in more than one configured instance; an service_id is required", h)
		}
		x := t
		found = &x
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
		mediaMap[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = model.MediaRef{ServiceID: m.ServiceID, Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}
	}
	byMedia := map[string][]model.MediaFileRef{}
	for _, r := range proven {
		key := fmt.Sprintf("%s:%s:%d", r.MediaType, r.ServiceID, r.MediaID)
		byMedia[key] = append(byMedia[key], r)
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
			if fr.MediaType != mr.Type || fr.ServiceID != mr.ServiceID || fr.MediaID != mr.SourceID {
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
		BackURL: "/torrents/" + url.PathEscape(h), Hash: h, TorrentServiceID: found.ServiceID,
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
		mergeRemovalCandidate(cm, removal.CandidateFile{Path: p, Owner: removal.UnmanagedOwner, OwnerKey: p, Label: p, Selected: true, Selectable: true})
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
		BackURL: "/unmanaged", UnmanagedPaths: clean, Related: torrents,
		RelatedGroups: groupRelatedTorrents(torrents), SelectedActions: actions,
	}, nil
}
