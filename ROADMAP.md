# Connarr Roadmap

This file separates implemented behavior from intended direction. It is not a promise of release dates.

## Implemented through 0.2.12

### Reactive UI state

- Server-rendered Go templates enhanced by pinned, embedded htmx and Stimulus;
  Connarr has no CDN, SPA, or mandatory frontend build service.
- Cached, revisioned dashboard state with SSE invalidation and conditional
  five-second polling fallback. Storage sampling is independent and no UI
  refresh calls an integration or starts a filesystem scan.
- Stable open lists: background additions and reorderings show **Updates
  available**, while removals always disappear immediately. Filters, paging,
  focus, scroll, disclosures, and list layout survive ordinary updates.
- Home, Library, Torrents, Unmanaged, Tasks, History, and object details update
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
  managed files, and Unmanaged files across every view. Successful mutations
  remain suppressed until consistency reconciliation catches up; failures
  restore the object and expose a persistent History-linked error.
- Detail pages become durable operation-result views instead of reloading into
  a 404 after their object is removed.
- Actual Movie/Episode filenames, plain season disclosures, local group
  selection, hardlink guidance, optional unmonitoring, and Radarr/Sonarr
  import-list exclusions.
- File-level series selection supersedes the old partial-series removal roadmap
  item; there is no separate series-wide removal primitive to add.

### Torrent relationships and Retention/Swarm Value

- Current torrent states are **Current**, **Superseded**, and **Unassociated**.
  The legacy Orphaned state migrates to Unassociated while its former media
  identity remains provenance History.
- Current and historical relationships project bidirectionally between Media
  and Torrents without fuzzy title matching.
- Authoritative current import provenance and proven device/inode physical
  backing independently establish a Current relationship. Physical proof can
  therefore correct stale or absent provenance; neither source is allowed to
  demote the other.
- Torrent health contributes to media Retention Value only when the
  relationship is Current and reconciled device/inode identity proves the
  torrent and media paths are hardlinks. Copies, unknown topology, and
  provenance alone contribute nothing; the Retention Value explanation states
  the hardlink requirement.
- Swarm Value remains an independent explainable swarm-retention signal.

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
  ownership/device/inode revalidation, and fail-closed Unmanaged discovery.

### Storage devices (0.2.10)

- No configured global storage path. Known storage devices are derived from
  the roots each integration already discovers on its own, grouped by
  physical device when multiple roots share one disk.
- Home shows one usage graphic per known device, broken down by which
  integration's files occupy it, with Unmanaged and unattributed real usage
  kept as separate, honestly-labeled segments rather than forced to match.
- Target/Critical reclamation thresholds apply independently to every known
  device; a device Connarr cannot measure is reported Unavailable without
  affecting others.

### Cross-domain cleanup planning (0.2.11)

- Each device's Cleanup plan reasons across Library media and Torrents
  together, not Media alone. A Media item physically hardlinked to a Current
  torrent is bundled with it into one action that is proposed or withheld as
  a unit, since removing only one side would free none of the shared bytes
  while still discarding real value. A Current torrent proven to be a
  separate copy remains fully independent; one whose hardlink status is not
  yet proven is never proposed alone, the same conservative rule as a proven
  hardlink.
- Torrent-domain candidates (Superseded, Unassociated, or independent
  Current copies) are always proposed before any Media-domain candidate on
  the same device. Media Retention Value and Torrent Swarm Value are
  deliberately unrelated scores and are never compared numerically; only
  this tier order decides which domain is tried first.
- Unmanaged files are never a candidate source for this planner, under any
  circumstance — this is a permanent exclusion, not a low priority.
- Shipped as planning computation only, with automatic execution added
  separately in 0.2.12 below.

### Torrent protection, automatic execution, and season-level Value (0.2.12)

- Torrents gain the same absolute Protection state Media already has:
  a configured minimum ratio and an explicit keep-tag both exclude a torrent
  from the planner entirely, regardless of storage pressure. A protected
  torrent hardlinked to a media item vetoes that whole bundle, not just
  itself, since removing the bundle would still delete files it needs.
- A global auto-removal switch and a per-integration opt-in flag gate real,
  unattended execution of the cross-domain Cleanup plan; both default to
  false, so a fresh or upgraded install does nothing automatically. A
  bundled action spanning integrations with mixed opt-in status is skipped
  entirely, never partially executed. Automatic submissions travel through
  the exact same admission/durable-history/scheduler path as a manual
  browser removal, so `removal.dry_run` and every existing safety mechanism
  (live final-boundary revalidation included) apply identically regardless
  of what triggered the removal.
- Evaluation runs as its own periodic task reading only already-published
  inventory state, independent of the 12-hour File reconciliation task,
  since storage pressure can appear from swarm activity alone well within
  that window.
- Series media gain per-season Retention Value instead of one score for an
  entire series: series-wide factors (rating, keep tag, favorite, request
  state) apply identically to every season, plus season-specific recency and
  torrent activity scoped to whichever season a hardlinked torrent's files
  actually belong to. The planner proposes individual old seasons of an
  otherwise-kept show instead of only ever reasoning about a whole series;
  a movie, or a series with no season data yet, is unaffected.

### Live config editing and multi-instance integrations (0.2.13-0.2.28)

- Integrations move from a static, must-exist-before-launch config file to
  live, in-app management: adding, editing (including moving an integration to
  an entirely new URL without severing the data already attributed to it), and
  removing an integration all validate a real connection before persisting,
  then activate immediately without disturbing in-memory reconciliation state.
  A removed integration's files become Unmanaged rather than disappearing.
- Radarr, Sonarr, and qBittorrent each support more than one configured
  instance simultaneously (separate quality-tier libraries, a seedbox
  alongside a local client); every reconciliation, import-history, and
  removal-execution path fans out per instance and disambiguates identity by
  a stable per-integration ID rather than assuming one adapter per type.
  Jellyfin and Seerr remain single-instance by design, matching how they're
  actually deployed in practice.
- Media and torrent removal, and the Library/Torrent detail pages, carry an
  explicit `integration_id` end to end so an ambiguous match (the same
  Radarr/Sonarr source ID, or the same torrent infohash, existing in more than
  one configured instance) fails closed instead of silently guessing.
- Adding, editing, or removing an integration schedules an immediate
  inventory-then-files consistency pass (the same chain a removal triggers)
  instead of waiting for the next periodic file reconciliation — the actual
  point of moving storage-path discovery into the app in the first place:
  add an integration and its paths show up promptly, without a restart or an
  hours-long wait.
- File reconciliation no longer fails on a fresh install with zero
  integrations configured — that's an expected starting state, not a
  misconfiguration, and now publishes an empty-but-reliable file topology
  instead of erroring. The Home page's Services card always shows (with an
  empty state instead of disappearing entirely), and its Library card only
  shows a Movies/Series row for a library that's actually configured. The
  Add/Edit integration forms show only the credential fields the selected
  type actually uses (an API key, or a username+password, never both).

### App-wide slowness during reconciliation publishing (0.2.29)

- Fixed a serious pre-existing bug: reconcileFiles, the inline delta
  reconciliation inside Refresh, and reconcileTargeted all held the
  in-memory state lock (the one every page load needs just to read cached
  data) for the entire duration of a database write. Any one of these
  publishing — which happens routinely, not just on a rare full scan —
  stalled every page in the app for as long as that write took, with no
  corresponding CPU/disk/memory signal to explain it. A dedicated lock now
  serializes reconciliation publishers against each other without ever being
  held by a reader, so a slow write no longer blocks anything but itself.
  This was a real, worthwhile fix, but turned out not to be the cause of a
  concurrently-reported "pages sometimes take a minute" symptom — see below.

### Browser-side connection exhaustion from a leaked reconnecting EventSource (0.2.31)

- Fixed the actual cause of the reported app-wide slowness: the reactive
  UI's EventSource (`/ui/events`) fell back to polling on a connection error
  but never called `.close()` on the errored object first. Per spec, a
  dropped EventSource that isn't explicitly closed keeps retrying to
  reconnect in the background forever, even after the app has already
  switched to polling — and every silent retry still consumes one of the
  browser's ~6 connections-per-origin. Left running long enough on a single
  tab (hours open, one network blip), those leaked reconnect loops alone
  can exhaust the pool, and every subsequent request to the origin — any
  page, any asset — queues forever behind them ("Stalled" in the browser's
  own network timing, not a server-side delay at all: no CPU/disk/memory
  signal, nothing blocked in a goroutine dump, because the request never
  even reached the server). Fixed by explicitly closing and clearing the
  EventSource before falling back to polling. Added a permanent regression
  test (`tests/ui/reactivity.mjs`) that forces a connection failure and
  asserts no further reconnect attempts occur.
- That fix alone turned out to be insufficient: it only runs when the
  browser actually fires an `error` event, but a connection can also go
  silently dead (a NAT mapping timing out, a network path change) with
  neither side ever seeing an error — left open indefinitely from both
  ends' point of view, so the fix's own cleanup code never executes at all.
  Closed the gap (0.2.32) by having the server voluntarily end and rotate
  every `/ui/events` connection on a bounded lifetime (3 minutes) regardless
  of whether anything looks wrong, so a silently-dead connection is always
  eventually replaced. Because EventSource has no way to distinguish an
  intentional server-side close from a real failure — both fire the same
  `error` event — the client no longer treats a single error as fatal: it
  now retries with capped exponential backoff (up to 5 attempts) before
  falling back to polling, so routine rotations reconnect near-instantly
  and only genuine, sustained failures degrade to polling. Verified with a
  new Go test (`TestSSEEndsOnItsOwnAfterMaxLifetime`) and an extended
  browser regression test asserting the retry count climbs and then
  plateaus rather than staying flat or growing forever.

### Model and operations

- First-class generic File model with separate media/torrent ownership and
  physical device/inode/link-count identity.
- Generation-bound atomic inventory/file publication and topology-derived
  reclaimability.
- Home, Library, Torrent, Unmanaged, Tasks, and History views; server-side
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
- Add useful Unmanaged filters.
- Improve explicit provenance-change reasons and per-object historical
  timelines.
- Make Target/Critical reclamation thresholds independently configurable per
  storage device rather than one global percentage applied to every device.
- Continue readability work outside the HTTP/UI files touched through 0.2.8.

## Later

- Capability discovery for the integration manager (auto-detecting what an
  added integration supports, beyond the connection test already in place).
- Lidarr, Readarr, Bazarr, Transmission, Deluge, rTorrent, Plex, and other
  integration adapters.
- Per-storage-device alarms, acquisition inhibition, and explicitly
  configured emergency behavior.
- Per-tracker torrent retention rules (e.g. a hit-and-run window, or a ratio
  floor that varies by tracker rather than one global value) on top of the
  global ratio floor and keep-tag protection already implemented (0.2.12).
  `Torrent.Tracker` is already a reconciled field; this is a policy layer on
  existing data, not new integration work.
- Observed reclamation verification: confirming after automatic execution
  that the predicted bytes were actually freed, not only that the plan ran.
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
