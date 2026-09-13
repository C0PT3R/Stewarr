package httpui

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"stewarr/internal/config"
	"stewarr/internal/inventory"
)

type storageData struct {
	Devices          []deviceView
	UnreachableRoots []inventory.UnreachableRoot
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
		Devices:             server.deviceViews(items, ts, planningReliable),
		UnreachableRoots:    server.inv.UnreachableServiceRoots(),
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
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type deviceSettingsData struct {
	RepresentativePath   string
	RootLabels           []string
	Filesystem           string
	Name                 string
	TargetUsagePercent   float64
	CriticalUsagePercent float64
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
	target, critical := cfg.ThresholdsFor(path)
	data := deviceSettingsData{RepresentativePath: path, Name: cfg.DeviceName(path), TargetUsagePercent: target, CriticalUsagePercent: critical}
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
	critical, err := strconv.ParseFloat(r.FormValue("critical_usage_percent"), 64)
	if err != nil {
		http.Error(w, "critical_usage_percent must be a number", http.StatusBadRequest)
		return
	}
	if err := server.inv.SetDeviceThreshold(path, r.FormValue("name"), target, critical); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
