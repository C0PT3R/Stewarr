package httpui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"stewarr/internal/inventory"
	"stewarr/internal/model"
	"stewarr/internal/removal"
	"stewarr/internal/store"
	"stewarr/internal/tasks"
)

func selectedUnmanagedStates(plan removal.RemovalPlan, paths []string) []removal.FileState {
	wanted := map[string]bool{}
	for _, p := range paths {
		wanted[filepath.Clean(p)] = true
	}
	out := []removal.FileState{}
	for _, f := range plan.Files {
		if f.Owner == removal.UnmanagedOwner && f.Selected && wanted[filepath.Clean(f.Path)] {
			out = append(out, f)
		}
	}
	return out
}

// unlinkVerified closes the last practical time-of-check gap before direct OS
// deletion. Each File is checked independently against live owners and the
// physical identity displayed by the confirmed plan.
func (server *Server) unlinkVerified(ctx context.Context, expected []removal.FileState) ([]string, []string) {
	results, errs := []string{}, []string{}
	for _, state := range expected {
		p := filepath.Clean(state.Path)
		if err := server.inv.VerifyUnmanagedContext(ctx, []string{p}); err != nil {
			errs = append(errs, p+": final ownership verification failed: "+err.Error())
			continue
		}
		info, err := os.Lstat(p)
		if err != nil {
			errs = append(errs, p+": final identity verification failed: "+err.Error())
			continue
		}
		if !info.Mode().IsRegular() || !state.Exists || !state.IdentityKnown {
			errs = append(errs, p+": final identity verification failed: path is not the confirmed regular File")
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || uint64(stat.Dev) != state.Device || uint64(stat.Ino) != state.Inode {
			errs = append(errs, p+": final identity verification failed: physical File changed after confirmation")
			continue
		}
		if err := os.Remove(p); err != nil {
			errs = append(errs, p+": "+err.Error())
		} else {
			results = append(results, "filesystem removed: "+p)
		}
	}
	return results, errs
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
	serviceID := strings.TrimSpace(r.URL.Query().Get("service_id"))
	d, err := server.buildMediaRemovalPlan(kind, id, serviceID, r.URL.Query().Get("selection") == "1", selectedManagedSet(r.URL.Query()["managed_file"]), selectedTorrentSet(r), selectedUnmanagedSet(r.URL.Query()["unmanaged_path"]))
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
	d, err := server.buildTorrentRemovalPlan(r.URL.Query().Get("hash"), strings.TrimSpace(r.URL.Query().Get("service_id")), targetSelection(r), selectedManagedSet(r.URL.Query()["managed_file"]), selectedUnmanagedSet(r.URL.Query()["unmanaged_path"]))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	server.renderRemoval(w, d)
}
func (server *Server) removalUnmanaged(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	d, err := server.buildUnmanagedRemovalPlan(r.URL.Query()["path"], selectedTorrentSet(r))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	server.renderRemoval(w, d)
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

// admitRemoval validates only identifiers against Stewarr's published state.
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
		serviceID := strings.TrimSpace(form.Get("service_id"))
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
		mr, ok := mediaRefFor(items, kind, id, serviceID)
		if !ok {
			return removalAdmission{}, fmt.Errorf("media not found")
		}
		return removalAdmission{Kind: removal.MediaObject, Key: mediaOperationKey(kind, id, mr.ServiceID), Label: mr.Title, DryRun: dryRun}, nil
	case "torrent":
		hash := strings.ToLower(strings.TrimSpace(form.Get("hash")))
		serviceID := strings.TrimSpace(form.Get("service_id"))
		if hash == "" || form.Get("target") != "1" {
			return removalAdmission{}, fmt.Errorf("nothing selected")
		}
		var found *model.Torrent
		for _, torrent := range server.inv.TorrentSnapshot() {
			if !strings.EqualFold(torrent.Hash, hash) {
				continue
			}
			if serviceID != "" {
				if torrent.ServiceID == serviceID {
					t := torrent
					found = &t
					break
				}
				continue
			}
			if found != nil {
				return removalAdmission{}, fmt.Errorf("torrent hash %q exists in more than one configured instance; an service_id is required", hash)
			}
			t := torrent
			found = &t
		}
		if found == nil {
			return removalAdmission{}, fmt.Errorf("torrent not found")
		}
		return removalAdmission{Kind: removal.TorrentObject, Key: hash, Label: found.Name, DryRun: dryRun}, nil
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
		serviceID := strings.TrimSpace(r.FormValue("service_id"))
		d, err = server.buildMediaRemovalPlan(mt, id, serviceID, true, selectedManagedSet(r.Form["managed_file"]), mapFromValues(r.Form["torrent"]), map[string]bool{})
	case "torrent":
		d, err = server.buildTorrentRemovalPlan(r.FormValue("hash"), strings.TrimSpace(r.FormValue("service_id")), true, map[string]bool{}, map[string]bool{})
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
	// mediaBytes is the raw sum of every selected file's own size, before
	// hardlink deduplication ("Library bytes removed"). Unlike
	// d.Plan.SelectedPathBytes/ReclaimableBytes, it counts a selected file
	// even when another hardlink to the same data survives the removal.
	var mediaBytes int64
	for _, file := range d.Plan.Files {
		if file.Selected {
			mediaBytes += file.SizeBytes
		}
	}
	var historyErr error
	if historyID > 0 {
		historyErr = db.UpdateHistoryEvent(store.HistoryEvent{ID: historyID, EventType: "removal", Status: "started", DryRun: dry, RequestedKind: string(d.Plan.Kind), RequestedKey: d.Plan.RequestedKey, RequestedLabel: d.Plan.RequestedLabel, ReclaimableBytes: d.Plan.ReclaimableBytes, MediaBytes: mediaBytes, Payload: startedPayload})
	} else {
		historyID, historyErr = db.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "started", DryRun: dry, RequestedKind: string(d.Plan.Kind), RequestedKey: d.Plan.RequestedKey, RequestedLabel: d.Plan.RequestedLabel, ReclaimableBytes: d.Plan.ReclaimableBytes, MediaBytes: mediaBytes, Payload: startedPayload})
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
				if e := server.inv.RemoveTorrent(ctx, rt.Torrent.Hash, rt.Torrent.ServiceID); e != nil {
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
				if e := server.inv.RemoveTorrent(ctx, d.Hash, d.TorrentServiceID); e != nil {
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
				if e := server.inv.RemoveTorrent(ctx, rt.Torrent.Hash, rt.Torrent.ServiceID); e != nil {
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
			seen := map[string]bool{}
			for _, ref := range removedManaged {
				key := ref.ServiceID + ":" + strconv.Itoa(ref.MediaID)
				if strings.EqualFold(ref.Source, "radarr") && !seen[key] {
					seen[key] = true
					if e := server.inv.SetMovieMonitored(ctx, ref.ServiceID, ref.MediaID, false); e != nil {
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
			seen := map[string]bool{}
			idsByService := map[string][]int{}
			for _, ref := range removedManaged {
				if !strings.EqualFold(ref.Source, "sonarr") {
					continue
				}
				for _, part := range ref.Parts {
					key := ref.ServiceID + ":" + strconv.Itoa(part.SourcePartID)
					if part.SourcePartID > 0 && !seen[key] {
						seen[key] = true
						idsByService[ref.ServiceID] = append(idsByService[ref.ServiceID], part.SourcePartID)
					}
				}
			}
			for serviceID, ids := range idsByService {
				sort.Ints(ids)
				if e := server.inv.SetEpisodesMonitored(ctx, serviceID, ids, false); e != nil {
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
			removedMedia[fmt.Sprintf("%s:%s:%d", ref.MediaType, ref.ServiceID, ref.MediaID)] = true
		}
		for _, mediaItem := range d.ExclusionMedia {
			if !removedMedia[fmt.Sprintf("%s:%s:%d", mediaItem.Type, mediaItem.ServiceID, mediaItem.SourceID)] {
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
	if historyErr := db.UpdateHistoryEvent(store.HistoryEvent{ID: historyID, EventType: "removal", Status: status, DryRun: dry, RequestedKind: string(d.Plan.Kind), RequestedKey: d.Plan.RequestedKey, RequestedLabel: d.Plan.RequestedLabel, ReclaimableBytes: d.Plan.ReclaimableBytes, MediaBytes: mediaBytes, Payload: payload, Error: strings.Join(errs, "; ")}); historyErr != nil {
		log.Printf("[removal] [operation=%d] critical History finalization failure: %v", historyID, historyErr)
		recordError(fmt.Sprintf("removal operation %d completed but its durable result could not be finalized: %v", historyID, historyErr))
		if len(results) > 0 {
			status = "partial"
		} else {
			status = "failed"
		}
	}
	auditRemovalComplete(historyID, status, d, len(results), len(errs))
	if r.Header.Get("X-Stewarr-Overlay") == "1" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "dryRun": dry, "results": results, "errors": errs})
		return
	}
	http.Redirect(w, r, "/history", http.StatusSeeOther)
}

func (server *Server) schedulePostRemovalConsistency(historyID int64) error {
	_, err := server.tasks.AdvanceWorkflow(
		"inventory-and-files-consistency",
		"global",
		0,
		5*time.Minute,
		fmt.Sprintf("Removal operation %d", historyID),
	)
	return err
}
