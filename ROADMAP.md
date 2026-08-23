# Togetharr Roadmap

This file separates implemented behavior from intended direction. It is not a promise of release dates.

## Implemented through 0.1.21

- Adopted cheap scans / small durable model / lazy details as an architecture invariant.
- Torrent detail diagnostics are fetched live from qBittorrent and are not persisted as durable index state.
- Media with no files are retained as media; Library hides them by default with an explicit opt-in checkbox.
- Library default ordering is now title ascending rather than lowest-value Value first.

- Home dashboard.
- Library profiles and Value valuation.
- Radarr and Sonarr inventory/history integration.
- Jellyfin activity integration.
- Seerr request integration.
- qBittorrent incremental synchronization and detailed torrent pages.
- Torrent provenance: Associated / Superseded / Orphaned / Unassociated.
- File-topology-derived reclaimability for torrent content, including hardlinked/shared data.
- Unclaimed download-data detection with fail-closed ownership inventory.
- Hardlink-aware reclaimability for unclaimed files.
- First-class generic File model with separate media-file and torrent-file ownership relations.
- Same-physical-data relationships derived from device+inode and exposed in detail views.
- Media-only, torrent-only, and combined unlink/reclaim estimates derived from File topology.
- Sparse file reconciliation task with filesystem existence/device/inode/link-count enrichment.
- Media and torrent detail pages expose reconciled file previews; `/api/files` exposes the indexed file graph.
- Search, filtering, sorting and server-side pagination for Library/Torrents; search/sort/pagination for unclaimed data.
- SQLite durable state and legacy Spartarr DB filename migration.
- Cleanup-run/statistics persistence schema and History UI (no destructive execution yet).
- Storage capability reporting.
- Tasks page with Run now.
- Inventory refresh and unclaimed scans separated into independent schedules.
- PUID/PGID-friendly Docker deployment.
- Canonical project name: Togetharr.

## Near-term

- Make Target the sole operational storage threshold; demote/remove Critical from normal cleanup logic.
- Make task schedules configurable through the GUI.
- Improve task run history/status and add full reconciliation task.
- Reuse/carry forward reconciled torrent-file ownership so unclaimed scans can eventually avoid refetching all file lists when nothing changed.
- Add filters to Unclaimed downloads where useful.
- Expand storage-pool modeling beyond a single configured path.
- Improve provenance for explicit deletion reasons and richer historical timelines.
- Build non-destructive storage reclamation planning across Library, torrents and unclaimed data.

## Later

- Multiple instances of Radarr/Sonarr/qBittorrent and other integrations.
- GUI integration manager with connection testing and capability discovery.
- Lidarr, Readarr, Bazarr, Transmission, Deluge, rTorrent and Plex support.
- Per-storage-pool targets and pressure planning.
- Torrent retention valuation independent of Library Value.
- Safe automatic cleanup policies and actual cleanup execution.
- Detailed cleanup statistics/history and observed reclamation verification.
- Filesystem-specific shared-storage inspectors (reflinks/extents/ZFS/Windows/network filesystems where reliable).
- Cross-stack diagnostics and repair workflows.
- Global cross-integration search and per-item timelines.
- Quality-vs-storage-cost reasoning and upgrade/downgrade recommendations.
- Event/webhook support where integrations expose useful reliable events.

## Product direction

Togetharr should become the missing coordination layer in a modular media stack: not another specialized media manager, but the place where facts from specialized tools gain cross-stack context and purpose.

- Media detail torrent relationships are grouped by current/superseded/orphaned provenance and display torrent release names.


## Manual Removal foundation (0.1.16)

- Mandatory RemovalPlan for Media, Torrent, and Unclaimed File removal.
- Dry-run-by-default execution gate.
- Owner-driven managed removals; direct OS removal only for confirmed Unclaimed Files.
- Durable removal History events.
- Multi-select/Select-all for Unclaimed Files and linked torrents.
- Future dry-run cleanup and automation must reuse the same RemovalPlan/executor primitives rather than creating parallel destructive paths.


## 0.1.16 UI/removal normalization

- Shared application chrome/AppInfo across primary and detail pages.
- RemovalPlan runs as a blocking overlay without browser-history mutations.
- Media with no managed files is non-removable at planner level and shown with a disabled trash action.
- Torrent RemovalPlans expose only managed files whose correspondence to torrent files is proven; they never widen a torrent relationship to an entire Media object.
- Unclaimed files support per-item trash actions and bulk Remove with disabled empty selection.
- Operational UI hides unsupported or unconfigured capability clutter where practical.

## 0.1.17 removal/UX cleanup

- Removal targets are selectable rather than mandatory; empty selections cannot execute.
- Media removal availability is based on the full removal graph, so Media with no current files can still expose removable related Torrents.
- Operational UI follows two rules: show facts, choices, and consequences rather than implementation machinery; omit capabilities irrelevant to the current configuration/topology.
- Torrent and Library filters apply directly, inventory summaries no longer interrupt filter/result flow, and internal/debug prose is reduced across primary pages.


## 0.1.21 stabilization

- Fixed automatic Library, Torrents, and Unclaimed filter submission.
- Clear appears only when actual filters are active; pagination is not a filter.
- Normalized the Library filter strip, especially the no-files toggle.
- Removal target selection is separate from linked-object Select all behavior.
- Partial-series torrent removal scope remains intentionally deferred to the next iteration.

## 0.1.21 managed-file removal model

- Media is removal context/grouping, not an executable removal primitive.
- Managed storage removal is atomic at `MediaFileRef` and delegated to Radarr MovieFile / Sonarr EpisodeFile APIs.
- Sonarr file reconciliation stores season/episode grouping metadata for episode files.
- Media-originated plans may select all or only part of a Media's managed files.
- Torrent-originated plans may offer only managed files proven to be the same physical data as torrent files; filenames are never accepted as proof.
- Torrent removal remains independently selectable, so copied torrent data can be reclaimed while managed Media files remain.
- Manual managed-file plans keep monitoring unchanged by default and can optionally unmonitor affected Radarr movies or Sonarr episodes.
