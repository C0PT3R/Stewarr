# Togetharr — Architecture and Product Model

## Product identity

Togetharr is the coordination layer between specialized media applications. It should not duplicate Radarr, Sonarr, Jellyfin, qBittorrent, Seerr, Bazarr, Lidarr, Readarr or storage systems.

Core principle:

> **Integrations provide facts. Togetharr provides context.**

The unified model is the product. Cleanup is one application of that model.

## Core facts and interpretations

Raw integration data should remain attributable facts: an *arr import event, a qBittorrent hash, Jellyfin playback state, a Seerr request, a filesystem inode/link count, a storage usage reading.

Togetharr then derives interpretations from combinations of facts: Associated, Superseded, Orphaned, Value, reclaimable bytes, storage pressure, safe actions, inconsistencies and health conditions.

Do not blur those layers. A future change in interpretation should not require rebuilding the integration that supplied the facts.

## Main domain concepts

### Media
A logical Library item managed today by Radarr or Sonarr. Future adapters may add albums, books and other content types.

### Media Value
A Library-media retention value. Higher means a stronger claim to remain. Value is not storage size and should not be treated as a deletion eligibility flag.

### Torrent
A download/share representation managed by a torrent client. Torrent retention value is conceptually separate from Library Value.

### Torrent Value
An independent, explainable retention value derived from current swarm facts. It is not inherited from Media Value and does not include storage cost.

### Provenance
Historical relationships between downloads/torrents and managed media. Preserve history even after a release is replaced; old associations enable safe classification of superseded/orphaned data.

### Storage claim
A representation occupies bytes on a storage pool. Library and torrent representations can be independent copies, hardlinks to one inode, reflinks/shared extents, remote data or unknown relationships.

### Storage pool
Target/pressure ultimately belongs to a storage device/pool, not globally to the application. Multiple configured paths may map to the same underlying pool and should eventually be detected as such.

## Reclaimability

Two questions must remain separate:

1. **May this representation be removed?** — provenance, policy, requests, retention value, tracker constraints, user protection.
2. **What happens if it is removed?** — filesystem/storage analysis and actual reclaimable bytes.

For ordinary local hardlinks, device+inode identify the same file. Link count is important but not sufficient by itself: when evaluating a deletion set, all paths to an inode must be considered together.

Future storage inspectors may add filesystem-specific capabilities for reflinks/shared extents, ZFS behavior, Windows file IDs and network filesystems. Unsupported cases should return Unknown rather than pretend exactness.

## Torrent provenance states

- **Associated** — authoritative current relationship to Library media.
- **Superseded** — historical import replaced by a newer import for the same media/episode.
- **Orphaned** — authoritative historical relationship exists, but current Library media no longer claims the import.
- **Unassociated** — no authoritative relationship established.

Unassociated must never be treated as synonymous with orphaned.

## Unclaimed download data

A separate storage class: files in download roots that no configured torrent client claims.

Detection must fail closed. Ownership absence is valid only from a complete client file inventory. Scan results are slower/deeper maintenance data and should not live on the normal refresh critical path.

## Task model

Background work should be represented as tasks. Scheduled and manual **Run now** executions must use the same implementation.

Current tasks:

- Inventory refresh — incremental/cheap, frequent.
- File reconciliation — integration/file-identity reconciliation, sparse (currently 12h).
- Unclaimed download scan — filesystem-heavy, sparse (currently 12h).

Future tasks may include full reconciliation, storage capability scans, statistics maintenance and integration-specific reconciliation. Schedules should eventually be configurable through the GUI.

## State and persistence

SQLite is durable state at:

```text
/config/state/togetharr.db
```

The path is deliberately not configurable. Togetharr migrates the legacy Spartarr database filename when appropriate.

SQLite stores cached media/torrent/unclaimed state, import provenance, synchronization cursors, cleanup history/statistics and future integration/task state.

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

Critical is not needed for normal hysteresis. If retained later, it should represent urgency/alerting or emergency policy behavior, not the ordinary cleanup trigger.

Targets should eventually be per storage pool.

## Planner direction

The planner should minimize lost value while satisfying storage constraints. It must reason about representations independently.

Possible reclamation sources include:

- unclaimed download data;
- redundant/duplicated storage;
- superseded torrent data;
- orphaned torrent data;
- low-retention torrents;
- lower-cost media representations/quality changes (future);
- low-Value Library media.

These are not necessarily a rigid priority list. A valuable active superseded torrent may be worth retaining when capacity permits.

The long-term optimization question is:

> **Given current storage pressure, what set of safe actions returns each pressured storage pool to target with the least loss of total value?**

## Integrations

Do not let core logic become hard-coded around Radarr/Sonarr/qBittorrent singleton assumptions.

Future integration entities should support:

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

Togetharr must explain decisions, not merely scores.

A cleanup plan should answer:

- Why is action required now?
- How many bytes must be reclaimed?
- Which storage pool is pressured?
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
- Keep deletion disabled until classification/reclaim calculations are trustworthy.
- Never equate file/media size with reclaimed filesystem bytes without storage evidence.
- Support capabilities conditionally rather than pretending every filesystem/topology behaves the same way.

## Data economy and lazy loading

Togetharr is a cross-stack model, not a replica of every integrated application's database.

The default rule is: **cheap scans, a small durable model, and lazy details**.

Persist data when Togetharr needs it for stable identity, cross-application relationships, provenance/history, search/filter/sort, decisions, sync cursors, or expensive filesystem reconciliation. Derive interpretations from those facts when practical. Details that remain authoritative and cheap to retrieve from the owning application should be fetched on demand and normally not persisted.

List pages must be renderable from indexed state and must not cause one remote detail request per row. Detail pages may enrich one selected object from its owning application. Heavy filesystem or whole-client reconciliation belongs in an explicit sparse task rather than the routine inventory refresh.

Media, files, torrents, source ownership, and physical storage identity are distinct concepts. The filesystem establishes which Files exist and their physical identity; integrations declare claims on those existing Files; Togetharr reconciles the two. Existing Files with no claims are Unclaimed, while claims with no matching File are missing claims rather than phantom Files. A media entity remains valid with zero files. A File is deliberately content-agnostic so subtitles, text files, ebooks, or other regular files can participate without changing the core storage object.


## Media detail relationship presentation
Media pages expose torrent provenance bidirectionally: current, superseded, and historical/orphaned torrent relationships are grouped rather than flattened. Torrent release names are the primary human identifier; relationship status is structural rather than repeated on every row.


### First-class File model

The durable file inventory is filesystem-first. A successful reconciliation validates integrations, discovers their storage roots, scans those roots for regular files, records path/size/device/inode/link-count facts, then applies integration claims. Symlinks are not followed. A failed or partial generation is never published over the last authoritative one.

Physical identity is authoritative for storage relationships: paths with the same device/inode are the same physical File. Ownership never overrides that fact, and physical identity never bypasses ownership authority. Owned paths are manipulated through their integration; Unclaimed paths may be unlinked directly by Togetharr after revalidation. Removal plans may traverse proven physical siblings, including between an owner-backed path and an Unclaimed hardlink, but filenames and historical Media associations never authorize destructive relationships.

Space consequences are calculated from the physical graph. A physical file is freed only when the selected actions remove every known filesystem link, and an incomplete hardlink set is surfaced as a warning rather than guessed away.


## Relationship projection

Torrent/media provenance is bidirectional at the Togetharr model boundary. Current torrent `MediaItems` and historical `FormerMediaItems` are both projected onto the related Media for navigation and explanation. Historical torrent relationships do not by themselves contribute to current media Value.


## Removal architecture

All manual removal flows use a mandatory `RemovalPlan`: request -> targeted file re-stat -> topology-aware consequence calculation -> confirmation -> revalidation -> owner-driven execution -> History -> reconciliation. Managed data is never removed directly from the filesystem. Radarr/Sonarr own Media removal; qBittorrent owns Torrent removal and torrent data. Direct OS removal is reserved for explicitly selected Files that are still Unclaimed at revalidation.

`Unassociated` is a Torrent provenance state. `Unclaimed` is a File/storage state. The terms are intentionally not interchangeable.

`removal.dry_run` is the central execution gate and defaults to true. A dry run follows the same planning/revalidation path but skips irreversible calls and records a removal History event with status `dry_run`.


## Shared UI shell

All primary pages use a single application chrome driven by `AppInfo` (`Name`, `Version`). Browser history is reserved for real page navigation. RemovalPlan is transient modal state: opening it, changing selections, cancelling it, and recalculating it do not create navigation entries.

Removal planning remains a domain boundary. A Media can open a RemovalPlan whenever its removal graph contains at least one removable resource. The initiating object is only context: every eligible Media or Torrent action, including the initiator, is independently selectable. An empty selection is never executable. These rules are enforced below the UI so future API/CLI callers cannot bypass them.

## Removal action model

Removal is built from concrete owner-backed resources. `Media` is logical context and a selection grouping; it is not deleted by Togetharr. Managed-file actions reference `MediaFileRef` records carrying the owning integration and owner file ID. Radarr actions delete MovieFiles; Sonarr actions delete EpisodeFiles. Torrent and Unclaimed File actions remain separate primitives.

A Media-originated plan may expose any subset of its managed files. Sonarr episode files are grouped by owner-derived season/episode metadata for presentation. A Torrent-originated plan may expose only managed files whose correspondence to a torrent file is proven from authoritative physical identity or future authoritative provenance. Filename/path similarity is never sufficient evidence for a destructive linked action.

Owner state changes are explicit options layered above managed-file removal. Manual plans leave monitoring unchanged by default and may request unmonitoring of affected Radarr movies or Sonarr episodes. Only successfully removed managed files contribute to those follow-up updates.
