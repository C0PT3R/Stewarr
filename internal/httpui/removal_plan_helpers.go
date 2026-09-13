package httpui

import (
	"fmt"
	"path/filepath"
	"sort"
	"stewarr/internal/model"
	"stewarr/internal/removal"
	"strconv"
	"strings"
)

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
