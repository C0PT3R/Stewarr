package httpui

import (
	"errors"
	"log"
	"net/http"
	"time"

	"connarr/internal/config"
	"connarr/internal/inventory"
)

// maximumAddIntegrationFormBytes bounds the add-integration form body the
// same way maximumRemovalFormBytes bounds removal forms.
const maximumAddIntegrationFormBytes = 2 << 20

// scheduleIntegrationConsistency runs the same inventory-then-files chain a
// removal triggers, so adding/editing/removing an integration discovers its
// storage paths (or reclassifies its files as Unmanaged) right away instead
// of waiting for the next periodic file reconciliation — the whole point of
// moving config editing into the app instead of a static file. The quiet
// period coalesces rapid successive changes (e.g. adding several
// integrations back to back) into one run rather than piling them up.
func (server *Server) scheduleIntegrationConsistency(cause string) {
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

func (server *Server) servicesPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/services" {
		http.NotFound(w, r)
		return
	}
	data := servicesData{ServiceRoots: server.inv.IntegrationRootPaths()}
	for _, svc := range server.inv.StatusSnapshot() {
		if svc.Configured {
			data.Services = append(data.Services, svc)
		}
	}
	if err := renderTemplate(w, server.servicesTpl, data); err != nil {
		log.Printf("[http] render services: %v", err)
	}
}

// addIntegrationForm returns the "Add integration" overlay fragment, fetched
// by shell.ts's openOverlay the same way removal overlays are.
func (server *Server) addIntegrationForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := renderTemplate(w, server.addIntegrationTpl, nil); err != nil {
		log.Printf("[http] render add-integration overlay: %v", err)
	}
}

// addIntegration validates and live-activates one new integration. It is
// only ever submitted via the overlay's [data-background-submit] fetch, so
// the response is a plain status: 204 on success (the caller closes the
// modal and refreshes fragments itself), or a plain-text error body shell.ts
// renders inline in the form.
func (server *Server) addIntegration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumAddIntegrationFormBytes)
	if err := r.ParseMultipartForm(maximumAddIntegrationFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	candidate := config.Integration{
		Type:     r.FormValue("type"),
		Name:     r.FormValue("name"),
		URL:      r.FormValue("url"),
		APIKey:   r.FormValue("api_key"),
		Username: r.FormValue("username"),
		Password: r.FormValue("password"),
	}
	if err := server.inv.AddIntegration(r.Context(), candidate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	server.scheduleIntegrationConsistency("Integration added: " + candidate.Name)
	w.WriteHeader(http.StatusNoContent)
}

type editIntegrationData struct {
	ID, Type, Name, URL, APIKey, Username, Password string
}

// editIntegrationForm dispatches by method the same way scheduled removal
// handlers do: GET returns the pre-filled overlay fragment (also carrying
// the "Remove integration" action, so a plain Edit link is the only trigger
// the Services page needs), POST saves the changes.
func (server *Server) editIntegrationForm(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		server.renderEditIntegrationForm(w, r)
	case http.MethodPost:
		server.submitEditIntegration(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (server *Server) renderEditIntegrationForm(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	var found *config.Integration
	for _, integration := range server.inv.Config().Integrations {
		if integration.ID == id {
			found = &integration
			break
		}
	}
	if found == nil {
		http.Error(w, "integration not found", http.StatusNotFound)
		return
	}
	data := editIntegrationData{ID: found.ID, Type: found.Type, Name: found.Name, URL: found.URL, APIKey: found.APIKey, Username: found.Username, Password: found.Password}
	if err := renderTemplate(w, server.editIntegrationTpl, data); err != nil {
		log.Printf("[http] render edit-integration overlay: %v", err)
	}
}

func (server *Server) submitEditIntegration(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maximumAddIntegrationFormBytes)
	if err := r.ParseMultipartForm(maximumAddIntegrationFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")
	updates := config.Integration{
		Name:     r.FormValue("name"),
		URL:      r.FormValue("url"),
		APIKey:   r.FormValue("api_key"),
		Username: r.FormValue("username"),
		Password: r.FormValue("password"),
	}
	if err := server.inv.EditIntegration(r.Context(), id, updates); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	server.scheduleIntegrationConsistency("Integration edited: " + updates.Name)
	w.WriteHeader(http.StatusNoContent)
}

// removeIntegration lets go of an integration entirely. It is only ever
// submitted via the edit overlay's own [data-background-submit] form, so
// like addIntegration/submitEditIntegration the response is a plain status.
func (server *Server) removeIntegration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumAddIntegrationFormBytes)
	if err := r.ParseMultipartForm(maximumAddIntegrationFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")
	if err := server.inv.RemoveIntegration(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	server.scheduleIntegrationConsistency("Integration removed: " + id)
	w.WriteHeader(http.StatusNoContent)
}
