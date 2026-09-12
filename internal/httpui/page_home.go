package httpui

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"stewarr/internal/inventory"
	"stewarr/internal/model"
	"stewarr/internal/store"
)

type homeData struct {
	Devices              []deviceView
	Reliability          inventory.Reliability
	Updated              time.Time
	LastErr              error
	Refreshing           bool
	TotalMedia           int
	HasMovieLibrary      bool
	Movies               int
	HasSeriesLibrary     bool
	Series               int
	LibraryBytes         int64
	TotalTorrents        int
	Current              int
	Superseded           int
	Orphaned             int
	Unassociated         int
	ObsoleteReclaimable  int64
	ObsoleteKnown        int
	UnmanagedFiles       int
	UnmanagedBytes       int64
	UnmanagedReclaimable int64
	UnmanagedAvailable   bool
	UnmanagedError       string
	Services             []inventory.ServiceStatus
	Stats                store.CleanupStats
}

func (server *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	d := server.dashboardSnapshot()
	if e := renderTemplate(w, server.homeTpl, d); e != nil {
		log.Printf("[http] render home: %v", e)
	}
}

func (server *Server) dashboardSnapshot() homeData {
	revision := server.revisions.current().Revision
	server.homeMu.Lock()
	defer server.homeMu.Unlock()
	if server.homeRevision == revision {
		return server.homeCache
	}
	items, updated, last := server.inv.Snapshot()
	projection := server.pendingProjection()
	items = projection.filterMedia(items)
	ts := projection.filterTorrents(server.inv.TorrentSnapshot())
	reliability := server.inv.ReliabilitySnapshot()
	planningReliable := server.planningReliable(reliability)
	cfg := server.inv.Config()
	d := homeData{
		Updated: updated, LastErr: last, Refreshing: server.inv.IsRefreshing(), Reliability: reliability, TotalMedia: len(items), Devices: server.deviceViews(items, ts, planningReliable),
		HasMovieLibrary: len(cfg.ServicesOfType("radarr")) > 0, HasSeriesLibrary: len(cfg.ServicesOfType("sonarr")) > 0,
	}
	if !planningReliable && d.LastErr == nil {
		if server.tasks != nil && server.tasks.ConsistencyPending() {
			d.LastErr = fmt.Errorf("Automatic removal planning paused. Post-removal synchronization is pending")
		} else {
			d.LastErr = fmt.Errorf("Automatic removal planning paused. Jellyfin: %s; Seerr: %s; File topology: %s", reliability.Jellyfin, reliability.Seerr, reliability.FileModel)
		}
	}
	for _, svc := range server.inv.StatusSnapshot() {
		if svc.Configured {
			d.Services = append(d.Services, svc)
		}
	}
	for _, m := range items {
		d.LibraryBytes += m.SizeBytes
		if m.Type == model.Movie {
			d.Movies++
		} else if m.Type == model.Series {
			d.Series++
		}
	}
	d.TotalTorrents = len(ts)
	for _, t := range ts {
		switch normalizeTorrentStatus(t.AssociationStatus) {
		case model.TorrentCurrent:
			d.Current++
		case model.TorrentSuperseded:
			d.Superseded++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += t.ReclaimableBytes
			}
		case model.TorrentOrphaned:
			d.Orphaned++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += t.ReclaimableBytes
			}
		default:
			d.Unassociated++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += t.ReclaimableBytes
			}
		}
	}
	ufs, unmanagedUpdated, ue := server.inv.UnmanagedSnapshot()
	ufs = projection.filterUnmanaged(ufs)
	applyUnmanagedSummary(&d, ufs, unmanagedUpdated, ue)
	if db := server.inv.Store(); db != nil {
		d.Stats, _ = db.CleanupStatistics()
	}
	server.homeRevision = revision
	server.homeCache = d
	return d
}

func applyUnmanagedSummary(d *homeData, files []model.UnmanagedFile, updated time.Time, scanErr error) {
	if scanErr != nil {
		d.UnmanagedError = scanErr.Error()
		return
	}
	if updated.IsZero() {
		// No successful scan has completed yet. This is a normal startup state,
		// not an error and not an empty authoritative result.
		return
	}
	d.UnmanagedAvailable = true
	d.UnmanagedFiles = len(files)
	for _, f := range files {
		d.UnmanagedBytes += f.SizeBytes
		d.UnmanagedReclaimable += f.ReclaimableBytes
	}
}
