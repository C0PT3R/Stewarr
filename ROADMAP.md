# Connarr Roadmap

This file separates implemented behavior from intended direction. It is not a promise of release dates.

## Implemented through 0.2.9

### Reactive UI state

- Server-rendered Go templates enhanced by pinned, embedded htmx and Stimulus;
  Connarr has no CDN, SPA, or mandatory frontend build service.
- Cached, revisioned dashboard state with SSE invalidation and conditional
  five-second polling fallback. Storage sampling is independent and no UI
  refresh calls an integration or starts a filesystem scan.
- Stable open lists: background additions and reorderings show **Updates
  available**, while removals always disappear immediately. Filters, paging,
  focus, scroll, disclosures, and list layout survive ordinary updates.
- Home, Library, Torrents, Unclaimed, Tasks, History, and object details update
  as bounded fragments instead of reloading the page.
- Persistent operation status and failures, accessibility announcements,
  keyboard/backdrop modal dismissal, reduced-motion support, and duplicate
  action prevention.

### Removal interaction and projection

- Removal plans inspect topology once. Every checkbox consequence is calculated
  locally within one browser frame; selection makes no HTTP or filesystem call.
- Confirmation durably journals and schedules work, then returns 202 Accepted.
  The scheduler performs authoritative owner/filesystem revalidation inside its
  exclusive mutation boundary, independently of the browser connection.
- A central pending-operation projection suppresses selected Media, Torrents,
  managed files, and Unclaimed files across every view. Successful mutations
  remain suppressed until consistency reconciliation catches up; failures
  restore the object and expose a persistent History-linked error.
- Detail pages become durable operation-result views instead of reloading into
  a 404 after their object is removed.
- Actual Movie/Episode filenames, plain season disclosures, local group
  selection, hardlink guidance, optional unmonitoring, and Radarr/Sonarr
  import-list exclusions.
- File-level series selection supersedes the old partial-series removal roadmap
  item; there is no separate series-wide removal primitive to add.

### Torrent relationships and Value

- Current torrent states are **Current**, **Superseded**, and **Unassociated**.
  The legacy Orphaned state migrates to Unassociated while its former media
  identity remains provenance History.
- Current and historical relationships project bidirectionally between Media
  and Torrents without fuzzy title matching.
- Authoritative current import provenance and proven device/inode physical
  backing independently establish a Current relationship. Physical proof can
  therefore correct stale or absent provenance; neither source is allowed to
  demote the other.
- Torrent health contributes to Media Value only when the relationship is
  Current and reconciled device/inode identity proves the torrent and media
  paths are hardlinks. Copies, unknown topology, and provenance alone
  contribute nothing; the Value explanation states the hardlink requirement.
- Torrent Value remains an independent explainable swarm-retention signal.

### Scheduler and safety (0.2.9)

- Durable event-driven scheduler with trigger/execution identity, coverage-aware
  coalescing, FIFO tie-breaking, fixed-rate catch-up, bounded aging, retries,
  cooldowns, cooperative preemption, and injected-clock tests.
- Shared/exclusive resource arbitration with exact wait reasons and generic
  durable workflows.
- Immediate post-removal Base inventory → File reconciliation workflow with
  scheduler coalescing and a five-minute consistency deadline.
- Browser-independent durable mutations, restart reconciliation, and attention
  rather than blind replay for uncertain irreversible work.
- Dry-run-by-default removal, owner-backed mutations, final-boundary live
  ownership/device/inode revalidation, and fail-closed Unclaimed discovery.

### Model and operations

- First-class generic File model with separate media/torrent ownership and
  physical device/inode/link-count identity.
- Generation-bound atomic inventory/file publication and topology-derived
  reclaimability.
- Home, Library, Torrent, Unclaimed, Tasks, and History views; server-side
  search/filter/sort/pagination; SQLite state and legacy DB migration.
- Radarr, Sonarr, Jellyfin, Seerr, and qBittorrent adapters with small durable
  index data and lazy source-owned detail loading.
- Owner-scoped removals that reject cross-owner fields at admission and again
  inside scheduled execution.
- Isolated integration authority: Jellyfin enrichment requires no shared path
  namespace, topology is informational, and cross-owner mutations fail closed.
- Unmanaged file inventory is read-only until a cleanup root is explicitly
  delegated to Connarr.
- Mandatory origin-labelled application logging to stdout and daily persistent
  files, with ten-day retention and fail-stop behavior.
- Complete line-oriented removal audits keyed by durable operation identity.
- One terminal log summary per scheduler execution, with reconciliation mode,
  stage timings, path counts, and torrent cache/fetch counts when relevant.
- Exact removal scopes durably merge across overlapping mutations and drive targeted
  owner, path, physical-peer, projection, and SQLite reconciliation.
- Missing, uncertain, partial, or invariant-breaking scopes automatically
  promote to the complete Base inventory → File reconciliation path.
- Removal disclosure and History details that distinguish physical-file count,
  path count, selected paths, and preserved paths.
- Media removal presents its Movie/Season/Episode files and lists every Current
  or Superseded torrent exactly once. Physically backing Current torrents are
  selected by default, other Current torrents remain optional, Superseded
  torrents remain preserved context, and the physical action graph synchronizes
  selections without becoming the primary UI.
- PUID/PGID-friendly Docker deployment and centralized Connarr product identity.

## Near-term

- Make task schedules configurable through the GUI.
- Add richer task execution history and reconciliation diagnostics.
- Add useful Unclaimed filters.
- Improve explicit provenance-change reasons and per-object historical
  timelines.
- Expand the single-path storage model into explicit storage pools.
- Build a non-destructive reclamation planner across Library, Torrents, and
  Unclaimed data.
- Continue readability work outside the HTTP/UI files touched through 0.2.8.

## Later

- Multiple Radarr/Sonarr/qBittorrent instances and a GUI integration manager
  with capability discovery and connection testing.
- Lidarr, Readarr, Bazarr, Transmission, Deluge, rTorrent, Plex, and other
  integration adapters.
- Per-storage-pool targets, alarms, acquisition inhibition, and explicitly
  configured emergency behavior.
- User-defined retention preferences and torrent-retention rules.
- Safe automatic cleanup policy and observed reclamation verification.
- Filesystem-specific shared-storage inspectors for reflinks/extents, ZFS,
  Windows, and network filesystems where reliable.
- Cross-stack diagnostics, repair workflows, global search, and per-item
  timelines.
- Quality-versus-storage-cost reasoning and upgrade/downgrade recommendations.
- Webhook/event adapters where integrations expose useful reliable events.

## Product direction

Connarr should become the missing coordination layer in a modular media stack:
not another specialized media manager, but the place where facts from
specialized tools gain cross-stack context and purpose.
