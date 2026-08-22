# Spartarr

> **Architecture note:** the project is evolving beyond media cleanup into a coordination/context layer for the wider media stack. `ARCHITECTURE.md` records the durable product model and `ROADMAP.md` separates implemented behavior from intended work. The README remains the description of the current release.

> **Here in Spartarr, your media compete to keep the disk space they claim as their own. Only the strongest shall survive.**

Spartarr is a storage-pressure media cleanup planner for Radarr and Sonarr libraries. It combines metadata and activity from Radarr, Sonarr, Jellyfin, Seerr and qBittorrent into a **Strength** score for each media item. Higher Strength means the item has a stronger claim to remain when storage becomes scarce.

Spartarr deliberately uses the terminology of the applications and protocols it integrates with: media, torrents, requests, ratings, watch history, hardlinks, seeders, leechers and so on. **Strength** is Spartarr's own core concept; everything else should feel familiar to users of the *arr ecosystem.

## Version 0.1.4

0.1.4 is still intentionally non-destructive. It inventories media, calculates Strength, ranks the library, tracks torrents, persists state and previews cleanup. It exposes no delete endpoint.

This iteration adds torrent provenance and hardlink-aware reclaimable-space inspection for obsolete torrent data. Spartarr now distinguishes **Associated**, **Superseded**, **Orphaned**, and **Unassociated** torrents, shows why a classification was made, estimates reclaimable bytes when the torrent content path is visible, aggregates that space on Home/Torrents, and lets the torrent table sort by reclaimable space.

The SQLite database is an implementation detail and always lives at:

```text
/config/state/spartarr.db
```

Spartarr creates the `state` directory when needed. `/config` must be writable by the user running the container.

## Application structure

The main UI is split into four areas:

- **Home** — storage pressure, cleanup explanation, library/torrent summaries, service state, storage capabilities and lifetime cleanup statistics.
- **Library** — ranked media, Strength, size and media metadata.
- **Torrents** — torrent association and qBittorrent activity/details.
- **History** — durable cleanup statistics and cleanup-run history. It remains empty while Spartarr is non-destructive.

The dashboard is intended to answer both **what is happening?** and **why?**. If cleanup is inactive it explains that current usage is below the critical threshold. If cleanup is active it shows how much space is required to return to Target and how many media items are currently in the previewed deletion set.

## Cleanup model

1. Every managed media item is potentially removable.
2. Scoring rules contribute **Strength**. Strength is not artificially bounded.
3. Media is ranked weakest first. If Strength is equal, larger size breaks the tie when size is not itself part of Strength.
4. No cleanup is needed while storage usage is below `critical_usage_percent`.
5. At or above the critical threshold, Spartarr calculates the bytes required to return to `target_usage_percent`.
6. It selects the weakest media in one batch until their cumulative storage claim meets or exceeds that requirement.
7. 0.1.4 previews that selection only; it does not delete anything.

`critical_usage_percent` must be greater than or equal to `target_usage_percent`. Percentages are the only storage threshold unit for now.

There is no separate candidate class. The ranking itself determines which media would be removed if storage pressure requires it.

## Strength

Strength is a positive retention score: higher means more likely to remain. Current inputs can include ratings, watch activity, library age, popularity, Seerr request information, favorites, configured keep tags and torrent activity.

The Library defaults to weakest-first. A media detail page shows the complete Strength breakdown so the reason for a ranking can be inspected without making the main Library table dense.

## Torrents and qBittorrent

Spartarr associates qBittorrent torrents with media using Radarr/Sonarr import-history `downloadId` values and torrent hashes. It deliberately does not establish associations from fuzzy filename matching.

Torrent activity can contribute to the associated media's Strength. `scoring.weights.torrent_activity` controls that contribution; set it to `0` to collect and display torrent information without allowing it to affect Strength.

Torrent provenance states are:

- **Associated** — the torrent hash matches the latest imported release for current library media.
- **Superseded** — a later torrent/release was imported for the same Radarr movie or Sonarr episode.
- **Orphaned** — the torrent was imported by Radarr/Sonarr, but no current library media claims that import.
- **Unassociated** — Spartarr cannot prove an association. This does **not** mean the torrent is orphaned or safe to remove.

Superseded and orphaned torrents are inspected when their `content_path` is visible under the configured storage path. Spartarr walks the torrent content, reads filesystem link counts, and reports:

- bytes uniquely held by that torrent path and therefore reclaimable if the data is removed;
- bytes still shared through hardlinks;
- files inspected and files with multiple hardlinks;
- an explicit unknown/error state when the path cannot be inspected.

The estimate is intentionally described as hardlink-aware rather than universally exact. Filesystem snapshots, reflinks, block-level deduplication, remote path mappings, or storage invisible to Spartarr can change the number of bytes actually returned to the pool. Provenance answers **may this be obsolete?**; storage inspection separately answers **what would deleting its data accomplish?**

The Torrents list is server-side paginated and sortable, just like the Library. It defaults to 50 rows per page and supports 25, 50, 100 and 250.

Media detail pages show only torrent information relevant to that media. Full qBittorrent diagnostics live on the torrent detail page.

## Persistent state and refreshes

Spartarr keeps durable state in `/config/state/spartarr.db`. It stores cached media/torrent state, qBittorrent synchronization state and indexed *arr import associations.

The first run against a long-running Radarr/Sonarr installation may take time because existing import history must be indexed once. Later refreshes stop when they reach already indexed history. qBittorrent uses incremental `/sync/maindata` synchronization. Inventory publication is staged so current media can appear before slower torrent/history enrichment completes.

Periodic refresh is still used as reconciliation. Future webhook/event support can update state more quickly without making event delivery the only source of truth.

## Cleanup history and statistics

The database now contains durable cleanup-run and cleanup-action structures. They are intentionally not populated from previewed plans.

When deletion is implemented, each real cleanup run can record:

- storage usage before and after;
- Target and Critical values used for that run;
- planned bytes;
- library bytes removed;
- actual bytes reclaimed;
- media removed;
- torrents removed;
- status and reason;
- per-item Strength and action details.

Spartarr keeps **library bytes removed** separate from **actual space reclaimed**. This distinction matters for hardlinks and other shared-storage arrangements: removing a 40 GiB library entry does not necessarily free 40 GiB of filesystem blocks.

The Home dashboard and History page already read these durable statistics. They remain zero until Spartarr performs real cleanup rather than pretending previewed bytes were saved.

## Storage capabilities

The dashboard performs an initial runtime capability inspection for the configured storage path. On Linux it currently reports:

- whether the path is visible;
- detected filesystem type;
- whether stable file identity is available;
- whether hardlink identity checks are supported;
- whether shared-extent/reflink detection is implemented;
- whether exact reclaim calculations are implemented.

Spartarr now performs hardlink-aware reclaim inspection for superseded and orphaned torrent content paths when they are visible beneath `storage.path`. The general **Exact reclaim estimate** capability remains unavailable because snapshots, reflinks, deduplication and other storage topologies are not yet modeled. Unsupported capabilities remain explicitly unavailable rather than being guessed.

The long-term rule is: detect capabilities at runtime and only enable functionality when the underlying storage/client topology supports it safely.

## Configuration

Start with `config.example.json`. A minimal layout looks like:

```json
{
  "server": {
    "listen": ":8088",
    "refresh_interval": "30m"
  },
  "radarr": {
    "url": "http://radarr:7878",
    "api_key": "..."
  },
  "sonarr": {
    "url": "http://sonarr:8989",
    "api_key": "..."
  },
  "jellyfin": {
    "url": "http://jellyfin:8096",
    "api_key": "..."
  },
  "seerr": {
    "url": "http://seerr:5055",
    "api_key": "..."
  },
  "storage": {
    "path": "/data",
    "target_usage_percent": 90,
    "critical_usage_percent": 95
  }
}
```

The database path is not configurable. Application state belongs under `/config/state`.

## Docker

The default Compose layout uses a writable configuration directory and a read-only media mount:

```yaml
services:
  spartarr:
    build: .
    container_name: spartarr
    restart: unless-stopped
    user: "${PUID:-1000}:${PGID:-1000}"
    ports:
      - "8088:8088"
    volumes:
      - ./config:/config
      - /mnt/media:/data:ro
```

Set `PUID` and `PGID` to the host account that owns `./config` when they are not `1000:1000`. For example:

```sh
PUID=$(id -u) PGID=$(id -g) docker compose up -d --build
```

Create `./config` before starting Docker. If Docker creates a missing bind-mount directory itself, the host directory is commonly created as `root:root`.

SQLite needs write access to the **directory**, not only `spartarr.db`, because it may create journal/WAL files beside the database.

The `/data` mount must refer to the filesystem configured by `storage.path`. If that filesystem cannot be measured, Spartarr reports storage as **UNAVAILABLE** and disables cleanup planning rather than reporting a misleading `0%` usage.

## First run

```sh
mkdir -p config
cp config.example.json config/config.json
$EDITOR config/config.json
PUID=$(id -u) PGID=$(id -g) docker compose up -d --build
```

Open `http://SERVER:8088`.

## Pagination and sorting

Both Library and Torrents use server-side pagination. The browser receives only the page being displayed rather than tens of thousands of rendered rows.

Library defaults to Strength rank (weakest first). Torrents can be sorted by provenance state, name, media count, qBittorrent state, size, reclaimable bytes, ratio, upload rate, seeders, leechers and last activity. Sorting is applied before pagination.

## Development from another machine

```sh
make deploy REMOTE=myuser@myserver REMOTE_DIR=/opt/spartarr
```

The repository defaults remain:

```makefile
REMOTE ?= user@your-server
REMOTE_DIR ?= ./servarr/spartarr
```

`make deploy` creates the remote config directory as the SSH user and starts Compose with that user's numeric UID/GID.

## Safety

`config.json` currently contains reserved deletion flags:

```json
"safety": {
  "allow_delete": false,
  "auto_cleanup": false
}
```

They remain intentionally unused in 0.1.4. `/api/plan` is preview-only.

## Current limitations

- Sonarr currently ranks whole series rather than individual seasons or episodes.
- Popularity currently uses upstream rating vote count as a proxy.
- Jellyfin usage is gathered from each user's item UserData and matched by provider IDs.
- Torrent provenance currently relies on Radarr/Sonarr import history. Very old or manually imported media may therefore remain unassociated even when related torrent data exists.
- qBittorrent content paths must be visible under Spartarr's configured `storage.path` for reclaim inspection; path mapping for differently mounted/remote clients is not implemented yet.
- Hardlink-aware reclaim inspection currently covers visible superseded/orphaned torrent content. Full cleanup-plan reclaim accounting across all media/torrent relationships is not yet implemented.
- Reflink/shared-extent and ZFS snapshot/dedup-aware reclaim calculations are not implemented.
- Only qBittorrent is currently implemented as a torrent client.
- Only one instance of each configured service is currently supported.
- No automatic deletion is implemented.
- Cleanup statistics remain zero until real cleanup execution exists.
- Missing or zero-byte media is excluded because it has no storage claim to reclaim.

## Origins

Spartarr began as an experiment in storage-pressure-based media cleanup. Before the name **Spartarr** was chosen, its first proposed name was the regrettable **Shovitupyoarr**. Better judgment eventually prevailed.

The name changed; the central idea survived: when storage is scarce, media is ranked by Strength and the weakest items are the first considered for removal.
