package httpui

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"stewarr/internal/cleanup"
	"stewarr/internal/config"
	"stewarr/internal/inventory"
	"stewarr/internal/model"
)

type storageData struct {
	Devices      []deviceView
	Capabilities []inventory.RootCapability
	// AutoRemovalDisabled hides the cleanup-candidate/plan section entirely
	// when Removal.AutoMode is Disabled — a disabled setting means
	// automatic removal (and the whole notion of "here's what we'd
	// suggest removing") doesn't exist, not just that nothing gets
	// submitted unattended. The bar/legend/usage numbers above it are
	// unaffected: those describe real disk state, not a removal
	// suggestion.
	AutoRemovalDisabled bool
}

func (server *Server) storagePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/storage" {
		http.NotFound(w, r)
		return
	}
	items, _, _ := server.inv.Snapshot()
	projection := server.pendingProjection()
	items = projection.filterMedia(items)
	ts := projection.filterTorrents(server.inv.TorrentSnapshot())
	planningReliable := server.planningReliable(server.inv.ReliabilitySnapshot())
	autoMode := server.inv.Config().Removal.AutoMode
	data := storageData{
		Devices:             server.deviceViews(items, ts, planningReliable, nil),
		Capabilities:        server.inv.StorageCapabilities(),
		AutoRemovalDisabled: autoMode != config.RemovalAutoConfirm && autoMode != config.RemovalAutoAuto,
	}
	if err := renderTemplate(w, server.storageTpl, data); err != nil {
		log.Printf("[http] render storage: %v", err)
	}
}

type storageStatsClaim struct {
	Service string `json:"service"`
	Bytes   uint64 `json:"bytes"`
}

type storageStatsDevice struct {
	RepresentativePath   string              `json:"representativePath"`
	Available            bool                `json:"available"`
	Error                string              `json:"error,omitempty"`
	TotalBytes           uint64              `json:"totalBytes"`
	FreeBytes            uint64              `json:"freeBytes"`
	UsedBytes            uint64              `json:"usedBytes"`
	UsagePercent         float64             `json:"usagePercent"`
	TargetUsagePercent   float64             `json:"targetUsagePercent"`
	CriticalUsagePercent float64             `json:"criticalUsagePercent"`
	Claimed              []storageStatsClaim `json:"claimed"`
	UnmanagedBytes       uint64              `json:"unmanagedBytes"`
	OtherBytes           uint64              `json:"otherBytes"`
	ReservedBytes        uint64              `json:"reservedBytes"`
	UsableBytes          uint64              `json:"usableBytes"`
	StewarrUsedBytes     uint64              `json:"stewarrUsedBytes"`
	UsagePercentOfUsable float64             `json:"usagePercentOfUsable"`
}

// storageStats serves the raw, cheap byte-level numbers a storage device
// exposes every few seconds (see watchStorageChanges in reactive.go) without
// ever touching cleanup.Build — unlike the full Storage page, computing
// which items would actually be removed is real work that has nothing to do
// with disk bytes ticking up or down, and must not be redone on every one of
// those ticks just because the client wants fresher numbers to display.
func (server *Server) storageStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := server.inv.Config()
	devices := server.inv.StorageDevices()
	out := make([]storageStatsDevice, 0, len(devices))
	for _, device := range devices {
		usagePercent := 0.0
		if device.TotalBytes > 0 {
			usagePercent = float64(device.UsedBytes) / float64(device.TotalBytes) * 100
		}
		target, critical := cfg.ThresholdsFor(device.RepresentativePath)
		claimed := make([]storageStatsClaim, 0, len(device.Claimed))
		for _, segment := range device.Claimed {
			claimed = append(claimed, storageStatsClaim{Service: segment.Service, Bytes: segment.Bytes})
		}
		usableBytes := device.UsableBytes()
		stewarrUsedBytes := device.StewarrUsedBytes()
		usagePercentOfUsable := 0.0
		if usableBytes > 0 {
			usagePercentOfUsable = float64(stewarrUsedBytes) / float64(usableBytes) * 100
		}
		out = append(out, storageStatsDevice{
			RepresentativePath:   device.RepresentativePath,
			Available:            device.Available,
			Error:                device.Error,
			TotalBytes:           device.TotalBytes,
			FreeBytes:            device.FreeBytes,
			UsedBytes:            device.UsedBytes,
			UsagePercent:         usagePercent,
			TargetUsagePercent:   target,
			CriticalUsagePercent: critical,
			Claimed:              claimed,
			UnmanagedBytes:       device.UnmanagedBytes,
			OtherBytes:           device.OtherBytes,
			ReservedBytes:        device.ReservedBytes,
			UsableBytes:          usableBytes,
			StewarrUsedBytes:     stewarrUsedBytes,
			UsagePercentOfUsable: usagePercentOfUsable,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type deviceSettingsData struct {
	RepresentativePath      string
	RootLabels              []string
	Filesystem              string
	Name                    string
	TargetUsagePercent      float64
	AutomaticRemovalEnabled bool
}

// deviceSettingsForm returns the per-device settings overlay fragment
// (currently just Target/Critical %, the same fields the old inline form
// on the Storage page collected) — the wrench icon on each device's card
// opens this, so any future per-device setting has one obvious place to
// join instead of the card growing a new inline field every time.
//
// RepresentativePath is only ever one arbitrarily chosen root among
// possibly several sharing the same physical device (see
// inventory.StorageDevice) — showing just that bare path made it look
// like the settings applied to one path specifically, rather than the
// whole device. RootLabels/Filesystem let the template show the same
// identity the Storage page card itself already does, so the modal
// reads as "here's which device this is" instead of "here's the one
// path affected."
func (server *Server) deviceSettingsForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	cfg := server.inv.Config()
	target, _ := cfg.ThresholdsFor(path)
	data := deviceSettingsData{RepresentativePath: path, Name: cfg.DeviceName(path), TargetUsagePercent: target, AutomaticRemovalEnabled: cfg.AutomaticRemovalEnabledFor(path)}
	for _, device := range server.inv.StorageDevices() {
		if device.RepresentativePath == path {
			data.RootLabels = device.RootLabels
			data.Filesystem = device.Filesystem
			break
		}
	}
	if err := renderTemplate(w, server.deviceSettingsTpl, data); err != nil {
		log.Printf("[http] render device-settings overlay: %v", err)
	}
}

type cleanupPlanData struct {
	RepresentativePath string
	RootLabels         []string
	Filesystem         string
	Plan               cleanup.Plan
	// Excluded is the current set of action keys left out of Plan's own
	// selection (see cleanup.Build) — rendered back into the Clean form as
	// hidden fields so submitting Clean, or protecting one more row,
	// reproduces exactly this same filtered view server-side rather than
	// risking drift between what's displayed and what gets acted on.
	Excluded []string
}

// planForDevice rebuilds the named device's current cleanup plan fresh,
// leaving out any action whose key is in excluded (nil/empty for the
// normal case) — shared by cleanupPlanForm, cleanupPlanClean, and
// cleanupPlanProtect so Clean/Protect always act against a live
// recomputation rather than trusting whatever was true when the modal was
// first opened, matching every other removal path's "recompute right
// before acting" convention.
func (server *Server) planForDevice(path string, excluded map[string]bool) (cleanupPlanData, bool) {
	items, _, _ := server.inv.Snapshot()
	projection := server.pendingProjection()
	items = projection.filterMedia(items)
	ts := projection.filterTorrents(server.inv.TorrentSnapshot())
	planningReliable := server.planningReliable(server.inv.ReliabilitySnapshot())
	excludedList := make([]string, 0, len(excluded))
	for key := range excluded {
		excludedList = append(excludedList, key)
	}
	for _, view := range server.deviceViews(items, ts, planningReliable, excluded) {
		if view.Storage.RepresentativePath == path {
			return cleanupPlanData{RepresentativePath: path, RootLabels: view.Storage.RootLabels, Filesystem: view.Storage.Filesystem, Plan: view.Plan, Excluded: excludedList}, true
		}
	}
	return cleanupPlanData{RepresentativePath: path, Excluded: excludedList}, false
}

// excludedSetFrom builds the excluded-keys set cleanupPlanForm/Clean/
// Protect all share from a request's repeated "excluded" values — the
// same field name whether it arrives as GET query parameters (opening/
// reopening the modal) or POST form values (Clean, which carries the
// modal's current hidden inputs forward so it recomputes the identical
// filtered plan the user is actually looking at).
func excludedSetFrom(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]bool, len(values))
	for _, v := range values {
		if v != "" {
			set[v] = true
		}
	}
	return set
}

// cleanupPlanForm returns the per-device cleanup-plan overlay fragment — the
// detailed per-action Torrents/Media lists used to live inline in the
// device's card, but a device with many candidates made the card
// uncomfortably long; the card now only shows the aggregate summary
// ("N action(s) selected"), and this overlay (opened by the 📋 button next
// to the device's wrench) is where the actual list lives. A repeated
// ?excluded=<key> query value reopens the same plan with those actions
// left out of selection — see planForDevice — used by the row-level
// Exclude/Protect buttons to show the live replacement immediately.
func (server *Server) cleanupPlanForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, _ := server.planForDevice(r.URL.Query().Get("path"), excludedSetFrom(r.URL.Query()["excluded"]))
	if err := renderTemplate(w, server.cleanupPlanTpl, data); err != nil {
		log.Printf("[http] render cleanup-plan overlay: %v", err)
	}
}

// cleanupPlanClean executes the batch of cleanup-plan actions the user
// left checked (see actionKey) — available regardless of removal.auto_mode,
// the same standing as manually removing any single item today, just
// batched. Deliberately does not apply Auto mode's unattended-only safety
// gates (actionIsUnassociatedTorrent/actionIsIncompleteTorrent/
// mediaTMDBDataStale in auto_removal.go) — those substitute for human
// judgment during *unattended* execution; a human explicitly reviewing
// this list and clicking Clean already is that judgment. It does still
// apply removal.dry_run (read live inside submitAutoRemoval's own
// admission path) and the AutoUnmonitor/AutoExcludeFromImportLists
// config toggles, the same as Auto mode.
func (server *Server) cleanupPlanClean(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(maximumAddServiceFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	// The excluded set carried forward from the modal's own hidden fields
	// (see cleanupPlanData.Excluded) is what makes this recompute produce
	// exactly the plan the user is currently looking at — every excluded/
	// protected-this-session row already replaced by its own backfill
	// candidate, rather than Clean silently cleaning fewer bytes than the
	// device's target calls for.
	data, found := server.planForDevice(r.FormValue("path"), excludedSetFrom(r.Form["excluded"]))
	if !found {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	errs := server.cleanActions(data.Plan.Actions, server.inv.Config())
	if len(errs) > 0 {
		http.Error(w, strings.Join(errs, "; "), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cleanActions submits every given action through the existing
// formForAction/submitAutoRemoval pipeline, and returns one error string
// per action that failed — factored out of cleanupPlanClean so it's
// testable independent of a real cleanup.Plan/device, which requires far
// heavier fixtures (StorageDevices, real disk stats) than this logic
// itself depends on.
func (server *Server) cleanActions(actions []cleanup.Action, cfg config.Config) []string {
	var errs []string
	for _, action := range actions {
		form, err := server.formForAction(action)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		// removal_execution.go's execution step already scopes each of
		// these to the right media type/source internally, so it's safe
		// to set all four unconditionally rather than branch on
		// action.Kind/Media.Type — see runAutoRemovalEvaluation.
		if cfg.Removal.AutoUnmonitor {
			form.Set("unmonitor_movies", "1")
			form.Set("unmonitor_episodes", "1")
		}
		if cfg.Removal.AutoExcludeFromImportLists {
			form.Set("exclude_movies", "1")
			form.Set("exclude_series", "1")
		}
		if err := server.submitAutoRemoval(form, action, "Manual cleanup: "); err != nil {
			errs = append(errs, err.Error())
		}
	}
	return errs
}

// cleanupPlanProtect permanently protects one action's media/torrent from
// cleanup (see inventory.ProtectMedia/ProtectTorrent) — an immediate,
// independent action, unlike Clean, since it's meant to be usable one row
// at a time while still reviewing the rest of the list.
func (server *Server) cleanupPlanProtect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	// Same reasoning as cleanupPlanClean: rebuild against the identical
	// already-excluded set the modal currently shows, so the requested key
	// is actually findable — a fresh, entirely unexcluded rebuild could
	// have already chosen a *different* replacement candidate instead of
	// the one this exact row (and its key) represents on screen.
	data, found := server.planForDevice(r.FormValue("path"), excludedSetFrom(r.Form["excluded"]))
	if !found {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	key := r.FormValue("key")
	var target *cleanup.Action
	for i := range data.Plan.Actions {
		if cleanup.ActionKey(data.Plan.Actions[i]) == key {
			target = &data.Plan.Actions[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "action no longer in the current plan", http.StatusConflict)
		return
	}
	var err error
	if target.Kind == cleanup.StandaloneTorrent {
		err = server.inv.ProtectTorrent(r.Context(), target.Torrents[0].Hash, target.Torrents[0].ServiceID)
	} else if target.Media.Type == model.Movie || target.Media.Type == model.Series {
		err = server.inv.ProtectMedia(r.Context(), target.Media.Type, target.Media.SourceID, target.Media.ServiceID)
	} else {
		http.Error(w, "unsupported action kind", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setDeviceThreshold saves one storage device's reclamation thresholds,
// edited directly from the Storage page — the natural place for this once
// a device actually exists, rather than guessing at add-service time
// before any of its roots are even known (storage roots are discovered by
// each service's own adapter, never entered by hand).
func (server *Server) setDeviceThreshold(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// shell.ts's data-background-submit always sends the body as
	// multipart/form-data (fetch(url, {body: new FormData(form)})), never
	// urlencoded — r.ParseForm alone never reads a multipart body, so every
	// field would come back empty here regardless of what was actually
	// submitted.
	if err := r.ParseMultipartForm(maximumAddServiceFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	path := r.FormValue("representative_path")
	target, err := strconv.ParseFloat(r.FormValue("target_usage_percent"), 64)
	if err != nil {
		http.Error(w, "target_usage_percent must be a number", http.StatusBadRequest)
		return
	}
	// Critical isn't on this form anymore (not shown in the UI at all), but
	// SetDeviceThreshold still persists a value for it under the hood — keep
	// whatever was already saved rather than resetting it to a form field
	// that no longer exists.
	_, critical := server.inv.Config().ThresholdsFor(path)
	enabled := r.FormValue("enable_automatic_removal") == "on"
	if err := server.inv.SetDeviceThreshold(path, r.FormValue("name"), target, critical, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
