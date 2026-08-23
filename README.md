# Togetharr

> **Your media stack, together.**

Togetharr is a coordination and storage-intelligence layer for self-hosted media stacks. It currently integrates Radarr, Sonarr, Jellyfin, Seerr and qBittorrent, correlates their data, assigns Library media **Value**, tracks torrent provenance, derives hardlink-aware reclaimable storage from the reconciled File topology, and discovers download data that no current torrent claims.

Togetharr does not try to replace the applications it integrates with. Integrations provide facts; Togetharr provides context across them.

## Current version

`0.1.29`

This release serializes all SQLite writes so inventory refresh, File reconciliation, removal history and cleanup bookkeeping cannot interleave transactions on the shared database connection. A failed refresh continues to preserve the last successfully published inventory, which remains available to File reconciliation.

## Navigation

- **Home** — storage state, Library/Torrent summaries, service health, storage capabilities and lifetime cleanup statistics.
- **Library** — searchable, filterable, sortable, server-paginated Radarr/Sonarr media ranked by Value.
- **Torrents** — searchable, filterable, sortable, server-paginated qBittorrent inventory with provenance and reclaimable-space information.
- **Unclaimed downloads** — files under qBittorrent save roots that no current torrent claims.
- **Tasks** — background maintenance tasks, their schedules/status, and **Run now** controls.
- **History** — durable event history. Removal simulations and live removal outcomes are recorded here.


## Manual removal and dry run

Manual Removal is available from discreet trash actions on Media and Torrent views, and as multi-select removal on **Unclaimed downloads**. Every operation is first materialized as a `RemovalPlan`. The plan re-stats only the affected known paths, calculates actual hardlink-aware reclaimability, exposes linked objects, and is re-built immediately before execution.

```json
"removal": {
  "dry_run": true
}
```

`removal.dry_run` defaults to **true**. In dry-run mode Togetharr performs no destructive owner API calls and no filesystem removals; confirming a plan records the simulation in History. Setting it to `false` enables manual execution. Managed Media is removed only through Radarr/Sonarr, Torrent data only through qBittorrent, and direct OS removal is restricted to files that are still confirmed **Unclaimed** at plan revalidation.

A Torrent may be **Unassociated** with managed Media while its files remain claimed by qBittorrent. `Unassociated` is therefore a Torrent provenance state. `Unclaimed` is a File/storage state and never means “safe to delete” automatically. Unclaimed files are manual-only.

## Persistent state

SQLite lives at the fixed path:

```text
/config/state/togetharr.db
```

The database path is intentionally not configurable. On first start after upgrading from Spartarr, Togetharr automatically migrates `/config/state/spartarr.db` to the new filename when the Togetharr database does not yet exist.

`/config` must be writable by the container user because SQLite may create WAL/journal files beside the database.

## Background tasks

Togetharr currently exposes independent maintenance tasks:

- **Inventory refresh** — uses `server.refresh_interval` (default `30m`). It refreshes Library data, Jellyfin/Seerr state, incremental *arr history, qBittorrent state, provenance and Value.
- **File reconciliation** — runs every **12 hours** and can be triggered manually from `/tasks`. It indexes authoritative Radarr/Sonarr/qBittorrent file ownership and enriches known paths with filesystem identity without crawling unrelated storage.
- **Unclaimed download scan** — runs every **12 hours** and can be triggered manually from `/tasks`. This expensive filesystem/torrent-file inventory no longer blocks normal refreshes.

The Tasks page is the foundation for future configurable schedules and additional maintenance/reconciliation jobs.

## Media Value

Media Value is a retention value for Library media. Higher means more valuable to retain when storage becomes scarce. Current inputs include rating, watch activity, library age, request state, popularity, favorites/keep tags and torrent activity.

Value is deliberately separate from storage size. Storage planning can later consider actual reclaimable bytes independently of value.

### Torrent Value

Torrents have an independent Value based on current swarm facts. The initial explainable model uses logarithmic swarm seeds, swarm leechers and live upload rate. It deliberately does not inherit Media Value or storage cost: those are separate dimensions. Torrent Value is shown and sortable now, but it does not yet drive cleanup decisions.

## Torrent provenance

Togetharr associates qBittorrent torrents with media using authoritative Radarr/Sonarr import-history `downloadId` values matched to torrent hashes. It does not establish ownership from fuzzy title matching.

Current provenance states are:

- **Associated** — the torrent backs current Library media.
- **Superseded** — the torrent backed an older imported release that was replaced by a later release.
- **Orphaned** — the torrent was imported historically, but no current Library media claims that import.
- **Unassociated** — Togetharr cannot prove an association. This is not evidence that the torrent is safe to remove.

Torrent reclaimability is derived from the reconciled File topology rather than by walking torrent content directories during normal inventory refresh. The same device+inode+link-count model therefore explains hardlinked, copied, and separate-device layouts consistently.


## File model

Togetharr treats files as first-class, general storage objects rather than assuming that a media item or torrent is itself a file. The durable relationship is:

```text
Media -> Files <-> Files <- Torrent
```

A `File` is intentionally generic enough to represent any regular file a future integration may own (video, subtitle, ebook, text, and so on). Current ownership sources are Radarr movie files, Sonarr episode files, and qBittorrent torrent files. Ownership is stored separately from path-level filesystem facts, so one path can be referenced by multiple logical sources without duplicating the file record.

File reconciliation records only path, size, existence, modification time, device, inode and hardlink count. It does not hash content, inspect codecs, or crawl the full storage tree. Media and torrent detail pages show a bounded preview of reconciled files, same-physical-data peers, and hypothetical unlink effects. `/api/files` exposes the indexed file and ownership records. Reclaimability is derived from the File model: unlinking one side of a hardlink pair reclaims 0 B, while unlinking every link to the inode reclaims the inode size.

## Unclaimed download data

Togetharr can discover regular files under qBittorrent save roots that are not claimed by any current torrent.

The scan fails closed: it must successfully retrieve the authoritative file list for **every current torrent** before absence is interpreted as unclaimed ownership. If any torrent file list fails, the previous successful results are retained and the scan is marked unavailable.

Hardlinks are grouped by device+inode. An inode is only counted as reclaimable when all of its filesystem links are included in the unclaimed set.

Browse results at:

```text
/downloads/unclaimed
```

## Search, filtering, sorting and pagination

Library and Torrents process lists in this order:

```text
search + filters
      ↓
sort
      ↓
paginate
```

Default page size is 50, with 25 / 50 / 100 / 250 choices.

Library filters currently include media type, requested state, watched state and torrent presence. Torrent filters include provenance, reclaimability and activity.

## Storage

Example:

```json
"storage": {
  "path": "/data",
  "target_usage_percent": 90,
  "critical_usage_percent": 95
}
```

The current 0.1.x planner still uses Critical as the activation threshold and Target as the return point. The agreed future model is simpler: **Target should become the operational threshold**, while Critical—if retained at all—should only represent urgency/alerting.

If `storage.path` cannot be measured, Togetharr reports storage as **UNAVAILABLE** and does not produce a cleanup plan.

## Docker

Example Compose:

```yaml
services:
  togetharr:
    build: .
    container_name: togetharr
    restart: unless-stopped
    user: "${PUID:-1000}:${PGID:-1000}"
    ports:
      - "8088:8088"
    volumes:
      - ./config:/config
      - /mnt/media:/data:ro
```

The bind-mounted `config` directory should already exist on the host and be writable by the UID/GID used to run the container.

## Configuration

See [`config.example.json`](config.example.json).

Integrations use named instance records in `integrations[]`. Each instance has a stable internal identity derived independently of its display name, so a future rename does not redefine ownership. `Unclaimed` is reserved as a synthetic owner label. Legacy singleton `radarr`, `sonarr`, `jellyfin`, `seerr`, and `qbittorrent` configuration blocks are still accepted and migrated in memory.

Storage roots are integration-owned facts when the integration can expose them. For integration types that cannot expose roots, `root_path` is required as a configuration fallback.

## Development

```bash
go test ./...
go build ./cmd/togetharr
```

Deployment defaults:

```makefile
REMOTE ?= user@your-server
REMOTE_DIR ?= ./servarr/togetharr
```

Override them as needed:

```bash
make deploy REMOTE=myuser@myserver REMOTE_DIR=/opt/togetharr
```

## Current safety posture

Togetharr remains non-destructive by default because `removal.dry_run` defaults to `true`. Cleanup statistics distinguish **Library bytes removed** from **actual filesystem bytes reclaimed**, because hardlinks and other forms of shared storage mean those values are not necessarily equal.

Unsupported or uncertain storage capabilities must remain explicitly unavailable rather than being guessed.

## Origins

Togetharr began as a storage-pressure cleanup experiment called **Spartarr**. Its first proposed name was the regrettable **Shovitupyoarr**. The project then outgrew the cleanup-only concept and became focused on bringing fragmented media-stack knowledge together.

Spartarr discovered the problem. Togetharr is the product that emerged from it.


### Data loading policy

Togetharr keeps routine inventory refreshes and its durable SQLite model deliberately small. List/index data, provenance, relationships, valuation inputs, sync cursors, and expensive reconciliation results are retained; cheap source-owned diagnostics are lazy-loaded on detail pages. Torrent detail pages now fetch full qBittorrent diagnostics on demand instead of relying on persisted copies. Media with no files remain part of the media model but are hidden from Library by default; use **Show media with no files** to include them.



## 0.1.11 media relationship/UI corrections

- Media detail pages now receive torrent relationships bidirectionally from both current and historical provenance. Superseded and orphaned torrents therefore appear on the media they previously backed.
- Historical torrents are visible context only for media Value; torrent-activity Value uses current associated torrents only.
- The Torrents table shows historical media for superseded/orphaned torrents instead of an unexplained dash when provenance is known.
- The media detail page was compacted into a denser profile layout with summary panels followed by full-width Files and Torrents sections.

## 0.1.10 UI
Media detail pages group torrent relationships into current, superseded, and historical/orphaned sections, with release names shown as the primary identifier.

## 0.1.13 File topology

- File is now the storage bridge between Media and Torrent ownership.
- Same physical data is derived from filesystem device+inode identity and exposed on media/torrent detail pages.
- Media pages show the storage effect of unlinking media files alone versus media plus current torrent files.
- Torrent pages show the storage effect of unlinking torrent files.
- Torrent reclaimability now uses the reconciled File topology; the old per-torrent directory-walking inspector was removed.
- Normal inventory refresh therefore reuses the latest File reconciliation snapshot and does not perform storage walks for torrent reclaimability.

### Managed-file removal

Manual removal now operates on owner-backed managed files rather than deleting Radarr/Sonarr Media objects. Movies normally collapse to one managed-file action; series can be selected by season/episode-file groups. Torrent-originated plans only expose managed-file actions when Togetharr can prove the file correspondence.

Manual managed-file removal can optionally unmonitor affected Radarr movies or Sonarr episodes; monitoring is otherwise left unchanged.
