package httpui

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"connarr/internal/config"
	"connarr/internal/inventory"
)

// consistencyWorkflowInstanceID identifies the single global workflow
// instance every service add/edit/remove coalesces into (see
// scheduleServiceConsistency) — the "inventory" step fetches from every
// configured service in one pass, not just whichever one triggered it, so
// there's exactly one instance to watch regardless of which service the
// step-3 progress view was opened for.
const consistencyWorkflowInstanceID = "inventory-and-files-consistency:global"

// maximumAddServiceFormBytes bounds the add-service form body the
// same way maximumRemovalFormBytes bounds removal forms.
const maximumAddServiceFormBytes = 2 << 20

// scheduleServiceConsistency runs the same inventory-then-files chain a
// removal triggers, so adding/editing/removing a service discovers its
// storage paths (or reclassifies its files as Unmanaged) right away instead
// of waiting for the next periodic file reconciliation — the whole point of
// moving config editing into the app instead of a static file. The quiet
// period coalesces rapid successive changes (e.g. adding several
// services back to back) into one run rather than piling them up.
func (server *Server) scheduleServiceConsistency(cause string) {
	if server.tasks == nil {
		return
	}
	if _, err := server.tasks.AdvanceWorkflow("inventory-and-files-consistency", "global", 5*time.Second, 2*time.Minute, cause); err != nil {
		log.Printf("[http] schedule inventory/files consistency after %s: %v", cause, err)
	}
}

type servicesData struct {
	Services     []inventory.ServiceStatus
	ServiceRoots map[string][]string
}

// serviceTypeOption is one entry in the Add-service overlay's Type select.
type serviceTypeOption struct {
	Value, Label string
}

// allServiceTypes is every adapter Connarr currently supports, in the
// order they've always been presented — the default when no category
// narrows the list (e.g. opened from the Services page directly).
var allServiceTypes = []serviceTypeOption{
	{"radarr", "Radarr"},
	{"sonarr", "Sonarr"},
	{"qbittorrent", "qBittorrent"},
	{"jellyfin", "Jellyfin"},
	{"seerr", "Seerr"},
}

// serviceCategory groups adapter types by the role they fill, purely for
// presentation — it's never persisted. Opening the overlay from a
// category-specific empty state (Torrents, Library) narrows the Type
// select to just that category's adapters and changes the heading, so a
// future Transmission/Deluge or Lidarr/Readarr adapter only ever needs
// adding to the relevant category's Types list here, not a new overlay.
type serviceCategory struct {
	Title string
	Types []serviceTypeOption
}

var serviceCategories = map[string]serviceCategory{
	"torrentclient": {Title: "Add torrent client", Types: []serviceTypeOption{
		{"qbittorrent", "qBittorrent"},
	}},
	"medialibrary": {Title: "Add media library", Types: []serviceTypeOption{
		{"radarr", "Radarr"},
		{"sonarr", "Sonarr"},
	}},
}

type addServiceFormData struct {
	Title string
	Types []serviceTypeOption
}

func addServiceFormDataFor(category string) addServiceFormData {
	if found, ok := serviceCategories[category]; ok {
		return addServiceFormData{Title: found.Title, Types: found.Types}
	}
	return addServiceFormData{Title: "Add service", Types: allServiceTypes}
}

func (server *Server) servicesPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/services" {
		http.NotFound(w, r)
		return
	}
	data := servicesData{ServiceRoots: server.inv.ServiceRootPaths()}
	for _, svc := range server.inv.StatusSnapshot() {
		if svc.Configured {
			data.Services = append(data.Services, svc)
		}
	}
	if err := renderTemplate(w, server.servicesTpl, data); err != nil {
		log.Printf("[http] render services: %v", err)
	}
}

// addServiceForm returns the "Add service" overlay fragment, fetched
// by shell.ts's openOverlay the same way removal overlays are.
func (server *Server) addServiceForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := addServiceFormDataFor(r.URL.Query().Get("category"))
	if err := renderTemplate(w, server.addServiceTpl, data); err != nil {
		log.Printf("[http] render add-service overlay: %v", err)
	}
}

// testServiceConnection is step 1 of the service setup overlay: a live
// connection check against the entered URL/credentials with nothing
// persisted and no client activated — fast feedback before step 2's extra
// fields (root path, device thresholds) are even shown. 204 on success,
// plain-text error body on failure, same convention as addService.
func (server *Server) testServiceConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumAddServiceFormBytes)
	if err := r.ParseMultipartForm(maximumAddServiceFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	candidate := config.Service{
		Type:     r.FormValue("type"),
		Name:     r.FormValue("name"),
		URL:      r.FormValue("url"),
		APIKey:   r.FormValue("api_key"),
		Username: r.FormValue("username"),
		Password: r.FormValue("password"),
	}
	if err := server.inv.TestServiceConnection(r.Context(), candidate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// addService validates and live-activates one new service. It is
// only ever submitted via the overlay's [data-background-submit] fetch, so
// the response is a plain status: 204 on success (the caller closes the
// modal and refreshes fragments itself), or a plain-text error body shell.ts
// renders inline in the form.
func (server *Server) addService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumAddServiceFormBytes)
	if err := r.ParseMultipartForm(maximumAddServiceFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	candidate := config.Service{
		Type:                  r.FormValue("type"),
		Name:                  r.FormValue("name"),
		URL:                   r.FormValue("url"),
		APIKey:                r.FormValue("api_key"),
		Username:              r.FormValue("username"),
		Password:              r.FormValue("password"),
		AllowAutomaticRemoval: r.FormValue("allow_automatic_removal") == "on",
	}
	if err := server.inv.AddService(r.Context(), candidate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	server.scheduleServiceConsistency("Service added: " + candidate.Name)
	w.WriteHeader(http.StatusNoContent)
}

type editServiceData struct {
	ID, Type, Name, URL, APIKey, Username, Password string
	AllowAutomaticRemoval                           bool
}

// editServiceForm dispatches by method the same way scheduled removal
// handlers do: GET returns the pre-filled overlay fragment (also carrying
// the "Remove service" action, so a plain Edit link is the only trigger
// the Services page needs), POST saves the changes.
func (server *Server) editServiceForm(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		server.renderEditServiceForm(w, r)
	case http.MethodPost:
		server.submitEditService(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (server *Server) renderEditServiceForm(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	var found *config.Service
	for _, service := range server.inv.Config().Services {
		if service.ID == id {
			found = &service
			break
		}
	}
	if found == nil {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}
	data := editServiceData{ID: found.ID, Type: found.Type, Name: found.Name, URL: found.URL, APIKey: found.APIKey, Username: found.Username, Password: found.Password, AllowAutomaticRemoval: found.AllowAutomaticRemoval}
	if err := renderTemplate(w, server.editServiceTpl, data); err != nil {
		log.Printf("[http] render edit-service overlay: %v", err)
	}
}

func (server *Server) submitEditService(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maximumAddServiceFormBytes)
	if err := r.ParseMultipartForm(maximumAddServiceFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")
	// The edit form only shows the credential fields relevant to the
	// service's type (API key, or username+password, never both) — the
	// other side isn't present in the submission at all, so it must be
	// carried forward from the existing value rather than treated as
	// cleared, or a save with no real intent to touch it would silently wipe
	// a credential set outside the UI (e.g. qBittorrent's optional API key).
	var existing config.Service
	for _, service := range server.inv.Config().Services {
		if service.ID == id {
			existing = service
			break
		}
	}
	updates := config.Service{
		Name:                  r.FormValue("name"),
		URL:                   r.FormValue("url"),
		APIKey:                existing.APIKey,
		Username:              existing.Username,
		Password:              existing.Password,
		AllowAutomaticRemoval: r.FormValue("allow_automatic_removal") == "on",
	}
	if strings.EqualFold(existing.Type, "qbittorrent") {
		updates.Username = r.FormValue("username")
		updates.Password = r.FormValue("password")
	} else {
		updates.APIKey = r.FormValue("api_key")
	}
	if err := server.inv.EditService(r.Context(), id, updates); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	server.scheduleServiceConsistency("Service edited: " + updates.Name)
	w.WriteHeader(http.StatusNoContent)
}

// removeService lets go of a service entirely. It is only ever
// submitted via the edit overlay's own [data-background-submit] form, so
// like addService/submitEditService the response is a plain status.
func (server *Server) removeService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumAddServiceFormBytes)
	if err := r.ParseMultipartForm(maximumAddServiceFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")
	if err := server.inv.RemoveService(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	server.scheduleServiceConsistency("Service removed: " + id)
	w.WriteHeader(http.StatusNoContent)
}

// serviceConsistencyProgressForm is step 3 of the service setup overlay,
// opened automatically once step 2 saves. It shows which stage of the
// inventory-and-files-consistency workflow is running with an indeterminate
// (not percentage) progress indicator, since the files stage has no known
// total ahead of time. A "Close" button (data-overlay-cancel) already just
// closes the modal without touching server-side state, so leaving the scan
// running in the background is the existing default — no new endpoint
// needed for that part.
func (server *Server) serviceConsistencyProgressForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := renderTemplate(w, server.serviceProgressTpl, nil); err != nil {
		log.Printf("[http] render service-consistency-progress overlay: %v", err)
	}
}

type consistencyStatusResponse struct {
	State       string `json:"state"`
	CurrentStep int    `json:"currentStep"`
	TotalSteps  int    `json:"totalSteps"`
	StepLabel   string `json:"stepLabel"`
}

// serviceConsistencyStatus backs step 3's polling: the client re-fetches
// this on every SSE revision (server.tasks.Changes() already publishes one
// on every workflow step advance — no new push mechanism needed) and
// updates the label/step text in place.
func (server *Server) serviceConsistencyStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	response := consistencyStatusResponse{State: "succeeded", TotalSteps: 2}
	if server.tasks != nil {
		for _, status := range server.tasks.WorkflowStatuses() {
			if status.ID != consistencyWorkflowInstanceID {
				continue
			}
			response = consistencyStatusResponse{State: status.State, CurrentStep: status.CurrentStep, TotalSteps: status.TotalSteps, StepLabel: status.StepName}
			break
		}
	}
	writeJSON(w, response)
}
