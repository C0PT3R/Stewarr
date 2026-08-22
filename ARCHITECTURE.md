# Spartarr / future Togetharr — Architecture and Product Model

This document is the durable source of truth for architectural direction discovered while building the project. The README describes what the current release does; this file records the model we intend to preserve as the application grows.

## Product thesis

The product is not fundamentally a cleanup application. It is a coordination and interpretation layer for a fragmented media stack.

Radarr, Sonarr, Lidarr, Readarr, Seerr, Jellyfin/Plex, download clients, subtitle tools and filesystems each expose useful facts, but each understands only its own domain. The application combines those facts into relationships, context, decisions and eventually safe actions.

**The integrations provide facts. The application provides context.**

Cleanup is one application of that unified knowledge, not the entire product.

The working name in the codebase remains **Spartarr**. Names discussed for the broader product include **Whatevarr**, **Connectarr**, and **Togetharr**. Do not rename code merely because a candidate name appears here; naming remains deliberately unsettled.

## Core architectural rule: facts vs interpretations

Adapters should ingest raw, attributable facts without prematurely converting them into policy.

Examples of facts:

- Radarr reports a MovieFileDeleted event.
- Sonarr imported a release with a given downloadId.
- qBittorrent reports ratio 1.00, 15 seeds, 0 leechers and a content path.
- Jellyfin reports playback history.
- Seerr reports a request.
- stat reports device, inode, link count and size.
- a storage pool reports capacity and free space.

Examples of derived interpretations:

- this torrent is associated with current library media;
- this torrent was superseded by a later import;
- this torrent is orphaned because its former library representation was deleted;
- removing this storage claim would reclaim approximately 2.75 GiB;
- a media item has a particular Strength;
- a torrent has a particular retention value;
- a proposed set of actions is the least-destructive way to restore a storage target.

Keep evidence/provenance so interpretations can evolve without re-fetching or losing the underlying facts.

## Unified model

The long-term model should distinguish logical content, representations, relationships and storage claims.

Conceptually:

    Logical content
    ├── library representation(s)
    ├── torrent/download representation(s)
    ├── playback/request/metadata facts
    └── storage claims
        ├── storage pool A
        └── storage pool B

A library representation and a torrent representation are not the same object even when their files are hardlinked. Removing one may or may not reclaim bytes depending on storage topology.

A storage claim should eventually be able to express at least:

- pool/device identity;
- paths/files involved;
- logical byte size;
- shared vs unique data when detectable;
- reclaimable bytes for a proposed action;
- dependencies/relationships;
- retention/value information;
- confidence/evidence behind the claim.

## Storage pools and targets

Storage policy should eventually be **per storage pool/device**, not globally assumed to be one disk.

Example:

    Library pool   target 90%
    Downloads pool target 85%

Configured paths that resolve to the same underlying storage should be recognized as the same pool when runtime capabilities allow it.

This matters because optimal actions depend on which pool is pressured. With separate download and library disks, deleting a stale torrent can reclaim download capacity while retaining media; deleting library media can reclaim library capacity while retaining the torrent. With hardlinks on one filesystem, either deletion alone may reclaim zero bytes.

### Target semantics

The desired model uses a single operational **Target** per pool:

- usage <= Target: no reclamation required;
- usage > Target: reclaim only enough to return to <= Target.

The project does **not** require hysteresis merely because other cleanup tools use high/low watermarks. Refreshes can occur hourly or every few hours; scanning is expected to be cheap enough that continuous threshold flapping is not a concern.

A future **Critical** threshold may exist, but only as an urgency/alert/emergency-policy concept. Critical should not normally decide when routine cleanup begins.

Current code may still implement Critical as a cleanup trigger. Treat that as legacy behavior to simplify, not the intended final model.

## Reclamation philosophy

When a new item arrives and causes a pool to exceed Target, the application should choose the least-destructive set of actions needed to restore Target — no more and no less.

The planner should prefer reclaiming storage without sacrificing valued content when possible, but states such as `orphaned` and `superseded` are context, not automatic death sentences.

Possible claims/actions include:

- redundant/duplicate storage;
- superseded torrent data;
- orphaned torrent data;
- stale/low-value torrent data;
- library representations;
- eventually other safely understood storage claims.

A superseded torrent can still be valuable if it is actively serving a swarm. An orphan can still be worth keeping when capacity is plentiful. Conversely, on a separate downloads disk, a stale torrent may be the cheapest thing to remove while retaining its library media.

The long-term goal is therefore closer to:

> Maintain the highest-value media/storage ecosystem that fits within the capacity the user has assigned.

## Value models

### Media Strength

**Strength** is the retention value of a media item. It answers: "How strongly should this media remain in the library?"

Strength should not be confused with reclaimable bytes. A 5 GiB movie and 500 GiB series can have equal Strength while having very different utility in a reclamation plan.

Series-level signals must be normalized so episode count does not automatically inflate value. One viewing of a 100-episode series should not count like 100 movie views.

External review ratings should eventually account for media-type distribution. Series ratings tend to be inflated relative to movie ratings, so a numeric 8.0 should not necessarily contribute identical Strength for movies and series. Separate curves are a practical first step; percentile/source normalization is a possible later model.

### Torrent retention value

Torrents should eventually have their own retention model rather than inheriting media Strength. Candidate signals include:

- current upload activity;
- leechers/demand;
- seeds/availability/rarity when reliable;
- ratio;
- seeding time;
- last activity;
- age;
- tracker/category/tags;
- size/reclaimability;
- explicit protection or user policy;
- provenance state (associated/superseded/orphaned/unassociated) as context.

Torrent value and media Strength can remain domain-specific while the planner compares the cost/value of available actions.

## Torrent provenance

Current provenance vocabulary is intentional and should remain ordinary rather than themed:

- **Associated** — backs current library media/current import.
- **Superseded** — previously backed media but a later release/import replaced it.
- **Orphaned** — a known former relationship exists, but the corresponding current library representation no longer exists.
- **Unassociated** — the application cannot prove a relationship.

**Unassociated must never be treated as synonymous with orphaned.** Matching can fail; lack of evidence is not evidence that deletion is safe.

Historical *arr associations are valuable provenance and must not be discarded when imports change. They allow the system to distinguish "I do not know what this is" from "I know exactly what this used to be."

A concrete observed lifecycle:

    qBittorrent download
      -> Radarr import
      -> library + torrent representations
      -> library file deleted through Radarr/Maintainerr
      -> torrent remains
      -> torrent path has nlink=1
      -> known orphan with uniquely reclaimable storage

Another expected lifecycle:

    torrent A imported
      -> later torrent B imported as upgrade
      -> A remains seeding
      -> A is superseded, not merely orphaned

## Storage accounting

Never equate media size, torrent size or deleted-path size with bytes actually reclaimed.

For ordinary hardlinks, use device+inode identity and account for all links in the proposed deletion set. `nlink > 1` alone is insufficient if every link to the inode is itself being removed.

Keep these questions separate:

- **May this be removed?** — provenance, relationships, policy and retention value.
- **What happens if it is removed?** — filesystem/storage analysis and reclaimable bytes.

Runtime capability detection should govern which claims the application can make. Important storage features include:

- stable file identity;
- hardlink support/detection;
- filesystem/device identity;
- reflink/shared-extent detection;
- snapshots;
- block-level deduplication;
- remote/NFS path visibility and mappings;
- exact vs estimated reclaimability.

Unsupported capabilities should be reported as unavailable, not guessed.

## Integrations

Current integrations are the first adapters, not privileged permanent architecture. Future support should include at least:

- Radarr;
- Sonarr;
- Lidarr;
- Readarr (where applicable/maintained ecosystem equivalents);
- Seerr;
- Bazarr;
- Jellyfin;
- Plex;
- qBittorrent;
- Transmission;
- Deluge;
- other major download/torrent clients;
- storage/filesystem providers/capabilities.

The core should avoid spreading `if radarr`, `if sonarr`, `if qbittorrent` logic throughout the application. Integrations should expose capabilities/facts through adapters.

Conceptual capabilities include:

- library/catalog;
- import/history/provenance;
- requests;
- playback;
- downloads/torrents;
- subtitles/auxiliary metadata;
- actions;
- storage observations.

Domain-specific models still matter: an album is not a movie and a subtitle is not a torrent. Capability interfaces should unify what is genuinely common without flattening useful semantics.

### Multiple instances

Do not assume one instance per product. The eventual model should support configurations such as:

- Radarr — Movies;
- Radarr — 4K Movies;
- Sonarr — TV;
- Sonarr — Anime;
- qBittorrent — Public;
- qBittorrent — Private.

Integrations should eventually become database-backed entities rather than fixed singleton configuration blocks.

### Graphical integration management

The intended normal UX is graphical:

    Settings -> Integrations -> Add integration

Users choose an integration type, provide URL/credentials as needed, test the connection, save it, and the application discovers supported capabilities. Raw JSON/YAML should not be the primary long-term configuration experience.

## UI principles

The application should explain decisions without making lists dense.

- Home is a system overview and health/pressure dashboard, not a duplicate of Library.
- Library and Torrents are first-class surfaces.
- Detail pages carry explanations/provenance; list pages remain scannable.
- Dashboard counts should become links into corresponding filtered views.
- Library and Torrents must use server-side search, filtering, sorting and pagination so tens of thousands of rows remain practical.
- Query order is: **search/filter -> sort -> paginate**.

### Torrent filters/search

Initial useful torrent filters:

- provenance state;
- client/category;
- reclaimability known/unknown/>0;
- active/inactive;
- tracker/tags later.

Torrent search should eventually match:

- name;
- hash;
- tracker;
- category;
- tags;
- associated media title.

### Library filters/search

Useful library filters/search include:

- title;
- media type;
- Strength range;
- torrent/provenance state;
- requested/not requested;
- watched/unwatched;
- additional domain-specific facts as integrations grow.

## Refresh/event model

Do not assume every upstream application is event-driven. Periodic reconciliation remains authoritative and robust. Incremental APIs/history cursors should be used where possible. Webhooks/events can later reduce latency, but event delivery should not become the only source of truth.

An hourly or few-hour refresh cadence is acceptable for routine storage maintenance; there is no requirement to poll every five minutes.

## Persistent state

SQLite is required for durable correlation, history and statistics. The database path is intentionally not configurable:

    /config/state/spartarr.db

The repository/database should preserve enough provenance to reinterpret historical relationships as the model improves.

## Statistics and auditability

The application should keep durable statistics such as:

- bytes reclaimed;
- library bytes removed;
- torrent bytes removed;
- media/torrents removed;
- cleanup runs;
- before/after pool usage;
- reason and evidence for each action;
- score/value at action time.

Every destructive decision should eventually be explainable. The product should answer not merely "what was removed?" but "why was this the least-destructive choice given the state known at that time?"

## Safety posture

The application is currently non-destructive. When actions are introduced:

- provenance confidence and storage effect remain separate;
- unknown/unassociated objects are not assumed safe;
- actions should be auditable;
- dry-run/preview should remain available;
- capability-dependent behavior must fail closed rather than guess;
- users may configure policy, but defaults should not silently destroy data on uncertain evidence.

## Scope discipline

Ambition is allowed; simultaneous implementation of every implication is not required.

When a future idea is valuable but premature, preserve the data foundation and architecture needed to support it rather than implementing a half-finished feature immediately.

The repository — code, tests, schema and these documents — is the source of truth. Conversation is where ideas are explored. Decisions become authoritative when they are recorded here and/or embodied in tested code.
