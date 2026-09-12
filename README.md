# Stewarr

> **Your media stack, together.**

Stewarr is a coordination and storage-intelligence layer for self-hosted media stacks. It currently integrates Radarr, Sonarr, Jellyfin, Seerr and qBittorrent, correlates their data, assigns Library media **Retention Value**, tracks torrent provenance, derives hardlink-aware reclaimable storage from the reconciled File topology, and discovers download data that no current torrent claims.

Stewarr does not try to replace the applications it integrates with. Services provide facts; Stewarr provides context across them.

> **Status:** Stewarr is a work in progress. Core flows (inventory, removal planning, storage accounting) are functional and used daily, but interfaces and configuration may still change between releases. See `CHANGELOG.md` for release history and `ROADMAP.md` for planned direction.

## Quickstart

```yaml
services:
  stewarr:
    build: https://github.com/C0PT3R/stewarr.git
    container_name: stewarr
    restart: unless-stopped
    user: "${PUID:-1000}:${PGID:-1000}"
    ports:
      - "8088:8088"
    volumes:
      - ./config:/config
      - /mnt/media:/data
```

1. Create a `config` directory next to your compose file, writable by the UID/GID you run the container as.
2. Copy [`config.example.json`](config.example.json) into it as `config.json` and fill in your services' URLs and API keys.
3. `docker compose up -d`, then open `http://<host>:8088`.

See [Configuration](#configuration) and [Docker](#docker) below for details.

## Current version

`0.4.8`

There is no configured global storage path. Stewarr derives its known
storage devices from the roots each service already discovers on its
own, grouping roots that resolve to the same physical device. Home shows one
usage graphic per device, broken down by which service's files occupy
it, with Unmanaged and unattributed real usage kept as separate, honestly
labeled segments rather than forced to match; Target/Critical reclamation
thresholds apply independently to every device.

Background and mutation work now runs on the scheduler engine described in
`Scheduler-Spec.md`: durable trigger/execution identity, coverage-aware
coalescing, shared/exclusive resource arbitration, retries, and generic
workflows replace the previous ad hoc scheduling. Removal admission and the
post-removal Base inventory → File reconciliation workflow are built on it,
and no legacy dirty loop, polling wait, or hardcoded task ID remains.

Filesystem identity and import provenance now form one coherent relationship
model. A torrent is Current when an authoritative current import says so or
when reconciled device/inode identity proves that it physically backs current
managed media. Superseded is retained as historical context only when no
current relationship exists; Unassociated means neither source establishes a
current relationship. Strict distinct-path hardlink proof remains required for
torrent health to affect media Retention Value.

Media removal again uses the compact Movie/Season/Episode hierarchy. It lists
each Current or Superseded torrent exactly once: physically backing Current
torrents are selected by default, other Current torrents are optional, and
Superseded torrents are preserved context. The browser synchronizes every
coupled action locally while paths and physical details stay collapsed under
**Files and storage**. The modal header and actions remain visible while its
content scrolls.

## Navigation

- **Home** — storage state, Library/Torrent summaries, service health, storage capabilities and lifetime cleanup statistics.
- **Library** — searchable, filterable, sortable, server-paginated Radarr/Sonarr media ranked by Retention Value.
- **Torrents** — searchable, filterable, sortable, server-paginated qBittorrent inventory with provenance and reclaimable-space information.
- **Unmanaged files** — observational inventory of paths that no current service claims; not directly removable.
- **Tasks** — background maintenance tasks, their schedules/status, and **Run now** controls.
- **History** — durable event history. Removal simulations and live removal outcomes are recorded here.


## Manual removal and dry run

Manual Removal is available from discreet trash actions on Media and Torrent views. Every operation is first materialized as a `RemovalPlan`. The plan re-stats only affected known paths, calculates hardlink-aware consequences, groups known owner links as physical-file selection units, and is rebuilt immediately before execution.

```json
"removal": {
  "dry_run": true
}
```

`removal.dry_run` defaults to **true**. In dry-run mode Stewarr performs no destructive owner API calls; confirming a plan records the simulation in History. Setting it to `false` enables owner-backed execution. Managed Media is removed only through Radarr/Sonarr and Torrent data only through qBittorrent. Unmanaged files are removed by direct OS deletion — the only removal kind not delegated to a service, since nothing claims them — with the same admission, revalidation, and physical-identity re-verification every other removal kind goes through.

Every confirmed operation is written durably with status `queued` before
scheduler admission and advances to `started` before any mutation. Once execution begins it uses an application-owned bounded context,
so closing or disconnecting the browser cannot cancel it halfway through. The
same History row is finalized with the outcome. On restart, safely queued work
is reconciled with durable scheduler state; uncertain started work is recorded
as `interrupted` and is not blindly replayed.

Removal confirmation shows actual storage reclaimed for the current selection.
When a media file is physically proven to share device/inode identity with a
torrent path, selecting either visible object updates the same internal action
graph and the torrent is selected by default. Selecting a torrent means
removing that torrent's complete data set. It is shown once in the torrent
section rather than repeated beneath every episode it backs. Unknown external
hardlinks remain preserved, keep reclaimability at zero, and are reported
explicitly.

A Torrent may be **Unassociated** with managed Media while its files remain claimed by qBittorrent. `Unassociated` is therefore a Torrent provenance state. `Unmanaged` means only that Stewarr currently knows no owner claim; it is neither ownership nor deletion authority.

Manual removal operates on owner-backed managed files rather than deleting Radarr/Sonarr Media objects. Movies normally collapse to one managed-file action; series can be selected by season/episode-file groups. Torrent-originated plans only expose managed-file actions when Stewarr can prove the file correspondence. Manual managed-file removal can optionally unmonitor affected Radarr movies or Sonarr episodes; monitoring is otherwise left unchanged.

Because removal deletes files through Radarr/Sonarr's own file-delete API rather than deleting the Movie/Series record, an emptied series or movie folder is left on disk until Radarr/Sonarr cleans it up themselves. Enable **Settings → Media Management → Delete empty folders** in Radarr/Sonarr so they remove now-empty folders as part of that same file-delete call; otherwise Jellyfin (or any other library scanner) keeps indexing the leftover empty folder.

## Persistent state

SQLite lives at the fixed path:

```text
/config/state/inventory.db
```

The database path is intentionally not configurable.

## Application logs

Every application log entry is written both to container stdout and to a mandatory daily file under `/config/log`:

```text
/config/log/stewarr-YYYY-MM-DD.log
```

The date follows the container's local timezone. Stewarr appends across restarts on the same day, opens a new file at local midnight, and retains the latest ten daily files. It refuses to start if the log directory or current file cannot be opened. A runtime write or rollover failure stops the application with a non-zero exit instead of continuing without its audit trail.

Lines identify their origin, such as `[app]`, `[http]`, `[inventory]`, `[scheduler]`, and `[removal]`. Removal lines also carry `[operation=N]`. Each removal audit records the authoritative plan, every selected and preserved path, physical identity facts, warnings, options, owner actions, results, errors, and terminal status. Credentials, cookies, API keys, passwords, and browser operation tokens are never written.

`/config` must be writable by the container user because SQLite may create WAL/journal files beside the database.

## Scheduler and background tasks

Stewarr currently exposes independent maintenance tasks:

- **Base inventory** — uses `server.refresh_interval` (default `30m`) for Radarr, Sonarr, qBittorrent and incremental import provenance.
- **Jellyfin enrichment** — runs every **12 hours** for playback and favorite facts.
- **Seerr enrichment** — runs every **hour** for request facts.
- **File reconciliation** — runs every **12 hours** and indexes authoritative file ownership and filesystem identity.

The Unmanaged-files **Scan** action invokes the same File reconciliation task; it does not bypass scheduler exclusivity.

Post-removal consistency uses the durable mutation scope only when every
required invariant remains exact. It otherwise promotes itself to a full scan
and includes the promotion reason in the scheduler execution summary.

The scheduler coordinates startup, periodic, manual, workflow, retry, and live
mutation triggers. Every accepted trigger receives an identity and becomes an
execution, attaches to sufficiently fresh active work, or explicitly coalesces
into one successor. Fixed-rate periodic schedules produce at most one catch-up
run after downtime. Durable correctness work survives restart.

Tasks declare shared or exclusive resource claims. This permits explicitly safe
overlap while serializing conflicting publication and owner/filesystem work.
Mutation priority may cooperatively interrupt only tasks that declare themselves
interruptible; resources remain held until the runner actually exits. Waiting,
backoff, cancellation, priority, workflow progress, and exact resource conflicts
are projected on the Tasks page.

A successful or uncertain live mutation immediately advances one durable
post-removal consistency workflow: Base inventory, then File reconciliation.
Overlapping removals coalesce or create one necessary successor while retaining
the five-minute deadline from the first unsatisfied revision. A qualifying
scheduled or manual execution may satisfy a step. Jellyfin and Seerr enrichment
remain outside this workflow, and automatic planning stays inhibited until it
succeeds.

## Retention Value

Retention Value is a retention score for Library media. Higher means more valuable to retain when storage becomes scarce. Current inputs include rating, watch activity, library age, request state, popularity, favorites/keep tags and torrent activity. Torrent activity contributes only when the torrent is both **Current** for that media and its files are physically proven to be hardlinked to the media files.

Retention Value is deliberately separate from storage size. Storage planning can later consider actual reclaimable bytes independently of Retention Value.

### Swarm Value

Torrents have an independent Swarm Value based on current swarm facts. The initial explainable model uses logarithmic swarm seeds, swarm leechers and live upload rate. It deliberately does not inherit media Retention Value or storage cost: those are separate dimensions. Swarm Value is shown and sortable now, but it does not yet drive cleanup decisions.

## Torrent provenance

Stewarr relates qBittorrent torrents to media using authoritative Radarr/Sonarr import-history `downloadId` values matched to torrent hashes and reconciled filesystem identity. It does not establish ownership from fuzzy title matching.

Current torrent relationship states are:

- **Current** — an authoritative current import relates the torrent to managed Library media, or reconciled device/inode identity proves that it physically backs the media.
- **Superseded** — the torrent backed an older imported release that was replaced by a later release.
- **Unassociated** — neither current provenance nor physical topology establishes a current media relationship. Stewarr may still retain a former relationship in History; that historical fact is not a fourth current state and is not evidence that the torrent is safe to remove.

Torrent reclaimability is derived from the reconciled File topology rather than by walking torrent content directories during normal inventory refresh. The same device+inode+link-count model therefore explains hardlinked, copied, and separate-device layouts consistently.


## File model

Stewarr treats files as first-class, general storage objects rather than assuming that a media item or torrent is itself a file. The durable relationship is:

```text
Media -> Files <-> Files <- Torrent
```

A `File` is intentionally generic enough to represent any regular file a future service may own (video, subtitle, ebook, text, and so on). Current mutation owners are Radarr movie files, Sonarr episode files, and qBittorrent torrent files. Jellyfin independently contributes playback and favourite facts; it is not required to share Stewarr's filesystem namespace. Ownership is stored separately from path-level filesystem facts, and relationships never create transitive mutation rights.

File reconciliation records only path, size, existence, modification time, device, inode and hardlink count. It does not hash content, inspect codecs, or crawl the full storage tree. Media and torrent detail pages show a bounded preview of reconciled files, same-physical-data peers, and hypothetical unlink effects. `/api/files` exposes the indexed file and ownership records. Reclaimability is derived from the File model: unlinking one side of a hardlink pair reclaims 0 B, while unlinking every link to the inode reclaims the inode size.

## Unmanaged file inventory

Stewarr can discover regular files under reconciled storage roots that no current service claims. This state is **Unmanaged**, not a third ownership category: a file is either managed by a service or it isn't.

The scan fails closed: it must successfully retrieve the authoritative owner inventories before absence is reported. If an owner inventory fails, previous successful results are retained and the scan is marked unavailable. Even a complete absence of claims does not grant Stewarr ownership.

Hardlinks are grouped by device+inode to explain physical storage potential. No Unmanaged path can be removed directly in this release.

Browse results at:

```text
/unmanaged
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
  "device_thresholds": {
    "/data": { "target_usage_percent": 90, "critical_usage_percent": 95 }
  }
}
```

There is no configured storage path. Stewarr derives its known storage
devices from the roots each service already discovers on its own
(Radarr/Sonarr root folders, qBittorrent save paths); roots that resolve to
the same physical device are grouped into one device. Each device's
reclamation thresholds are configured independently, keyed by that device's
representative root path — a device with no entry defaults to 90%/95%.
Home shows one usage bar per device, broken down by which service's files occupy it, with
Target/Critical applied independently to each device.

Target is the operational reclamation threshold and return point per device.
Critical is independent and reserved for a future emergency policy such as
alerting or pausing new downloads; it does not activate or gate cleanup
planning.

A device Stewarr cannot measure is reported as **UNAVAILABLE** and produces
no cleanup plan; other devices are unaffected.

## Docker

Example Compose:

```yaml
services:
  stewarr:
    build: .
    container_name: stewarr
    restart: unless-stopped
    user: "${PUID:-1000}:${PGID:-1000}"
    ports:
      - "8088:8088"
    volumes:
      - ./config:/config
      - /mnt/media:/data
```

The bind-mounted `config` directory should already exist on the host and be writable by the UID/GID used to run the container.

## Configuration

See [`config.example.json`](config.example.json).

Services use named instance records in `services[]`. Each instance has a stable internal identity derived independently of its display name, so a future rename does not redefine ownership. `Unmanaged` is reserved as a synthetic owner label. Legacy singleton `radarr`, `sonarr`, `jellyfin`, `seerr`, and `qbittorrent` configuration blocks are still accepted and migrated in memory.

Only service types with a defined adapter are accepted. Current adapters
discover every storage root they require from their authoritative APIs, so
`root_path` is not accepted. Future adapters may participate in non-File
features without exposing storage capabilities at all.

## Development

```bash
go test ./...
go build ./cmd/stewarr
```

The real-browser reactivity suite uses Playwright and an installed Chromium:

```bash
PLAYWRIGHT_NODE_MODULES=/path/to/node_modules make test-browser
```

Deployment defaults:

```makefile
REMOTE ?= user@your-server
REMOTE_DIR ?= ./servarr/stewarr
```

Override them as needed:

```bash
make deploy REMOTE=myuser@myserver REMOTE_DIR=/opt/stewarr
```

## Current safety posture

Stewarr remains non-destructive by default because `removal.dry_run` defaults to `true`. Cleanup statistics distinguish **Library bytes removed** from **actual filesystem bytes reclaimed**, because hardlinks and other forms of shared storage mean those values are not necessarily equal.

Unsupported or uncertain storage capabilities must remain explicitly unavailable rather than being guessed.

## Origins

Stewarr began as a storage-pressure cleanup experiment called **Spartarr**. Its first proposed name was the regrettable **Shovitupyoarr**. As it outgrew cleanup-only scope it became **Togetharr**, then **Stewarr** when a shorter name proved preferable. Product identity is centralized in code because this may not be the final rename.

Spartarr discovered the problem. Togetharr broadened the purpose. Stewarr is the current name.

### Data loading policy

Stewarr keeps routine inventory refreshes and its durable SQLite model deliberately small. List/index data, provenance, relationships, valuation inputs, sync cursors, and expensive reconciliation results are retained; cheap source-owned diagnostics are lazy-loaded on detail pages. Torrent detail pages now fetch full qBittorrent diagnostics on demand instead of relying on persisted copies. Media with no files remain part of the media model but are hidden from Library by default; use **Show media with no files** to include them.

## License

MIT — see [`LICENSE`](LICENSE).
