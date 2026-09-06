package httpui

import (
	"errors"
	"log"
	"net/http"
	"strconv"
)

type storageData struct {
	Devices      []deviceView
	ServiceRoots map[string][]string
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
	data := storageData{
		Devices:      server.deviceViews(items, ts, planningReliable),
		ServiceRoots: server.inv.ServiceRootPaths(),
	}
	if err := renderTemplate(w, server.storageTpl, data); err != nil {
		log.Printf("[http] render storage: %v", err)
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
	if err := server.inv.SetDeviceThreshold(path, target, critical); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
