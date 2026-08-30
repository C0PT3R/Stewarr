# Connarr

> **0.2.10 storage devices:** There is no configured global storage path. Connarr derives known storage devices from the roots each integration already discovers on its own, and Home shows one usage graphic per device broken down by which integration's files occupy it.

> **0.2.9 scheduler rewrite:** Background and mutation work now runs on a durable, domain-neutral engine with trigger/execution identity, coverage-aware coalescing, resource arbitration, and workflow-driven post-removal consistency. See `Scheduler-Spec.md` for the full contract.

> **0.2.8 relationship and removal clarity:** Proven physical backing promotes a torrent to Current, and media removal presents the complete Current/Superseded set without exposing the physical graph as the primary interface.

> **Your media stack, together.**

Connarr is a coordination and storage-intelligence layer for self-hosted media stacks. It currently integrates Radarr, Sonarr, Jellyfin, Seerr and qBittorrent, correlates their data, assigns Library media **Value**, tracks torrent provenance, derives hardlink-aware reclaimable storage from the reconciled File topology, and discovers download data that no current torrent claims.

Connarr does not try to replace the applications it integrates with. Integrations provide facts; Connarr provides context across them.

## Current version

`0.2.10`

There is no configured global storage path. Connarr derives its known
storage devices from the roots each integration already discovers on its
own, grouping roots that resolve to the same physical device. Home shows one
usage graphic per device, broken down by which integration's files occupy
it, with Unclaimed and unattributed real usage kept as separate, honestly
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
torrent health to affect Media Value.

Media removal again uses the compact Movie/Season/Episode hierarchy. It lists
each Current or Superseded torrent exactly once: physically backing Current
torrents are selected by default, other Current torrents are optional, and
Superseded torrents are preserved context. The browser synchronizes every
coupled action locally while paths and physical details stay collapsed under
**Files and storage**. The modal header and actions remain visible while its
content scrolls.

## Navigation

- **Home** — storage state, Library/Torrent summaries, service health, storage capabilities and lifetime cleanup statistics.
- **Library** — searchable, filterable, sortable, server-paginated Radarr/Sonarr media ranked by Value.
- **Torrents** — searchable, filterable, sortable, server-paginated qBittorrent inventory with provenance and reclaimable-space information.
- **Unmanaged files** — observational inventory of paths that no current integration claims; not directly removable.
- **Tasks** — background maintenance tasks, their schedules/status, and **Run now** controls.
- **History** — durable event history. Removal simulations and live removal outcomes are recorded here.


## Manual removal and dry run

Manual Removal is available from discreet trash actions on Media and Torrent views. Every operation is first materialized as a `RemovalPlan`. The plan re-stats only affected known paths, calculates hardlink-aware consequences, groups known owner links as physical-file selection units, and is rebuilt immediately before execution.

```json
"removal": {
  "dry_run": true
}
```

`removal.dry_run` defaults to **true**. In dry-run mode Connarr performs no destructive owner API calls; confirming a plan records the simulation in History. Setting it to `false` enables owner-backed execution. Managed Media is removed only through Radarr/Sonarr and Torrent data only through qBittorrent. Direct OS removal of Unmanaged files is disabled until a future cleanup root is explicitly delegated to Connarr.

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

A Torrent may be **Unassociated** with managed Media while its files remain claimed by qBittorrent. `Unassociated` is therefore a Torrent provenance state. `Unmanaged` means only that Connarr currently knows no owner claim; it is neither ownership nor deletion authority.

## Persistent state

SQLite lives at the fixed path:

```text
/config/state/connarr.db
```

The database path is intentionally not configurable. On first start after upgrading, Connarr automatically migrates `/config/state/togetharr.db` or the still-older `/config/state/spartarr.db` when the Connarr database does not yet exist. If both legacy files exist, the Togetharr database takes precedence; legacy files that are not selected are left untouched.

## Application logs

Every application log entry is written both to container stdout and to a mandatory daily file under `/config/log`:

```text
/config/log/connarr-YYYY-MM-DD.log
```

The date follows the container's local timezone. Connarr appends across restarts on the same day, opens a new file at local midnight, and retains the latest ten daily files. It refuses to start if the log directory or current file cannot be opened. A runtime write or rollover failure stops the application with a non-zero exit instead of continuing without its audit trail.

Lines identify their origin, such as `[app]`, `[http]`, `[inventory]`, `[scheduler]`, and `[removal]`. Removal lines also carry `[operation=N]`. Each removal audit records the authoritative plan, every selected and preserved path, physical identity facts, warnings, options, owner actions, results, errors, and terminal status. Credentials, cookies, API keys, passwords, and browser operation tokens are never written.

`/config` must be writable by the container user because SQLite may create WAL/journal files beside the database.

## Scheduler and background tasks

Connarr currently exposes independent maintenance tasks:

- **Base inventory** — uses `server.refresh_interval` (default `30m`) for Radarr, Sonarr, qBittorrent and incremental import provenance.
- **Jellyfin enrichment** — runs every **12 hours** for playback and favorite facts.
- **Seerr enrichment** — runs every **hour** for request facts.
- **File reconciliation** — runs every **12 hours** and indexes authoritative file ownership and filesystem identity.

The Unclaimed-files **Scan** action invokes the same File reconciliation task; it does not bypass scheduler exclusivity.

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

## Media Value

Media Value is a retention value for Library media. Higher means more valuable to retain when storage becomes scarce. Current inputs include rating, watch activity, library age, request state, popularity, favorites/keep tags and torrent activity. Torrent activity contributes only when the torrent is both **Current** for that media and its files are physically proven to be hardlinked to the media files.

Value is deliberately separate from storage size. Storage planning can later consider actual reclaimable bytes independently of value.

### Torrent Value

Torrents have an independent Value based on current swarm facts. The initial explainable model uses logarithmic swarm seeds, swarm leechers and live upload rate. It deliberately does not inherit Media Value or storage cost: those are separate dimensions. Torrent Value is shown and sortable now, but it does not yet drive cleanup decisions.

## Torrent provenance

Connarr relates qBittorrent torrents to media using authoritative Radarr/Sonarr import-history `downloadId` values matched to torrent hashes and reconciled filesystem identity. It does not establish ownership from fuzzy title matching.

Current torrent relationship states are:

- **Current** — an authoritative current import relates the torrent to managed Library media, or reconciled device/inode identity proves that it physically backs the media.
- **Superseded** — the torrent backed an older imported release that was replaced by a later release.
- **Unassociated** — neither current provenance nor physical topology establishes a current media relationship. Connarr may still retain a former relationship in History; that historical fact is not a fourth current state and is not evidence that the torrent is safe to remove.

Torrent reclaimability is derived from the reconciled File topology rather than by walking torrent content directories during normal inventory refresh. The same device+inode+link-count model therefore explains hardlinked, copied, and separate-device layouts consistently.


## File model

Connarr treats files as first-class, general storage objects rather than assuming that a media item or torrent is itself a file. The durable relationship is:

```text
Media -> Files <-> Files <- Torrent
```

A `File` is intentionally generic enough to represent any regular file a future integration may own (video, subtitle, ebook, text, and so on). Current mutation owners are Radarr movie files, Sonarr episode files, and qBittorrent torrent files. Jellyfin independently contributes playback and favourite facts; it is not required to share Connarr's filesystem namespace. Ownership is stored separately from path-level filesystem facts, and relationships never create transitive mutation rights.

File reconciliation records only path, size, existence, modification time, device, inode and hardlink count. It does not hash content, inspect codecs, or crawl the full storage tree. Media and torrent detail pages show a bounded preview of reconciled files, same-physical-data peers, and hypothetical unlink effects. `/api/files` exposes the indexed file and ownership records. Reclaimability is derived from the File model: unlinking one side of a hardlink pair reclaims 0 B, while unlinking every link to the inode reclaims the inode size.

## Unmanaged file inventory

Connarr can discover regular files under reconciled storage roots that no current integration claims. The legacy `/downloads/unclaimed` route remains for compatibility, but the product state is **Unmanaged**.

The scan fails closed: it must successfully retrieve the authoritative owner inventories before absence is reported. If an owner inventory fails, previous successful results are retained and the scan is marked unavailable. Even a complete absence of claims does not grant Connarr ownership.

Hardlinks are grouped by device+inode to explain physical storage potential. No Unmanaged path can be removed directly in this release.

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
  "target_usage_percent": 90,
  "critical_usage_percent": 95
}
```

There is no configured storage path. Connarr derives its known storage
devices from the roots each integration already discovers on its own
(Radarr/Sonarr root folders, qBittorrent save paths); roots that resolve to
the same physical device are grouped into one device. Home shows one usage
bar per device, broken down by which integration's files occupy it, with
Target/Critical applied independently to each device.

Target is the operational reclamation threshold and return point per device.
Critical is independent and reserved for a future emergency policy such as
alerting or pausing new downloads; it does not activate or gate cleanup
planning.

A device Connarr cannot measure is reported as **UNAVAILABLE** and produces
no cleanup plan; other devices are unaffected.

## Docker

Example Compose:

```yaml
services:
  connarr:
    build: .
    container_name: connarr
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

Integrations use named instance records in `integrations[]`. Each instance has a stable internal identity derived independently of its display name, so a future rename does not redefine ownership. `Unclaimed` is reserved as a synthetic owner label. Legacy singleton `radarr`, `sonarr`, `jellyfin`, `seerr`, and `qbittorrent` configuration blocks are still accepted and migrated in memory.

Only integration types with a defined adapter are accepted. Current adapters
discover every storage root they require from their authoritative APIs, so
`root_path` is not accepted. Future adapters may participate in non-File
features without exposing storage capabilities at all.

## Development

```bash
go test ./...
go build ./cmd/connarr
```

The real-browser reactivity suite uses Playwright and an installed Chromium:

```bash
PLAYWRIGHT_NODE_MODULES=/path/to/node_modules make test-browser
```

Deployment defaults:

```makefile
REMOTE ?= user@your-server
REMOTE_DIR ?= ./servarr/connarr
```

Override them as needed:

```bash
make deploy REMOTE=myuser@myserver REMOTE_DIR=/opt/connarr
```

## Current safety posture

Connarr remains non-destructive by default because `removal.dry_run` defaults to `true`. Cleanup statistics distinguish **Library bytes removed** from **actual filesystem bytes reclaimed**, because hardlinks and other forms of shared storage mean those values are not necessarily equal.

Unsupported or uncertain storage capabilities must remain explicitly unavailable rather than being guessed.

## Origins

Connarr began as a storage-pressure cleanup experiment called **Spartarr**. Its first proposed name was the regrettable **Shovitupyoarr**. As it outgrew cleanup-only scope it became **Togetharr**, then **Connarr** when a shorter name proved preferable. Product identity is centralized in code because this may not be the final rename.

Spartarr discovered the problem. Togetharr broadened the purpose. Connarr is the current name.


### Data loading policy

Connarr keeps routine inventory refreshes and its durable SQLite model deliberately small. List/index data, provenance, relationships, valuation inputs, sync cursors, and expensive reconciliation results are retained; cheap source-owned diagnostics are lazy-loaded on detail pages. Torrent detail pages now fetch full qBittorrent diagnostics on demand instead of relying on persisted copies. Media with no files remain part of the media model but are hidden from Library by default; use **Show media with no files** to include them.



## 0.1.11 media relationship/UI corrections

- Media detail pages receive torrent relationships bidirectionally from both current and historical provenance. Superseded and formerly related torrents therefore appear on the media they previously backed.
- Historical torrents are visible context only for media Value; torrent activity contributes only from current, physically hardlinked torrents.
- The Torrents table shows historical media for superseded or formerly related torrents instead of an unexplained dash when provenance is known.
- The media detail page was compacted into a denser profile layout with summary panels followed by full-width Files and Torrents sections.

## 0.1.10 UI
Media detail pages group torrent relationships into current, superseded, and unassociated sections, with former relationships identified as historical context and release names shown as the primary identifier.

## 0.1.13 File topology

- File is now the storage bridge between Media and Torrent ownership.
- Same physical data is derived from filesystem device+inode identity and exposed on media/torrent detail pages.
- Media pages show the storage effect of unlinking media files alone versus media plus current torrent files.
- Torrent pages show the storage effect of unlinking torrent files.
- Torrent reclaimability now uses the reconciled File topology; the old per-torrent directory-walking inspector was removed.
- Normal inventory refresh therefore reuses the latest File reconciliation snapshot and does not perform storage walks for torrent reclaimability.

### Managed-file removal

Manual removal now operates on owner-backed managed files rather than deleting Radarr/Sonarr Media objects. Movies normally collapse to one managed-file action; series can be selected by season/episode-file groups. Torrent-originated plans only expose managed-file actions when Connarr can prove the file correspondence.

Manual managed-file removal can optionally unmonitor affected Radarr movies or Sonarr episodes; monitoring is otherwise left unchanged.
