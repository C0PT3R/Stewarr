package httpui

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"connarr/internal/cleanup"
	"connarr/internal/inventory"
	"connarr/internal/store"
)

func (server *Server) apiMedia(w http.ResponseWriter, r *http.Request) {
	x, u, e := server.inv.Snapshot()
	writeJSON(w, map[string]any{"items": x, "updated": u, "error": errString(e), "refreshing": server.inv.IsRefreshing(), "reliability": server.inv.ReliabilitySnapshot()})
}

type dashboardAPI struct {
	Revision            uint64                    `json:"revision"`
	TotalMedia          int                       `json:"totalMedia"`
	Movies              int                       `json:"movies"`
	Series              int                       `json:"series"`
	LibraryBytes        string                    `json:"libraryBytes"`
	TotalTorrents       int                       `json:"totalTorrents"`
	Current             int                       `json:"current"`
	Superseded          int                       `json:"superseded"`
	Unassociated        int                       `json:"unassociated"`
	ObsoleteReclaimable string                    `json:"obsoleteReclaimable"`
	ObsoleteKnown       int                       `json:"obsoleteKnown"`
	Stats               store.CleanupStats        `json:"stats"`
	Services            []inventory.ServiceStatus `json:"services,omitempty"`
}

func (server *Server) apiDashboard(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "GET only", http.StatusMethodNotAllowed)
		return
	}
	revision := server.revisions.current().Revision
	etag := `"dashboard-` + strconv.FormatUint(revision, 10) + `"`
	response.Header().Set("ETag", etag)
	response.Header().Set("Cache-Control", "no-cache")
	if request.Header.Get("If-None-Match") == etag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	data := server.dashboardSnapshot()
	writeJSON(response, dashboardAPI{
		Revision:   revision,
		TotalMedia: data.TotalMedia, Movies: data.Movies, Series: data.Series, LibraryBytes: cleanup.Human(uint64(max64(data.LibraryBytes, 0))),
		TotalTorrents: data.TotalTorrents, Current: data.Current, Superseded: data.Superseded, Unassociated: data.Unassociated,
		ObsoleteReclaimable: cleanup.Human(uint64(max64(data.ObsoleteReclaimable, 0))), ObsoleteKnown: data.ObsoleteKnown,
		Stats: data.Stats, Services: data.Services,
	})
}
func (server *Server) apiTorrents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": server.inv.TorrentSnapshot(), "refreshing": server.inv.IsRefreshing()})
}
func (server *Server) apiUnmanaged(w http.ResponseWriter, r *http.Request) {
	x, u, e := server.inv.UnmanagedSnapshot()
	writeJSON(w, map[string]any{"items": x, "updated": u, "error": errString(e), "refreshing": server.inv.IsRefreshing()})
}
func (server *Server) apiFiles(w http.ResponseWriter, r *http.Request) {
	x, mr, tr, u, e := server.inv.FileSnapshot()
	writeJSON(w, map[string]any{"items": x, "mediaRefs": mr, "torrentRefs": tr, "updated": u, "error": errString(e)})
}
func (server *Server) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if server.tasks != nil {
		server.tasks.RunAsyncApp("inventory")
	} else {
		go func() {
			if e := server.inv.Refresh(context.Background()); e != nil {
				log.Printf("[inventory] refresh: %v", e)
			}
		}()
	}
	http.Redirect(w, r, "/", 303)
}
func (server *Server) plan(w http.ResponseWriter, r *http.Request) {
	x, _, _ := server.inv.Snapshot()
	t := server.inv.TorrentSnapshot()
	reliability := server.inv.ReliabilitySnapshot()
	views := server.deviceViews(x, t, server.planningReliable(reliability))
	plans := make([]cleanup.Plan, 0, len(views))
	for _, view := range views {
		plans = append(plans, view.Plan)
	}
	writeJSON(w, plans)
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
