package httpui

import (
	"log"
	"net/http"
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
