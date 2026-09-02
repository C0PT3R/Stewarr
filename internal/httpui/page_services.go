package httpui

import (
	"log"
	"net/http"

	"connarr/internal/inventory"
)

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
