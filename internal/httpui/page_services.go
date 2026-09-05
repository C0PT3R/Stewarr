package httpui

import (
	"errors"
	"log"
	"net/http"

	"connarr/internal/config"
	"connarr/internal/inventory"
)

// maximumAddIntegrationFormBytes bounds the add-integration form body the
// same way maximumRemovalFormBytes bounds removal forms.
const maximumAddIntegrationFormBytes = 2 << 20

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
	if server.tasks != nil {
		_, _ = server.tasks.RunAsyncApp("inventory")
	}
	w.WriteHeader(http.StatusNoContent)
}
