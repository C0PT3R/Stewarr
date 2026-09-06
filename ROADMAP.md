# Connarr Roadmap

This file separates implemented behavior from intended direction. It is not a promise of release dates.

## Implemented through 0.2.12

### Reactive UI state

- Server-rendered Go templates enhanced by pinned, embedded htmx and Stimulus;
  Connarr has no CDN, SPA, or mandatory frontend build service.
- Cached, revisioned dashboard state with SSE invalidation and conditional
  five-second polling fallback. Storage sampling is independent and no UI
  refresh calls a service or starts a filesystem scan.
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
  the roots each service already discovers on its own, grouped by
  physical device when multiple roots share one disk.
- Home shows one usage graphic per known device, broken down by which
  service's files occupy it, with Unmanaged and unattributed real usage
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
- A global auto-removal switch and a per-service opt-in flag gate real,
  unattended execution of the cross-domain Cleanup plan; both default to
  false, so a fresh or upgraded install does nothing automatically. A
  bundled action spanning services with mixed opt-in status is skipped
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

### Live config editing and multi-instance services (0.2.13-0.2.28)

- Services move from a static, must-exist-before-launch config file to
  live, in-app management: adding, editing (including moving a service to
  an entirely new URL without severing the data already attributed to it), and
  removing a service all validate a real connection before persisting,
  then activate immediately without disturbing in-memory reconciliation state.
  A removed service's files become Unmanaged rather than disappearing.
- Radarr, Sonarr, and qBittorrent each support more than one configured
  instance simultaneously (separate quality-tier libraries, a seedbox
  alongside a local client); every reconciliation, import-history, and
  removal-execution path fans out per instance and disambiguates identity by
  a stable per-service ID rather than assuming one adapter per type.
  Jellyfin and Seerr remain single-instance by design, matching how they're
  actually deployed in practice.
- Media and torrent removal, and the Library/Torrent detail pages, carry an
  explicit `service_id` end to end so an ambiguous match (the same
  Radarr/Sonarr source ID, or the same torrent infohash, existing in more than
  one configured instance) fails closed instead of silently guessing.
- Adding, editing, or removing a service schedules an immediate
  inventory-then-files consistency pass (the same chain a removal triggers)
  instead of waiting for the next periodic file reconciliation — the actual
  point of moving storage-path discovery into the app in the first place:
  add a service and its paths show up promptly, without a restart or an
  hours-long wait.
- File reconciliation no longer fails on a fresh install with zero
  services configured — that's an expected starting state, not a
  misconfiguration, and now publishes an empty-but-reliable file topology
  instead of erroring. The Home page's Services card always shows (with an
  empty state instead of disappearing entirely), and its Library card only
  shows a Movies/Series row for a library that's actually configured. The
  Add/Edit service forms show only the credential fields the selected
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
- Still not the whole story: this app has no TLS in front of it, so
  browsers only ever talk HTTP/1.1 to it (cleartext HTTP/2 exists, but no
  mainstream browser will negotiate it for a normal page load — only
  TLS-based HTTP/2 gets multiplexing), which means every tab is capped at
  ~6 connections to the origin at once. A single page load already used
  most of that budget on its own assets (HTML + CSS + 3 separate scripts +
  the EventSource), so fast navigation between pages — each opening a
  fresh EventSource on load — could transiently pile an old page's
  not-yet-closed connection on top of a new page's requests and exceed the
  cap, independent of the dead-connection issue above. Closed this (0.2.33)
  two ways: the `revisions` controller now force-closes its EventSource on
  `pagehide`, which fires synchronously ahead of a full-document
  navigation's teardown (Stimulus's own `disconnect()` isn't guaranteed to
  run in time, since the whole document — DOM, JS heap, MutationObserver —
  gets discarded as part of the navigation itself); and htmx, Stimulus, and
  the app bundle are now merged into a single `app.js` by
  `tools/buildassets` instead of three separate `<script>` tags, freeing up
  two connection slots on every page load. Verified with an isolated
  Playwright harness proving the old page's SSE connection is observed
  closed server-side before/at the point the new page's connection opens.

### Terminology rename, per-device thresholds, and a two-step service setup (0.2.34-0.2.35)

- "Integration" is renamed to "service" everywhere: Go identifiers
  (`config.Service`, `Media.ServiceID`/`ServiceName`/`ServiceType`,
  `inventory.Service.AddService`/`EditService`/`RemoveService`), JSON field
  names (`serviceId`, the config file's own `services[]` key), routes
  (`/services/create`), templates, and docs. The SQLite `integration_name`
  column is now `service_name` — a real schema change with no migration
  written, matching this project's established convention for its one real
  deployment. A pre-existing, unrelated `Service{URL, APIKey}` convenience
  struct (`Config.Radarr`/`.Sonarr`/`.Jellyfin`/`.Seerr`) collided with the
  renamed type and was renamed to `Connection` instead.
- Target/Critical reclamation thresholds move from one global percentage
  applied to every storage device to a per-device setting
  (`Storage.DeviceThresholds`, keyed by a device's stable representative
  path — `config.ThresholdsFor`/`SetDeviceThreshold`), closing the
  long-standing "Near-term" item of the same name. Editable directly from
  the Storage page, once a device actually exists — an earlier version of
  this work tried to collect a root path and thresholds during the
  Add-service overlay itself, before any of a service's storage roots are
  known, but storage roots are always discovered by each service's own
  adapter and `validateService` rejects a hand-entered `root_path` outright
  for every real service type. Thresholds only ever make sense to set
  *after* discovery, never during initial setup.
- The Add service overlay now runs a real, non-persisting connection check
  (`inventory.Service.TestServiceConnection`) before revealing the actual
  "Add service" submit button, instead of testing and saving in one atomic
  step — cheap, real feedback before committing anything.
- Saving a service now opens a second, automatic step: a progress view
  showing which stage of the inventory-and-files-consistency workflow is
  running (`/services/consistency-status`, backed by the task manager's
  existing per-workflow step tracking — no new progress-reporting
  infrastructure needed), with an indeterminate animated bar rather than a
  fabricated percentage, since the filesystem-walk stage has no known total
  ahead of time. Closing it early leaves the scan running in the
  background; the global `#operation-indicator` (already used for pending
  removals) now also reflects an in-flight consistency scan, reusing the
  same reactive SSE mechanism rather than adding a second UI element.

### Category-aware empty states and dynamic Library filters (0.2.36-0.2.37)

- The Torrents and Library pages no longer render an empty, filterable
  list with a generic "nothing matches" row when the relevant service type
  isn't configured at all — Torrents shows "No torrent client registered"
  (gated on `cfg.ServicesOfType("qbittorrent")`), Library shows "No media
  library registered" (gated on Radarr or Sonarr), each with an
  Add-service button scoped to that category.
- The Add-service overlay gained a `category` concept (`torrentclient`,
  `medialibrary`) purely for presentation — it narrows the Type select and
  changes the heading/submit label ("Add torrent client", "Add media
  library"), so a future Transmission/Deluge or Lidarr/Readarr adapter
  only needs adding to the relevant category's type list, not a new
  overlay. Fixed a real bug this surfaced: credential-field visibility
  (API key vs username/password) was a static template default assuming
  Radarr is always first, which broke once a category could default to
  qBittorrent — now synced whenever the overlay opens, not only on a
  manual type change.
- The Library page's Type filter is now derived from the types actually
  present (not a hardcoded Movie/Series pair), future-proofing for
  Lidarr/Readarr, and disappears entirely when the library only ever
  holds one type. A new Source filter (`ServiceName` or `ServiceName ·
  rootLabel`, reusing the same suppression rule as the removal plan's
  `displayPath` so a single-root service never shows a redundant "Radarr
  · Radarr") appears only when more than one source exists library-wide,
  with its options scoped to whichever Type is currently selected. The
  Torrent filter follows the same never-show-a-no-op-filter rule: it's
  hidden (and the filter forced closed server-side, not just visually)
  when no torrent-client-type service is configured, since no media item
  could ever have a torrent to filter by.

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
- Isolated service authority: Jellyfin enrichment requires no shared path
  namespace, topology is informational, and cross-owner mutations fail closed.
- Unmanaged files can be selected and removed directly from the Unmanaged
  page (see below) — this replaces an earlier read-only-until-a-cleanup-root
  state.
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

### Overlay/UI polish and removal fixes (0.2.41-0.2.44)

- Overlay dialogs now size to their actual content instead of always
  filling ~90% of the viewport height, capping/scrolling internally only
  when content genuinely needs it.
- The shared overlay CSS/DOM naming (`.removal-overlay`/`.removal-dialog`/
  `.removal-scroll`, `#removal-modal`) is renamed to `.modal-overlay`/
  `.modal-dialog`/`.modal-scroll`/`#modal-root` — it backs every overlay
  in the app (removal, add/edit service, service setup progress), not
  just removals, and the old name was misleading.
- The scan-in-progress indicator in the top nav now links to `/tasks`
  instead of `/history`, since `/history` only shows completed operations.
- Fixed a real regression from the earlier SSE connection-pool fix: every
  EventSource reconnect (the server's own periodic rotation, or a flaky
  connection retrying) replays the current revision, which was only ever
  suppressed on a page's very first connection — later reconnects treated
  that replay as a genuine change and could resurface "Updates available"
  on Library/Torrents with nothing actually new to show. Fixed by
  tracking the actual revision number instead of a one-time flag, so only
  a strictly newer revision counts as real.
- Each Home dashboard card links to exactly one place ("View"), so the
  whole card is now the click target (a "stretched link" via `::after`,
  scoped to `#home-dashboard` since `.card`/`.card-link` are reused
  elsewhere for things that must not become full-card links), with a
  hover highlight to signal it.
- Fixed the Storage page crashing ("reflect: slice index out of range")
  whenever a plan contained a `StandaloneSeason` action — its cleanup-actions
  list only branched on "bundle"/"media" kinds, treating anything else as a
  torrent action and indexing into an empty `Torrents` slice.
- Removing a torrent hardlinked to a service-owned library file freed no
  space at all, since the shared blocks stay allocated until that file is
  also removed — but the torrent removal view never disclosed this
  relationship. The backend already computed it (`RelatedManaged`), but a
  template guard excluded rendering it specifically for torrent-kind
  plans, making that whole code path dead. Since a torrent-kind submission
  must never directly mutate a media-owned file (`validateRemovalScope`
  forbids `managed_file` there — owned objects are only ever manipulated
  through their own service), this is a disclosure-only section — the
  same pattern already used for hardlinked Unmanaged paths — warning that
  removing the torrent alone won't reclaim the space, with a link to the
  media so the user can remove it from there instead.

### Unmanaged file removal re-enabled (0.2.45)

- Standalone Unmanaged file removal — direct OS deletion, since nothing
  claims these files — was disabled during an earlier refactoring pass
  (all removal kinds were disabled at once and re-enabled one at a time as
  each was re-verified; Unmanaged was the last one still off). Re-enabling
  it surfaced real gaps beyond the one deliberate `validateRemovalScope`
  rejection: `groupRemovalFiles` had no switch case at all for an
  Unmanaged-kind plan's own target files, and the initial candidate never
  set `Selectable: true` — together these meant every Unmanaged target
  would have rendered as "Preserved · not owned by this operation" and
  been unselectable even with the rejection lifted. The GET overlay
  handler (`removalUnmanaged`) was also a hardcoded 403 stub, never
  calling the plan builder at all.
- The Unmanaged page's removal UI didn't just need re-enabling — it had
  been fully stripped (no checkboxes, no trash button) and the leftover
  JS (`syncUnmanagedSelection`) only ever managed checkbox *state*, with
  no submit ever wired to it. Rebuilt as a per-path checkbox list plus a
  "select all", submitting through the same removal overlay already
  proven for Library/Torrents rather than reconstructing the old bespoke
  two-level group/sub-path scheme — simpler, and each hardlinked path of
  an Unmanaged file can now be selected independently, since removing one
  alias vs. all of them are genuinely different, meaningful actions.

## Near-term

- Make task schedules configurable through the GUI.
- Add richer task execution history and reconciliation diagnostics.
- Add useful Unmanaged filters.
- Improve explicit provenance-change reasons and per-object historical
  timelines.
- Continue readability work outside the HTTP/UI files touched through 0.2.8.

## Later

- Capability discovery for the service manager (auto-detecting what an
  added service supports, beyond the connection test already in place).
- Lidarr, Readarr, Bazarr, Transmission, Deluge, rTorrent, Plex, and other
  service adapters.
- Per-storage-device alarms, acquisition inhibition, and explicitly
  configured emergency behavior.
- Per-tracker torrent retention rules (e.g. a hit-and-run window, or a ratio
  floor that varies by tracker rather than one global value) on top of the
  global ratio floor and keep-tag protection already implemented (0.2.12).
  `Torrent.Tracker` is already a reconciled field; this is a policy layer on
  existing data, not new service work.
- Observed reclamation verification: confirming after automatic execution
  that the predicted bytes were actually freed, not only that the plan ran.
- Filesystem-specific shared-storage inspectors for reflinks/extents, ZFS,
  Windows, and network filesystems where reliable.
- Cross-stack diagnostics, repair workflows, global search, and per-item
  timelines.
- Quality-versus-storage-cost reasoning and upgrade/downgrade recommendations.
- Webhook/event adapters where services expose useful reliable events.

## Product direction

Connarr should become the missing coordination layer in a modular media stack:
not another specialized media manager, but the place where facts from
specialized tools gain cross-stack context and purpose.
