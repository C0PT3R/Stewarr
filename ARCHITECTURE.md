# Stewarr — Architecture and Product Model

## Product identity

Stewarr is the coordination layer for the *arr ecosystem and related
applications. Those applications are principally centered on their own data
and responsibilities. Stewarr combines attributable facts and authoritative
actions across them to provide features that none can provide individually.

File and storage management is the first major feature built on that context;
it is not an architectural limit on Stewarr's purpose. Future features may
participate in entirely different domains when a concrete cross-application
need justifies them. Stewarr should not duplicate Radarr, Sonarr, Jellyfin,
qBittorrent, Seerr, Bazarr, Lidarr, Readarr or storage systems.

Core principle:

> **Services provide facts. Stewarr provides context.**

The unified model is the product. Cleanup is one application of that model.

Inventory generations distinguish authoritative base facts from enrichment. Radarr, Sonarr and qBittorrent establish their own mutation ownership. Jellyfin contributes playback/favourite facts and Seerr contributes request facts without joining Stewarr's filesystem namespace. Cached or partially enriched generations may be displayed, but they are explicitly valuation-unreliable and cannot produce automatic cleanup candidates. A failed service disables only capabilities depending on it and never expands another service's authority.

File topology is generation-bound. A reconciliation result is discarded if the authoritative base inventory changes before it is published. Its Files, claims, Unmanaged projection, Media and Torrents are committed atomically as one durable generation; readers cannot enter a replacement transaction halfway through. Direct filesystem removal has a stronger boundary still: at the final unlink boundary, after owner-backed actions finish, Stewarr discovers the live torrent set and refreshes current Radarr/Sonarr file claims rather than treating cached absence as proof.

## Core facts and interpretations

Raw service data should remain attributable facts: an *arr import event, a qBittorrent hash, Jellyfin playback state, a Seerr request, a filesystem inode/link count, a storage usage reading.

Stewarr then derives interpretations from combinations of facts: Current, Superseded, Orphaned, Unassociated, former relationships, Value, reclaimable bytes, storage pressure, safe actions, inconsistencies and health conditions.

Do not blur those layers. A future change in interpretation should not require rebuilding the service that supplied the facts.

## Main domain concepts

### Media
A logical Library item managed today by Radarr or Sonarr. Future adapters may add albums, books and other content types.

### Retention Value
A Library-media retention score. Higher means a stronger claim to remain. Retention Value is not storage size and should not be treated as a deletion eligibility flag.

Protection is an eligibility fact, not a very large Retention Value. Keep tags, configured Jellyfin favorites and recent Seerr requests are absolute protection states and are excluded from automatic candidates regardless of storage pressure.

### Torrent
A download/share representation managed by a torrent client. Torrent Value is conceptually separate from media Retention Value.

### Torrent Value
An independent, explainable retention score derived from a torrent's own history — ratio, realized contribution over time, consistency, seeding time, and private-tracker status. Entirely client-agnostic: every signal is derivable from any torrent-client adapter, not just qBittorrent. It is not inherited from media Retention Value and does not include storage cost.

### Provenance
Historical relationships between downloads/torrents and managed media. Preserve history even after a release is replaced or its media disappears; former association is historical context, not a current torrent state.

### Storage claim
A representation occupies bytes on a storage device. Library and torrent representations can be independent copies, hardlinks to one inode, reflinks/shared extents, remote data or unknown relationships.

### Storage device
Target/pressure ultimately belongs to a storage device, not globally to the application. There is no configured storage path: known devices are derived from the storage roots each service already discovers on its own, and roots resolving to the same physical device are grouped into one device entry.

## Reclaimability

Two questions must remain separate:

1. **May this representation be removed?** — provenance, policy, requests, retention value, tracker constraints, user protection.
2. **What happens if it is removed?** — filesystem/storage analysis and actual reclaimable bytes.

For ordinary local hardlinks, device+inode identify the same file. Link count is important but not sufficient by itself: when evaluating a deletion set, all paths to an inode must be considered together.

Future storage inspectors may add filesystem-specific capabilities for reflinks/shared extents, ZFS behavior, Windows file IDs and network filesystems. Unsupported cases should return Unknown rather than pretend exactness.

## Torrent provenance states

- **Current** — authoritative current relationship to Library media.
- **Superseded** — historical import replaced by a newer import for the same media/episode.
- **Orphaned** — provenance exists (the torrent was managed once) but no specific newer import replaced it, typically because the media it belonged to was removed from Radarr/Sonarr entirely.
- **Unassociated** — no relationship to Library media exists at all, current or historical.

`Orphaned` and `Unassociated` are deliberately distinct: the former still
has a known former relationship in provenance History, the latter never
did. Current relationship state and historical provenance are separate
axes.

## Unmanaged download data

A separate storage class: files in download roots that no configured torrent client claims.

Detection must fail closed. Ownership absence is valid only from a complete client file inventory. Scan results are slower/deeper maintenance data and should not live on the normal refresh critical path.

## Task model

All background and mutation work is represented as registered task definitions.
Periodic, startup, manual **Run now**, event, retry, and workflow triggers use
the same runner and admission path.

The scheduler is domain-neutral. Application composition declares stable task
identities, runners, priorities, interruptibility and static resource claims.
Every accepted trigger has a durable identity and explicit disposition. A
compatible trigger attaches to sufficiently fresh running work or coalesces
into one pending successor; a newer monotonic coverage token cannot be
accidentally satisfied by older work. Callers can wait for the exact trigger
they received without polling.

Shared resource claims may overlap; an exclusive claim conflicts with every
claim on the same named resource. Claims are acquired atomically and remain
held until the runner exits, including during cooperative cancellation.
Priority selects only among resource-safe work. Emergency and mutation work may
request interruption from explicitly interruptible work, while
non-interruptible work reaches its safe boundary. Bounded aging prevents routine
maintenance starvation without aging it above mutation safety.

Periodic schedules are fixed-rate and produce at most one catch-up trigger
after downtime. Cooldowns, debounce windows and retry backoff are represented
as timer-driven not-before times under an injectable clock. Correctness-required
triggers and generic linear workflow instances are persisted in SQLite.
Interrupted idempotent maintenance may be retried; irreversible mutations defer
restart decisions to their domain journal and otherwise require attention.

Stewarr composes a shared owner/filesystem mutation boundary and an exclusive
inventory-publication boundary. Base inventory, Jellyfin enrichment and Seerr
enrichment publish serially. File reconciliation may overlap inventory network
work but cannot overlap a removal. Jellyfin is interruptible and resumes after
its cooldown when it yields to a mutation.

Confirmed removals are first written to the durable domain journal, then
submitted as application-owned exclusive mutation tasks. Browser lifetime only
controls how long the HTTP response waits; it never owns accepted execution.
Queued journal entries are reconciled with scheduler state on restart. A task
that may already have crossed an external side-effect boundary is not blindly
replayed.

A successful or uncertain live mutation immediately advances a generic durable
workflow: Base inventory, then File reconciliation. Further mutations coalesce
or create one necessary successor and retain the five-minute deadline from the
first unsatisfied revision. A qualifying scheduled or manual execution
may satisfy a step. Automatic planning remains paused until the workflow
succeeds. Jellyfin and Seerr enrichment are not workflow steps.

Current tasks:

- Base inventory — Radarr/Sonarr/qBittorrent plus incremental provenance, frequent.
- Jellyfin enrichment — playback/favorites, twice daily and removal-isolated.
- Seerr enrichment — request facts, hourly.
- File reconciliation — service/file-identity reconciliation, sparse (currently 12h).

Future tasks may include full reconciliation, storage capability scans, statistics maintenance and service-specific reconciliation. Schedules should eventually be configurable through the GUI.

## State and persistence

SQLite is durable state at:

```text
/config/state/inventory.db
```

The path is deliberately not configurable, and deliberately generic rather than product-name-specific, since there is exactly one database and no per-product migration path.

SQLite stores cached media/torrent/unmanaged state, import provenance, synchronization cursors, cleanup history/statistics and future service/task state. Logical inventory and reconciliation generations use atomic publication transactions. The shared connection is guarded for both reads and writes so a reader cannot observe a table between its DELETE and replacement INSERT phases.

## Refresh architecture

Preferred long-term pattern:

```text
push events where reliable
+ incremental synchronization
+ periodic reconciliation
        ↓
normalized durable facts
        ↓
derived context / selective recomputation
```

Do not build an elaborate event bus before needed. Persistent state plus incremental polling already solves most current performance problems.

## Storage target semantics

The agreed target model is:

```text
usage <= target  → no reclamation required
usage > target   → reclaim only enough to return <= target
```

Target is per storage device, not global. "Usage" is evaluated against a
device's *usable* capacity — total capacity minus whatever real disk usage
Stewarr cannot attribute to itself (`OtherBytes`: other services on a shared
pool, plus the filesystem's own reserved-for-root blocks, shown as a
distinct, honest line item rather than folded into one ambiguous bucket) —
not the raw disk total. A target of 90% means 90% of what Stewarr actually
has to work with, not 90% of a disk another service already holds a share
of.

A device can also be opted out of automatic removal entirely (independent of
the per-service `AllowAutomaticRemoval` opt-in), for a device that should
never be touched regardless of what's on it.

Critical is independent of reclamation Target. It is reserved for a future
emergency state such as alerting, stopping new downloads or temporarily
inhibiting acquisition. It never gates ordinary reclamation planning, and is
not currently shown anywhere in the UI — the underlying config value still
exists (unused) for when that future state is built.

## Planner direction

The planner should minimize lost value while satisfying storage constraints. It must reason about representations independently. Unmanaged files are never a reclamation source for the planner, under any circumstance: absence of a claim is not ownership, and the planner has no more authority over them than any other automatic mechanism.

Possible reclamation sources include:

- redundant/duplicated storage;
- superseded torrent data;
- unassociated torrent data with former provenance;
- low-Torrent-Value torrents;
- lower-cost media representations/quality changes (future);
- low-Retention-Value Library media.

Torrent Value and Retention Value are never compared directly — a single `torrent_care_percent` dial (50 by default) scales Torrent Value onto Retention Value's scale before the two are ranked together as one unified, ascending-value list per device. Below 50 makes torrents relatively less worth keeping than media at the same dial position; above 50, more. This is the only place the two domains are ever compared numerically, and it's deliberately one dial rather than exposing how either domain's own value is computed.

Within the media side of that ranking, a device's overage is not simply satisfied by whichever media items score lowest overall: it's first measured in aggregate via the unified ranking above (deciding how much comes from torrents vs. media in total, unchanged), then redistributed across each media action's owning service proportional to that service's current footprint on the device. Series consistently outscoring movies in Retention Value — real differential engagement, not a scoring defect — would otherwise mean a device shared between a Movies and a Series library structurally sheds movies first regardless of each library's actual size on that device. The mechanism generalizes past just movies vs. series: any two media services sharing a device (two Radarr instances, a "Movies" and "Movies 4K" root) get the same proportional treatment.

The long-term optimization question is:

> **Given current storage pressure, what set of safe actions returns each pressured storage device to target with the least loss of total value?**

## Services

A service type exists only when Stewarr has a defined adapter for it.
Configuration cannot create a generic service merely by supplying a URL or
filesystem root. The currently defined types are Radarr, Sonarr, Jellyfin,
Seerr and qBittorrent.

Adapters participate through explicit capabilities. An adapter need not
subscribe to the File management layer to contribute playback, request,
catalog, diagnostic or future domain facts. Conversely, a feature may consume
an adapter only when the adapter satisfies that feature's complete capability
contract.

Root discovery does not by itself establish ownership. A future adapter that
declares an exhaustive filesystem root must claim every regular File under it;
its root must be disjoint from every other exhaustive root, and an incomplete
claim inventory makes absence Unknown rather than Unmanaged.

Do not let core logic become hard-coded around Radarr/Sonarr/qBittorrent singleton assumptions.

Future service entities should support:

- multiple instances;
- capability advertisement;
- GUI creation/edit/test connection;
- manager/history providers (Radarr, Sonarr, Lidarr, Readarr);
- request providers (Seerr);
- playback providers (Jellyfin, Plex);
- torrent/download clients (qBittorrent, Transmission, Deluge, rTorrent, etc.);
- auxiliary services (Bazarr);
- storage/filesystem inspectors.

## Explainability

Stewarr must explain decisions, not merely scores.

A cleanup plan should answer:

- Why is action required now?
- How many bytes must be reclaimed?
- Which storage device is pressured?
- Which representations are proposed for removal/change?
- Why are they the least valuable safe choices?
- How many bytes are expected to be reclaimed?
- What is the first surviving cutoff when ranking is relevant?

## Statistics

Every destructive cleanup execution should eventually persist a cleanup run and its actions. Record both apparent representation size and observed/estimated actual reclaimed bytes.

Historical data should be captured from the first destructive release even if the full statistics UI comes later.

## Safety principles

- Prefer authoritative identifiers over fuzzy matching.
- Preserve Unknown when evidence is incomplete.
- Fail closed for ownership/reclaimability decisions.
- Keep deletion disabled until classification/reclaim calculations are Reliable.
- Never equate file/media size with reclaimed filesystem bytes without storage evidence.
- Support capabilities conditionally rather than pretending every filesystem/topology behaves the same way.

## Data economy and lazy loading

Stewarr is a cross-stack model, not a replica of every integrated application's database.

The default rule is: **cheap scans, a small durable model, and lazy details**.

Persist data when Stewarr needs it for stable identity, cross-application relationships, provenance/history, search/filter/sort, decisions, sync cursors, or expensive filesystem reconciliation. Derive interpretations from those facts when practical. Details that remain authoritative and cheap to retrieve from the owning application should be fetched on demand and normally not persisted.

List pages must be renderable from indexed state and must not cause one remote detail request per row. Detail pages may enrich one selected object from its owning application. Heavy filesystem or whole-client reconciliation belongs in an explicit sparse task rather than the routine inventory refresh.

Media, files, torrents, source ownership, and physical storage identity are distinct concepts. The filesystem establishes which Files exist and their physical identity; services declare claims on those existing Files; Stewarr reconciles the two. Existing Files with no claims are Unmanaged observations, while claims with no matching File are missing claims rather than phantom Files. A media entity remains valid with zero files. A File is deliberately content-agnostic so subtitles, text files, ebooks, or other regular files can participate without changing the core storage object.


## Media detail relationship presentation
Media pages expose their Current and Superseded torrent relationships rather than a global set of unrelated torrents. Former relationships remain identified as historical context. Torrent release names are the primary human identifier; relationship status is structural rather than repeated on every row.


### First-class File model

The durable file inventory is filesystem-first. A successful reconciliation validates services, discovers their storage roots, scans those roots for regular files, records path/size/device/inode/link-count facts, then applies service claims. Symlinks are not followed. A failed or partial generation is never published over the last authoritative one.

Physical identity is authoritative for storage relationships: paths with the same device/inode are the same physical File. Proven physical backing establishes a Current torrent/media relationship even when import provenance is absent or stale. Ownership never overrides physical identity, and physical identity never lets Stewarr manipulate a path through the wrong owner. Owned objects are manipulated only through their service. A Media plan may propose a qBittorrent action for a Current torrent; confirming it still delegates the complete Torrent removal to qBittorrent. Unmanaged paths cannot be unlinked directly. Filenames alone never authorize destructive relationships.

Space consequences are calculated from the physical graph. A physical file is freed only when the selected actions remove every known filesystem link, and an incomplete hardlink set is surfaced as a warning rather than guessed away.


## Relationship projection

Torrent/media relationships are bidirectional at the Stewarr model boundary. Authoritative current import provenance and proven device/inode physical backing independently establish Current `MediaItems`; either source is sufficient and neither may demote the other. Historical `FormerMediaItems` establish Superseded context only in the absence of a current relationship. Torrent health contributes to a media's Retention Value only when the relationship is Current and reconciled device/inode identity proves distinct torrent and media paths are hardlinks. Current copied imports, missing files, and unknown topology contribute nothing.


## Removal architecture

All manual removal flows use a mandatory `RemovalPlan`: initial targeted file inspection -> local consequence calculation -> durable admission -> authoritative revalidation inside the scheduler's exclusive mutation boundary -> owner-delegated execution -> History -> reconciliation. The browser calculation is presentation, never deletion authority. A composite plan may contain Radarr/Sonarr managed-file actions and qBittorrent Torrent actions, but every action remains delegated to its declared owner. Unmanaged files have no owner to delegate to, so their removal is a direct OS deletion instead — the one removal kind that mutates the filesystem itself rather than calling an owner's API — but it goes through the exact same admission/revalidation boundary, plus a final physical-identity re-check immediately before deletion to close the last time-of-check gap.

`Unassociated` is a Torrent provenance state. `Unmanaged` is an observational File state. Neither grants deletion authority.

`removal.dry_run` is the central execution gate and defaults to true. A dry run follows the same planning/revalidation path but skips irreversible calls and records a removal History event with status `dry_run`.

Removal execution also emits a complete line-oriented audit to the mandatory application log. Every line carries the removal origin and durable operation ID; plans enumerate selected and preserved paths with topology facts, results are recorded as actions complete, and a terminal line records status and planned reclaimability. The audit never serializes the submitted form wholesale, preventing operation tokens or future sensitive fields from entering logs.

## Application logging

The process has one logging stream mirrored to stdout and `/config/log/stewarr-YYYY-MM-DD.log`. Daily rollover uses local time, restarts append, and the latest ten days are retained without splitting an individual day by size. Log initialization is a startup prerequisite. A later write, rollover, retention, or filesystem failure cancels the application context and terminates the process non-zero; Stewarr never knowingly continues mutations without persistent logging. Each line declares an origin so concurrent scheduler, inventory, HTTP, and removal work remains attributable.

The scheduler emits exactly one terminal summary for each execution. Runners
may attach sorted key/value diagnostics to their execution context; File
reconciliation uses this channel for its mode, stage durations, path count, and
torrent membership cache/fetch counts.

Confirmed removal results merge a durable inventory-owned reconciliation scope
containing affected paths, physical peers, media owners, and removed torrents.
The scheduler remains domain-neutral: its workflow only invokes Base inventory
and File reconciliation. Those runners consume an exact scope by applying a
targeted owner/topology/database delta. Partial or uncertain work marks the
scope full. Missing paths, unexpected identities, owner changes outside scope,
new or moved torrents, and generation conflicts promote to the same existing
full reconciliation path. Startup, periodic, and manual runs are always full,
and their triggers cannot attach to an in-progress targeted execution.


## Shared UI shell

All primary pages use a single application chrome driven by the centralized `product` identity (`Name`, `Slug`, `Version`). Server-rendered HTML remains authoritative; pinned htmx and Stimulus assets provide bounded interaction and ship inside the binary. Browser history is reserved for real page navigation. RemovalPlan is transient modal state: opening it, changing selections, cancelling it, and recalculating it do not create navigation entries.

The UI consumes published/cached application facts only. A versioned dashboard
snapshot is invalidated by inventory, storage samples, database commits, task
state, and mutation projections. SSE announces revisions; conditional polling
is the fallback. Neither mechanism calls services or scans filesystems.

Pending operations form a central projection over published inventory. Durable
admission suppresses selected objects immediately across Home, lists, and
details. Stale inventory cannot resurrect them while execution or consistency
reconciliation remains pending. Failure removes the suppression and publishes
a persistent History-linked error. Background additions and reorderings do not
move an open list; they expose an **Updates available** action. Removals are the
exception and always disappear immediately.

Removal planning remains a domain boundary. A Media can open a RemovalPlan whenever its removal graph contains at least one removable resource. For Media-originated plans, managed files and Current torrents are owner-backed actions coupled through an internal physical graph. Physically backing Current torrents are selected by default; other Current torrents may be selected explicitly; Superseded torrents are non-actionable historical context. Selecting a Torrent removes its complete data set. Unknown external links are preserved and force zero reclaimability. An empty selection is never executable. These rules are enforced below the UI so future API/CLI callers cannot bypass them.

## Removal action model

Removal is built from concrete owner-backed resources. `Media` is logical context and a selection grouping; it is not deleted by Stewarr. Managed-file actions reference `MediaFileRef` records carrying the owning service and owner file ID. Radarr actions delete MovieFiles; Sonarr actions delete EpisodeFiles. Torrent and Unmanaged File actions remain separate primitives.

## Code readability

Names must describe the domain concept they hold. Single-letter and cryptic abbreviated names are acceptable only in a tiny scope where their meaning is unmistakable, such as an index in a short loop or the conventional `w` and `r` parameters of a small HTTP handler. Domain objects, filesystem facts, service responses, plans and persistent records use explicit names. Dense one-line control flow is not accepted merely because Go permits it.

A Media-originated plan may expose any subset of its managed files and related qBittorrent actions whose correspondence is proven from authoritative physical identity. The plan may disclose other owners affected by a multi-file Torrent, but those other Media actions are preserved and cannot be selected through the initiating Media. A Torrent-originated plan may expose only managed files whose correspondence to a torrent file is proven from authoritative physical identity or future authoritative provenance. Filename/path similarity is never sufficient evidence for a destructive linked action.

Owner state changes are explicit options layered above managed-file removal. Manual plans leave monitoring unchanged by default and may request unmonitoring of affected Radarr movies or Sonarr episodes. Only successfully removed managed files contribute to those follow-up updates.

The same boundary supports optional import-list exclusions. Radarr exclusions
require the media's authoritative TMDB ID; Sonarr exclusions require its TVDB
ID. The option is omitted when Stewarr cannot construct a valid owner request,
and exclusions are attempted only for media with at least one successfully
removed managed file.

Removal presentation is derived from the plan without exposing its internal
graph. Media-originated dialogs show actual Movie or Episode filenames; season
rows are disclosure/selection structure rather than card surfaces. Every
Current or Superseded torrent appears exactly once in its relationship group;
Unassociated torrents do not belong to a media plan. When a selected managed
path cannot release its physical bytes because an unselected torrent path
remains hardlinked, the dialog states the consequence and tells the user to
select the blocking related torrent. Detailed paths and physical identity
remain in the collapsed Files and storage diagnostic section. The modal uses a
fixed header and action footer around one scrollable content region.
