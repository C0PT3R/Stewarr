package httpui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"stewarr/internal/model"
	"stewarr/internal/removal"
	"strings"
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
