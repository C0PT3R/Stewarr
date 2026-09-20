# Stewarr Roadmap

This file separates implemented behavior from intended direction. It is not a promise of release dates. See `CHANGELOG.md` for a curated, version-by-version release history — the section below predates that file and was never fully kept in sync with it version-by-version as new releases shipped; entries here describe design intent and the state of the product at the time they were written, not necessarily the current terminology (e.g. "Swarm Value" below was Torrent Value's name at the time).

## Implemented through 0.4.8

### Reactive UI state

- Server-rendered Go templates enhanced by pinned, embedded htmx and Stimulus;
  Stewarr has no CDN, SPA, or mandatory frontend build service.
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

- Current torrent states are **Current**, **Superseded**, **Orphaned**, and
  **Unassociated** (Orphaned was reintroduced as its own distinct state in
  0.3.3, below, after briefly collapsing into Unassociated).
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
  device; a device Stewarr cannot measure is reported Unavailable without
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
- PUID/PGID-friendly Docker deployment and centralized Stewarr product identity.

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
  no submit ever wired to it. Rebuilt with one checkbox per physical
  file, submitting through the same removal overlay already proven for
  Library/Torrents. A first version let each hardlinked path of a file be
  selected independently, on the theory that removing one alias vs. all
  of them are different actions — in practice this let a user select
  only some of a file's hardlinks and reclaim exactly 0 bytes, since the
  remaining link keeps the data alive, with no visible reason why. One
  checkbox per file now selects (and submits) every one of its hardlinked
  paths together. Each row also has its own trash icon (same convention
  as Library/Torrents) for removing that one physical file directly,
  without selecting it first — it targets every one of its hardlinked
  paths too, for the same reason the bulk checkbox does.
- The Unmanaged page moved from `/downloads/unmanaged` to `/unmanaged`
  (and its scan endpoint from `/downloads/unmanaged/scan` to
  `/unmanaged/scan`) — a leftover from an earlier, narrower framing of
  the feature: `/downloads` was never used as a prefix anywhere else in
  the app, and the scan itself isn't scoped to download-client folders at
  all, so the nesting no longer matched anything. Now flat like every
  other top-level page (`/library`, `/torrents`, `/services`, ...).
- Fixed a third bug in the same feature, separate from the two already
  found: `groupRemovalFiles` marking the primary target Selectable fixed
  the *server-rendered* display, but the browser actually decides what
  gets submitted on "Remove selected" from an entirely different
  JSON model (`prepareRemovalData`, base64-embedded in the overlay) —
  and that model's `UnmanagedOwner` branch never set `Selectable` at
  all. `removal.ts` only ever includes a file in the real submission
  when both `selectable` and `selected` are true, so the primary target
  was silently dropped from every actual removal request regardless of
  what the preview showed — the file could never really be deleted.
- Fixed a fourth bug in the same feature: the Unmanaged listing page could
  report a hardlinked file as having "1 hardlink outside known topology"
  even when every one of its real paths was still present and its actual
  filesystem `nlink` matched the known path count exactly. The listing
  handler filtered out any individual path with a pending removal
  operation (`filterUnmanaged`) *before* grouping by device/inode — for a
  hardlinked file, hiding one known path this way left `Links` (the real,
  filesystem-read `nlink`) unchanged while the group's remaining `Paths`
  count dropped by one, manufacturing a "missing" hardlink that never
  existed. Fixed by grouping first, then dropping the whole physical-file
  group if any of its known paths has a pending operation, instead of
  partially hiding one path and desyncing its Links/Paths count.

### Authentication (0.3.0)

- Stewarr had no authentication at all — only `sameOriginWrites`, a
  CSRF-style guard, not an identity check. 0.3.0 adds a single-admin login
  gate in front of every route (`authGate`, `internal/httpui/auth.go`).
  There is deliberately no multi-user support, roles, or invitations —
  Stewarr remains a private single-deployment app for one person.
- Credentials (`config.Auth{Username, PasswordHash}`) live in `config.json`
  like every other secret; the password is bcrypt-hashed, never stored or
  logged in plain text. An empty `Auth.Username` is the "no account yet"
  signal — Stewarr has no default/backdoor account.
- First run shows a one-time setup screen (`/setup`) to create the admin
  account, instead of requiring a hand-edited config file or CLI step.
  There is no separate password-reset flow: like many apps in the *arr
  ecosystem, erasing the `auth` key from `config.json` and restarting puts
  Stewarr back into first-run setup.
- Sessions are server-side (a `sessions` table: token/created/expires),
  not JWTs — revoking one is a `DELETE`, not a client-side expectation.
  Cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` when the request
  (directly or via `X-Forwarded-Proto`) is actually HTTPS. Sessions are
  deliberately long-lived (30 days): this is a private app meant to stay
  signed in on the devices you actually use, not a multi-tenant service
  where a short session limits blast radius.
- A Settings page (`/settings`) lets the password be changed without
  editing the config file. Changing it revokes every other session
  (`DeleteAllSessions`) — a credential rotation should actually lock out
  a device you no longer trust, not just accept a new password while
  leaving old sessions valid — while re-establishing one for the browser
  that made the change, so that browser isn't logged out too.
- `authGate` fails open when no durable store is available, since sessions
  cannot be persisted at all without one. Every real deployment opens a
  store before constructing the server (`cmd/stewarr/main.go`); this only
  ever applies to handler-level tests built without one — and that bypass
  now logs a warning the first time it's hit, rather than silently leaving
  every route unauthenticated with no signal anything is wrong.
- A post-release review of this feature found and fixed two more gaps: the
  login handler used to short-circuit past `VerifyPassword`'s bcrypt call
  whenever the username alone was already wrong, so a wrong username
  returned near-instantly while a right-username-wrong-password case took
  bcrypt's cost — a timing side-channel disclosing the admin username
  without ever guessing its password. Both checks now always run. Separately,
  `/login` had no limit on repeated attempts beyond bcrypt's own per-attempt
  cost, so a scripted credential-stuffing run was only slowed, never
  stopped; a per-client `loginLimiter` now adds an escalating lockout
  (15s per failure past a small threshold, capped at 5 minutes) on top of it.

### Storage page cleanup-candidate list and auto-removal groundwork (0.3.2)

- The Storage page's cleanup-candidate list rendered a `StandaloneTorrent`
  action as nothing but its raw release name, with no way to tell whether
  removing it would touch any library copy at all. Candidates are now
  grouped into "Torrents" and "Media" sections (the same two tiers
  `cleanup.rank()` already computes internally — every torrent candidate
  before any media/season one), and each torrent line now discloses its
  association status and whether it's a proven independent (non-hardlinked)
  copy, plus the related media title if one is known.
- `runAutoRemovalEvaluation` (still unexposed in the UI — this is
  groundwork ahead of the 0.4.0 auto-removal milestone) now excludes
  Unassociated torrents by default, gated by a new
  `removal.auto_remove_unassociated_torrents` config flag (default false).
  An Unassociated torrent with no relationship Stewarr has ever recorded
  may simply be something the user downloaded through that client for
  their own purposes, or from a service Stewarr doesn't track — automatic
  removal has no basis to judge those safe to delete unattended, unlike a
  torrent it can prove is Superseded or Orphaned (see below). This was
  originally implemented as a side-check on `FormerMediaItems`; the
  follow-up below replaced that with a real, named status instead.

### Orphaned torrent status, and selectable historical torrents in media removal (0.3.3)

- Added a fourth torrent relationship status, `Orphaned`, alongside
  `Current`/`Superseded`/`Unassociated`. Reconciliation previously
  collapsed two different cases into `Unassociated`: a torrent with no
  import provenance at all, and a torrent with provenance (a
  `FormerMediaItems` relationship) but no proof of a specific replacement
  — e.g. the media it belonged to was removed from Radarr/Sonarr entirely
  rather than re-imported as a better copy. The second case is now
  `Orphaned`, its own real status (`internal/inventory/service.go`),
  distinct from both `Superseded` (a *specific* newer import is known to
  have replaced it) and `Unassociated` (no relationship at all, historical
  or otherwise). Every place that branched on the old three states —
  the media detail page's torrent groups, the Torrents page's counts and
  status filter, the home dashboard, the Storage page's per-torrent
  context — now has an explicit fourth case rather than Orphaned silently
  falling into whichever `default:`/`else` branch happened to catch it
  (in `groupMediaTorrents`'s case, that used to mean an Orphaned torrent
  vanished from the media page entirely — no case matched it at all).
  Auto-removal's Unassociated exclusion (0.3.2, above) now simply checks
  the status directly, since Orphaned no longer collapses into it.
- The media removal plan used to disclose only `Current` and `Superseded`
  torrents related to the media as context, and only `Current` ones were
  ever selectable — `Superseded` was read-only. Every related torrent
  except a truly Unassociated one (Current, Superseded, or Orphaned) is
  now disclosed *and* selectable, so removing a media item can also clean
  up every old release for it in one action instead of requiring a
  separate manual torrent removal for each. Default selection is
  unchanged for the primary case and now applies uniformly: only a
  torrent proven to physically back the media (hardlinked) is pre-checked;
  every other related torrent — a Current-but-not-hardlinked copy,
  Superseded, or Orphaned — starts unchecked but available to select.

### Season removal order now tracks air date, not import date (0.3.4)

- Season Retention Value's recency factor came from `LastAddedAt` — when
  Stewarr's library *imported* the season's files — not from when the
  content itself aired. This barely varies for a show backfilled all at
  once (every season gets nearly the same import timestamp), so which
  season looked "most recent" could come down to unrelated noise, letting
  an early season outrank a genuinely newer one in the removal order.
  Sonarr's episode API already reports each episode's air date; Stewarr
  just didn't fetch it. Added it (`internal/services/sonarr/client.go`
  now reads `airDateUtc`), threaded it through as
  `model.MediaFilePart.AiredAt`, and `Season.LastAiredAt` (renamed from
  `LastAddedAt`, since it's a different fact now) is the max of that
  across a season's episodes. `applySeasonValues` scores recency from
  this instead.
- On top of the corrected signal, added a hard guarantee
  (`cleanup.enforceSeasonOrder`) as a safety net: within one show, an
  earlier season can never be selected for removal after a later one,
  even if their computed values ever tie or invert (missing air-date
  data, a show that aired out of numeric order). It reassigns each show's
  own season actions into the exact ranked positions they already occupy
  after the normal value-based sort — so cross-show removal priority is
  unaffected — filling those positions by ascending season number instead
  of by value.

### TMDB rating/popularity enrichment (0.3.5)

Radarr/Sonarr's own rating data is a weak proxy for how much anyone would
actually miss a title, and neither service had anything resembling a real
popularity signal — Sonarr's TVDB-sourced rating has no equivalent at all,
and Radarr's own `popularity` field (TMDB's, distinct from rating/votes)
wasn't even parsed. Since Stewarr aims to become a publicly usable app,
not a single deployment tuned by hand, this is closed with an always-on
enrichment source rather than a per-service quirk — fetched directly from
TMDB itself (`internal/services/tmdb`), covering movies and TV under
one API. Considered and rejected: MDBList (an unnecessary extra hop in
front of the same underlying sources) and IMDb (paid API for programmatic
access; the free non-commercial dataset is a bulk TSV dump with no
popularity metric at all, just a redundant rating).

- A series' TVDB id resolves to a TMDB TV id via TMDB's own
  `/find/{tvdb_id}?external_source=tvdb_id`. That mapping is cached back
  onto `Media.TMDBID` (`preserveTMDBFacts`) so it's resolved at most once
  per series, ever — never repeated on later enrichment passes.
- TMDB is configured directly (`Config.TMDB.APIKey`, via the Settings
  page), not as a `config.Service`: unlike Radarr/Sonarr/Jellyfin/Seerr it
  isn't self-hosted, has no URL, and there's only ever one instance.
- Follows the exact `merge*Facts` / `preserve*Facts` / `clear*Facts` shape
  Jellyfin/Seerr enrichment already used — an async task
  (`RefreshTMDB`/`ValidateTMDB`, `internal/inventory/service.go`) on its
  own 24-hour interval (much longer than Jellyfin/Seerr's hourly one:
  rating/popularity don't need to track library changes in real time) that
  never blocks or gates the base Radarr/Sonarr refresh.
- Rating/VoteCount already had a fallback value to revert to (Radarr/
  Sonarr's own), so TMDB's numbers don't overwrite `Rating`/`VoteCount` in
  place — they get their own fields (`TMDBRating`, `TMDBVoteCount`), and
  `valuation.effectiveRating` prefers them when `TMDBVoteCount>0`, falling
  back to `Rating`/`VoteCount` otherwise. TMDB being unconfigured,
  unreachable, or having no match for a title never removes the signal
  Radarr/Sonarr already provided. `Popularity` has no such fallback
  anywhere — absent means "unknown," the same treatment `VoteCount==0`
  already got, never scored as "unpopular."
- New `Valuation.Weights.Popularity` weight scores `Media.Popularity`
  (log-scaled, same style as the existing vote-count weight); the
  vote-count reason label changed from "Popularity" to "Vote count" to
  keep the two distinct now that a real popularity signal exists.
- Per-item staleness for automatic removal only: unlike Jellyfin/Seerr's
  atomic all-or-nothing enrichment passes, TMDB fetches one item at a
  time, so one item's fetch can fail while its neighbors succeed.
  `Media.TMDBEnrichedAt` records when an item was last successfully
  enriched; `RefreshTMDB` no longer clears facts before re-fetching (a
  transient per-item failure now preserves the last known values instead
  of blanking them for that cycle). Automatic removal specifically
  (`mediaTMDBDataStale`, `internal/httpui/auto_removal.go`) excludes an
  item whose TMDB data was never fetched or is older than two refresh
  cycles (~48h) — everywhere else (valuation, the Storage page, manual
  removal) keeps using whatever it last knew, stale or not, since a human
  reviewing a manual removal can judge that for themselves. Turning TMDB
  off entirely (`SetTMDBAPIKey("")`) does explicitly clear it, though —
  a deliberate opt-out should stop influencing valuation immediately
  rather than lingering.
- Newly discovered media is enriched immediately rather than waiting for
  RefreshTMDB's next scheduled daily pass: `EnrichNewTMDBItems`
  (`internal/inventory/service.go`) fires as a fire-and-forget goroutine
  right after every successful base (Radarr/Sonarr) refresh
  (`cmd/stewarr/main.go`'s `inventory` task), fetching only items that
  have never been successfully enriched (`needsTMDBEnrichment` —
  `TMDBEnrichedAt` still zero and an external id exists to look up), so a
  freshly imported movie or show gets its rating/popularity right away
  instead of sitting unscored for up to 24h. Unlike RefreshTMDB it never
  touches `Reliability.TMDB` and isn't the authoritative full-library
  refresh — just a best-effort catch-up for specific new items.
- Settings page gained a single "Use TMDB enrichment" checkbox
  (`internal/httpui/templates/settings.html`, `page_settings.go`) that
  reveals the API key field and a "Test" button
  (`POST /settings/tmdb/test`, checks the key against TMDB directly with
  nothing saved) only once checked. There's no separate on/off flag —
  an empty key *is* the off state (`SetTMDBAPIKey("")`) — so unchecking
  and saving clears the key and, with it, `Popularity`/`TMDBRating`/
  `TMDBVoteCount` everywhere they're used. This is also why Radarr's own
  `popularity` field was never worth parsing: popularity only exists
  behind this one opt-in, so there was never a need for a Radarr-sourced
  fallback the way Rating/VoteCount have one.
- `Valuation.Weights.Popularity` defaults to a nonzero value for a
  `config.json` written before the field existed, the same way
  `TorrentWeights` already gets backfilled on `Load()` — otherwise the
  weight would silently sit at Go's zero value and Popularity would never
  affect `RetentionValue` until someone happened to hand-edit the config.

### Automatic removal controls moved into Settings/Service UI (0.3.6)

The automatic-removal execution pipeline itself (`runAutoRemovalEvaluation`,
the global `removal.auto_enabled` switch, the per-service `Service.
AllowAutomaticRemoval` opt-in, `removal.dry_run`) has been fully built and
scheduled since 0.2.12 — this only closes the last gap, that every one of
those flags could so far only be changed by hand-editing `config.json`.

- Settings gained an "Automatic removal" card
  (`internal/httpui/templates/settings.html`, `page_settings.go`,
  `POST /settings/removal`) with three checkboxes: the global enable
  switch, whether to also remove torrents with no known owner
  (`auto_remove_unassociated_torrents`), and dry run — which applies to
  every removal, manual or automatic, not just this feature.
- The add/edit service forms gained an "Allow automatic removal" checkbox.
  Fixed in the same change: `submitEditService`'s `updates` struct was
  never populated from a form field for this at all, and
  `config.EditService` replaces a service's fields wholesale rather than
  merging — so before this, *any* unrelated edit (a rename, a URL change)
  would have silently reset a service's automatic-removal opt-in back to
  false the moment the checkbox existed to set it. Guarded by
  `TestEditServiceRoundTripsAllowAutomaticRemoval`.

### Enrichment reliability trusts a restored cache instead of resetting stale (0.3.7)

A process restart that successfully loaded persisted media from the
database (`New()`, `internal/inventory/service.go`) was unconditionally
marking every configured enrichment source (Jellyfin, Seerr, TMDB)
`"stale"` via `enrichmentInitialState` — the same helper used for a
genuine cold start with no cache at all. That state only self-corrects
once that source's own periodic task completes again from scratch:
Jellyfin/Seerr's hourly interval hid this within an hour, but TMDB's 24h
interval meant "automatic removal planning is paused" for a full day
after every single restart, even though the just-loaded facts were
already good. New `enrichmentInitialStateFromCache` marks a successfully
restored cache `"reliable"` immediately instead. This doesn't weaken any
real protection: the precise per-item staleness check
(`mediaTMDBDataStale`, `internal/httpui/auto_removal.go`) still
independently excludes any specific item whose own `TMDBEnrichedAt`
really is too old, regardless of this coarser reliability flag. Guarded
by `TestNewTrustsCachedEnrichmentAfterRestart`.

### Non-opted-in services excluded from the removal candidate list itself (0.4.0)

`Service.AllowAutomaticRemoval` previously only gated automatic
(unattended) execution (`actionServicesOptedIn`, `internal/httpui/
auto_removal.go`) — the Storage page's manual candidate list came from
the exact same `cleanup.Build` plan and showed everything regardless, so
a series (or any media/torrent) from a service that had never opted in
could still appear as something to remove.

Fixed at the source instead of filtering the result: new `Media.
RemovalRestricted` / `Torrent.RemovalRestricted` (`internal/model/
media.go`) are computed by `valuation.ApplyMedia`/`ApplyTorrents` from
each item's own `ServiceID` (an unrecognized or opted-out id fails
closed), and `internal/cleanup`'s planner excludes a restricted item —
and vetoes a whole hardlinked bundle if either side is restricted — the
same way an already-`Protected` item already is. This keeps `NeedBytes`/
selection math consistent (an excluded item's hardlink partner is still
visible to the bundling logic, just never offered) instead of filtering
`Plan.Actions` after the fact. Deliberately a separate field from
`Protected`: this is an administrative permission, not a KeepTag/ratio
judgment the item earns on its own merits, so it's never shown as
"Protected" on the Library or media detail pages, and it does not gate a
human manually removing that one item by hand from its own page — only
the batch planner. `actionServicesOptedIn` is now redundant by
construction (no `Plan.Action` can reference a restricted item at all)
and was removed.

### Three-state automatic removal mode, service checkbox scoped to removable types (0.4.1)

- Settings' "Enable automatic removal" checkbox became `Removal.AutoMode`
  (Disabled/Confirm/Auto), replacing the plain on/off switch. Disabled
  means `runAutoRemovalEvaluation` does nothing at all — no snapshot, no
  `cleanup.Build` — not just that it withholds submission. Confirm runs
  the full evaluation (every automatic-removal-only filter: TMDB
  staleness, the unassociated-torrent gate) but never submits, leaving a
  pre-vetted candidate for a human to act on through the normal manual
  removal flow — there's no separate approval queue yet, since that's a
  bigger, still-undecided UI question. Auto is the original always-submit
  behavior.
- The add/edit service forms' "Allow automatic removal" checkbox now only
  shows for radarr/sonarr/qbittorrent — Jellyfin and Seerr are enrichment
  sources, not owners of anything `cleanup.Build` could ever propose for
  removal, so the checkbox was meaningless (and confusing) for them.

### Disabled hides the Storage page's cleanup plan too, not just the background task (0.4.2)

0.4.1's Disabled mode only stopped `runAutoRemovalEvaluation` — the
Storage page's own "Cleanup active" summary and candidate/action list
kept showing regardless, since that display has always been an
independent code path that happens to call the same `cleanup.Build`.
Disabled is meant to mean automatic removal doesn't exist at all, not
merely that nothing gets submitted unattended, so the Storage page now
hides that whole section (and its "Need to reclaim..."/action list)
when Disabled, leaving removal purely manual — the raw usage bar,
legend, and threshold markers are unaffected, since those describe real
disk state rather than a removal suggestion.

### Per-device settings moved into a wrench-icon overlay (0.4.3)

Each device's Target/Critical % fields were an inline form directly in
the Storage page card. Moved to a small wrench icon in the card's
top-right corner (`internal/httpui/templates/device_settings.html`, new
`GET /storage/device-settings?path=...`) that opens the same overlay
mechanism the service add/edit modals already use, with Save/Cancel —
still posting to the existing `/storage/device-threshold` endpoint
unchanged. Deliberately not tied to any auto-removal feature: this is a
general place for per-device settings to live, so a future one joins
this same modal instead of the card growing another inline field. Also
reworded the Disabled-mode note (0.4.2) from "nothing is suggested for
removal here" to "Automatic removal is off. Enable it in Settings to
see suggestions." — the old wording named where suggestions currently
show up, which is exactly the kind of detail that shouldn't be baked
into copy that's meant to outlive it.

### User-nameable storage devices (0.4.4)

`config.DeviceThreshold` (already keyed by `RepresentativePath`, holding
Target/Critical %) gained a `Name` field, editable from the same Device
settings overlay the thresholds already live in. When set, it becomes
the device's primary heading on both the Storage page card and the Home
dashboard's mini device summary — the root-labels/filesystem string
that used to be the only identity line stays underneath as secondary
detail either way.

Deliberately scoped down from the original ask: a stable numeric id
(assigned once per physical device, defaulting an unnamed device's name
to "Device #N") is not implemented yet — that needs a sync step run
after file reconciliation to assign ids to newly-discovered devices and
prune entries for devices no longer found, which is being held for a
follow-up rather than rushed into this change. Until then, a device's
Name is simply empty until someone sets one by hand.

### Stable device ids and default "Device #N" naming (0.4.5)

The follow-up promised in 0.4.4. `DeviceThreshold` gains an `ID int`,
and `config.SyncDeviceRegistry(configuration, liveRepresentativePaths)`
assigns the lowest currently-unused id to any live device that doesn't
have one yet, sets its `Name` to the literal string `"Device #<id>"` at
that moment (a real stored value from then on, not a computed fallback
— never overwrites a name that's already set, whether user-chosen or a
prior default), and prunes entries for devices no longer discovered so
ids actually get reused instead of only ever climbing.

Wired into `internal/inventory/reconcile.go`'s `reconcileFiles`, right
after it commits the fresh `storageRoots` — the one point with the
complete current device set. Deliberately not called from the
targeted/delta reconciliation path (`reconcileTargeted`), which only
ever sees a partial scope: running the prune there would wrongly delete
a device merely absent from that narrower view. Best-effort: a persist
failure here is logged rather than failing reconciliation, since the
authoritative file/media state is already committed by that point and
cosmetic device identity isn't worth discarding real work over.

Also fixed in the same change: `SetDeviceThreshold`'s upsert built a
fresh `DeviceThreshold` from scratch on every save, which would have
silently wiped whatever id this feature assigned the moment someone
saved the Device settings overlay. Guarded by
`TestSetDeviceThresholdPreservesAssignedID`.

### Clearing a device's name reverts to the default, not to blank (0.4.6)

`SetDeviceThreshold` treated an empty submitted name as "clear it,"
leaving the device with no name at all — but `SyncDeviceRegistry` only
ever sets `"Device #<id>"` once, the first time a device is seen, so a
cleared name had nothing to revert to. Clearing the Name field in the
Device settings overlay now reverts to that default instead. Guarded by
`TestSetDeviceThresholdClearingNameRevertsToDefault`.

### Fixed two ways TMDB data could go stale despite refreshing on schedule (0.4.6)

The design was supposed to make staleness impossible (full refresh every
24h, staleness threshold at 48h), but two bugs let it happen anyway —
both compounded by the auto-removal pipeline (0.4.0+) making removals,
and therefore base-inventory refreshes, more frequent:

- `RefreshTMDB` discarded its *entire* pass — including every item
  that was successfully re-fetched — the moment the base
  Radarr/Sonarr/qBittorrent generation ticked at all during the run.
  Since TMDB fetches one item at a time (a full pass over a real
  library can run for minutes) while the base refresh interval is
  routinely 30 minutes and every removal also bumps it, this discard
  fired far more often than the design assumed, and nothing else
  re-fetches an item that's already been enriched once
  (`EnrichNewTMDBItems` only ever catches up never-enriched items).
  Fixed by merging directly onto whatever's current at commit time
  (`mergeTMDBFacts` already matches by stable key, so this was safe all
  along) instead of comparing generations and discarding. Guarded by
  `TestRefreshTMDBDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass`.
- The `tmdb` task is `Interruptible`, so a higher-priority removal can
  cancel it mid-pass via the scheduler's own `context.Canceled`
  mechanism — but `tmdb.Client.Apply` swallowed that exactly like an
  ordinary per-item fetch failure and always returned `nil`. The
  scheduler only recognizes an interruption (and fast-retries via
  `InterruptionDelay` instead of waiting a full 24h) by checking
  `errors.Is(runError, context.Canceled)`, so every interrupted pass was
  misreported as a plain success, `Reliability.TMDB` was marked
  `"reliable"`, and whichever items the batch hadn't reached yet stayed
  silently stale until the next attempt — itself just as likely to be
  cut off the same way. Fixed by having `Apply` return the context's own
  error after its worker pool finishes. Guarded by
  `TestApplySurfacesContextCancellationInsteadOfSwallowingIt`.

### Enrichment is now fetched atomically at discovery, not awaited from a periodic task (0.4.7)

0.4.6 fixed two real bugs in `RefreshTMDB`, but production logs still
showed `task=inventory status=degraded warning="TMDB enrichment stale"`
recurring for hours in an actively-changing library. The actual root
cause was a third, coarser mechanism in `Refresh()`: any time the media
catalog's identity changed at all (a new item imported, or even a show's
TMDBID simply resolving for the first time), a fingerprint comparison
force-reset `Reliability.Jellyfin`/`Seerr`/`TMDB` straight back to
`"stale"` — regardless of whether anything had actually gone wrong.
Jellyfin/Seerr recovered within their ~1h interval; TMDB, refreshed only
every 24h, routinely didn't recover before the next reset fired again.

Rather than patch the reset's TMDB case alone, the underlying design was
wrong: a periodic Refresh* task should only exist to catch *drift* in
already-known items, not to be the thing a brand-new item waits on for
its first enrichment. Fixed by:

- Deleting the fingerprint-reset mechanism (and the now-dead
  `mediaEnrichmentFingerprint` function) entirely.
- Replacing `EnrichNewTMDBItems` with `EnrichNewMedia`, which fetches
  Jellyfin, Seerr, and TMDB facts atomically and concurrently for any
  item none of them has checked yet, right when the base inventory
  refresh that discovered it completes — the only place a new item's
  enrichment is ever fetched from now on. Guarded by
  `TestEnrichNewMediaFetchesAllApplicableSourcesForANewItem` and
  `TestEnrichNewMediaSkipsSourcesAlreadyChecked`.
- Extending the same generation-race tolerance 0.4.6 gave `RefreshTMDB`
  to `RefreshJellyfin`/`RefreshSeerr` too, since both were just as
  exposed to the same discard-a-good-pass bug. Guarded by
  `TestRefreshJellyfinDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass`
  and `TestRefreshSeerrDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass`.
- Fixing a related correctness bug this surfaced: `preserveTMDBFacts`/
  `preserveJellyfinFacts`/`preserveSeerrFacts` used to carry cached facts
  forward across a base refresh purely by stable key, even when an
  item's TMDB/TVDB/IMDB id had genuinely changed underneath it (Radarr
  re-matching a movie to a different TMDB entry) — silently keeping a
  now-wrong title's rating/playback/request facts attached to the new
  one. Fixed with a new `externalIDsChanged` check that clears cached
  facts (and the corresponding `*EnrichedAt` timestamp) instead of
  carrying them forward whenever the id actually changed. Guarded by
  `TestPreserveTMDBFactsClearsRatingWhenMovieIsReMatchedToADifferentTMDBID`
  and the equivalent Jellyfin/Seerr tests.

### Renamed Connarr to Stewarr (0.4.8)

Connarr was named that way partly as a joke (*connard* is French for
"dumbass"). Renamed to Stewarr instead — short, reads as "steward," and
still fits alongside Radarr/Sonarr/Overseerr's `*arr` convention without
carrying the joke. This is a mechanical, repo-wide rename: the Go module
path, every `connarr/internal/...` import, `cmd/connarr` →
`cmd/stewarr`, `product.Name`/`Slug`, Docker image/container/user names,
`Makefile` targets, and every doc/template/frontend string that spelled
out the old name.

Two things went further than a plain find-and-replace:

- The legacy database-migration mechanism (`migrateLegacyDatabaseAt`,
  carried since the app's Togetharr/Spartarr days) is removed entirely,
  and the database file itself is renamed from the brand-tied
  `connarr.db` to a generic `inventory.db` — this app has exactly one
  real deployment, and its database gets wiped by hand as part of this
  upgrade rather than migrated, so there is nothing left for that
  mechanism to do.
- The on-disk project directory itself (previously `spartarr`, the
  app's original name, predating even Connarr) is renamed to `stewarr`
  to match.

No back-compat shims: this is a single-user, single-deployment app (see
prior convention — DB schema changes get a "wipe the DB" answer, not a
migration), so an old binary or config path is simply retired, not kept
working alongside the new one.

## Next milestone: torrent valuation based on activity and history

Design only — nothing below is implemented yet. This is the confirmed
next body of work, ahead of the file explorer idea (deferred).

### The problem

`cleanup.rank()` (`internal/cleanup/plan.go`) always drains every
torrent-domain candidate before it ever looks at a media-domain one —
"Media Retention Value and Torrent Swarm Value are deliberately
unrelated scores and are never compared numerically; only this tier
order decides which domain is tried first" (0.2.11). In practice this
means a torrent with a great ratio, actively useful to its swarm, gets
removed before a movie nobody will ever watch, purely because of which
domain it happens to be in — never because anyone actually compared the
two.

### Cross-domain comparison, scoped to one device

Replace the strict tier order with a single unified ranking across both
domains, gated by one new user setting: how much the user cares about
their torrents relative to their media (a percentage; a 50% default was
floated but is explicitly **not calibrated yet** — needs more thought
before implementation, not a placeholder to ship as-is). That setting
scales a new, fully computed Torrent Value onto the same comparable
scale Media's Retention Value already uses, so the two can be sorted
together.

This comparison only ever makes sense between items on the *same*
physical device — comparing a torrent's value against media sitting on
an entirely different disk is meaningless. This already falls out for
free: `cleanup.Build` is already called once per physical device, fed
only that device's own media and torrents (grouped by the real
`stat.Dev` number via `knownDeviceRoots`), so a unified ranking
implemented inside that existing per-device scope never needs special
handling for this — it's structurally impossible for it to compare
across devices.

### Torrent evaluation is entirely hardcoded — one dial, not a weights panel

Unlike Media's `ValueWeights` (which stay user-editable), nothing about
*how a torrent's own value is computed* is user-configurable. The goal
is to establish what Stewarr considers objectively true about a
torrent's health, not what the user thinks makes one healthy — the only
thing the user controls is the single relative-care percentage above.
This retires the existing `TorrentValueWeights` (Seeds/Leechers/
UploadRate) config surface entirely, superseded by this.

Metrics feeding the hardcoded torrent value, all client-agnostic
(`model.Torrent` fields any adapter can populate from whatever its own
API exposes — none of this is qBittorrent-specific, since Stewarr will
eventually support other torrent clients too):

- **Ratio** — cumulative, stable.
- **Recency of real activity**, via `LastActivity` — the same shape as
  Media's existing `LastWatchedAge`, not a live instantaneous speed
  reading. Current upload/download speed was explicitly considered and
  rejected: it depends entirely on what's happening at the exact instant
  a calculation runs (peer availability, the user's own bandwidth,
  time of day), not any lasting property of the torrent.
- **Private tracker status** — static fact, but a meaningful one: losing
  standing on a private tracker (ratio requirements, warnings, bans) has
  real consequences a public-tracker torrent never faces.
- **Seeds/leechers demand** — a high leecher:seeder ratio is a genuine
  "other clients need this" signal, but only once confirmed *sustained*
  over the monitoring history (see below), not from a single live
  reading, which can be misleading during a stall or a temporary tracker
  error.

### A real history store, not live snapshots

Every signal above (except the static ones) is derived from *sustained*
history, not an instantaneous read — a new periodic task samples each
torrent's key stats over time and persists them, so "has this been
erroring for a week" replaces "is this erroring right now" everywhere
it matters. SQLite (already `PRAGMA journal_mode=WAL` +
`synchronous=NORMAL`, already using batched single-transaction writes
for other bulk data — see `saveMedia`/`saveTorrents`) is confirmed
sufficient for this: even a large single-user library sampled every
15–30 minutes with a sane retention window lands at a few hundred
thousand rows at most. The new table needs its own retention/pruning
step (delete samples past the window on each cycle, so it never grows
unbounded) and an index on torrent hash + timestamp for the "recent
history for this torrent" query pattern the derived signals need.

## Near-term

- **Priority: run without full filesystem access instead of failing closed
  entirely.** Today, `walkStorageRoots` ([reconcile.go](internal/inventory/reconcile.go))
  returns a hard error the moment a single configured root can't be stat'd,
  and `reconcileFiles` aborts the *entire* reconciliation cycle on that
  error — not just the filesystem-dependent parts. Since cleanup planning
  requires the file model to be "reliable," that one failure currently
  pauses Media/Torrent valuation and cleanup too, even though neither
  actually depends on the filesystem. Needs: (1) decoupling the
  filesystem-dependent stage (hardlink-bundle detection, Unmanaged
  scanning, real disk usage) from the filesystem-independent one
  (per-service valuation, standalone cleanup using each service's own
  self-reported file sizes) so either can run without the other; (2) a
  capabilities page — building on the existing `UnreachableServiceRoots`
  ([storage_devices.go](internal/inventory/storage_devices.go)) groundwork
  and `storagecapabilities.Inspect` — listing every discovered path, what
  it currently unlocks, and what mounting it would add, so a reduced
  configuration is discoverable and well-documented rather than a silent
  gap. This is the top of Near-term, not a someday item — see Product
  direction above.
- Make task schedules configurable through the GUI.
- Add richer task execution history and reconciliation diagnostics.
- Improve explicit provenance-change reasons and per-object historical
  timelines.
- Continue readability work outside the HTTP/UI files touched through 0.2.8.

## Later

- Capability discovery for the service manager (auto-detecting what an
  added service supports, beyond the connection test already in place).
  Where an adapter has a known, permanent limitation (not just "untested
  yet"), the add-service overlay should surface it up front as a plain
  warning before the service is added. This is disclosure, not
  configuration — the user isn't asked to work around it, just told
  what won't work and why, the same moment they're choosing which
  adapter to add.
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
  Windows, and network filesystems where reliable — part of the same
  graceful-degradation objective at the top of Near-term: an unrecognized
  or partially-inspectable filesystem should narrow what Stewarr can prove
  (e.g. falling back to reported size instead of confirmed shared bytes),
  not be treated as an error case.
- Cross-stack diagnostics, repair workflows, global search, and per-item
  timelines.
- ~~Movies and series must be separated~~ — **done.** `cleanup.rank`/`Build`
  now split media's share of a device's overage across each media action's
  owning service (`Action.Media.ServiceName`) proportional to that
  service's current claimed-bytes footprint on the device
  (`fairMediaShare`), not by whichever items score lowest overall — series
  consistently scoring higher in Retention Value than movies (real
  differential engagement, not a scoring defect) no longer means movies
  structurally absorb nearly all of a shared device's overage. This
  generalizes past just movies vs. series for free, since it keys off
  `ServiceName` — two Radarr instances (or a "Movies" and "Movies 4K" root)
  sharing a device get the same fair split, which a type-only rule would
  have missed. Torrents are explicitly excluded from this split — the
  existing `torrentCarePercent` cross-domain comparison against media as a
  whole is unchanged; a per-service fallback (no footprint data at all)
  behaves exactly like the old unconstrained order, so nothing regresses
  for a caller that hasn't wired `claimedByService` through. Still open:
  this is footprint-proportional only — an explicit per-service override
  (rather than always proportional) isn't built, and hasn't come up as
  needed yet.
- Quality-versus-storage-cost reasoning and upgrade/downgrade recommendations.
- Webhook/event adapters where services expose useful reliable events.
- A real Confirm-mode review surface: Confirm (0.4.1) currently just
  withholds submission, leaving pre-vetted candidates for a human to find
  via the normal Storage-page flow. Worth revisiting once it's actually
  used: a dedicated list of what Confirm evaluated, letting someone both
  approve/execute from it and add a Keep tag to anything they'd rather
  protect than remove, right from that review — instead of only being
  able to react to what's already about to be gone.
- TMDB trending (`/trending/movie|tv/{day|week}`): a different shape from
  Rating/Popularity — a curated top-N list fetched once per refresh
  cycle rather than a per-item lookup — with a rank-based score (top of
  the list near full weight, tapering toward the bottom) rather than a
  raw number. The Settings copy already promises "popularity and
  trending"; only popularity exists so far.

## Product direction

Stewarr should become the missing coordination layer in a modular media stack:
not another specialized media manager, but the place where facts from
specialized tools gain cross-stack context and purpose.

Stewarr must run with any *arr-stack configuration, on any filesystem or even
none Stewarr recognizes — this has always been the goal, even though it was
never written down and the code has drifted from it. Concretely: no service
(Radarr/Sonarr/qBittorrent/...) should be mandatory, neither should direct
filesystem access, and no specific filesystem type should be assumed either —
bare ZFS, reflink-capable filesystems, network filesystems, and anything
Stewarr can't identify at all are all the same case, not special ones. Losing
a piece — a service not configured, no volume mount at all, or a filesystem
Stewarr can't inspect as deeply as ext4 — must mean losing specific,
well-documented abilities (hardlink-bundle detection, Unmanaged discovery,
real disk usage, cross-referencing a torrent against the media it backs),
never breaking the abilities that don't depend on the missing piece: Stewarr
should always do the best it can with whatever it's given, and degrade
gracefully — never fail closed entirely — when it can't. This is a priority
objective, not a someday item — see the top of Near-term.
