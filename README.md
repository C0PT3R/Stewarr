# Stewarr

> **Your Arr stack knows what files it manages. Your filesystem knows what's actually using space. Stewarr connects the two — including hardlinks — so you can see what's truly reclaimable, and why.**

Stewarr is a coordination and storage-intelligence layer for self-hosted media stacks. It integrates Radarr, Sonarr, Jellyfin, Seerr and qBittorrent, and adds the cross-service context none of them have on their own: which torrents still actually back your library, which files are physically the same data via hardlinks, and which media is safest to remove when storage gets tight.

Stewarr does not try to replace any of those applications. Services provide facts; Stewarr provides context across them.

> **Status:** Stewarr is a work in progress. Core flows (inventory, removal planning, storage accounting) are functional and used daily, but interfaces and configuration may still change between releases. See `CHANGELOG.md` for release history, `ROADMAP.md` for planned direction, and `ARCHITECTURE.md` for the full technical model.

## What it does

- Correlates Radarr/Sonarr library media with qBittorrent torrents using authoritative import data, not fuzzy title matching.
- Proves whether a torrent's files are still physically hardlinked to your library media, so you know what removing it would *actually* free — not just what it appears to occupy.
- Ranks library media by **Retention Value** (rating, watch activity, requests, favorites, and more) to help decide what's safest to remove first when storage gets tight.
- Discovers files on disk that none of your services claim ownership of ("Unmanaged").
- Plans every removal as a dry-run-by-default, re-validated `RemovalPlan`, executed through the owning service's own API (Radarr/Sonarr/qBittorrent) rather than deleting files directly.

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

1. `docker compose up -d`, then open `http://<host>:8088`. Stewarr creates `/config/config.json`, its database, and its log directory on first start if they don't already exist — no manual setup required.
2. Follow the first-run screen to create the admin account, then add Radarr/Sonarr/Jellyfin/Seerr/qBittorrent from the Services page. Manually editing `config.json` (see [`config.example.json`](config.example.json) for the shape) is still possible but is meant for debugging or scripted setups, not routine service configuration.

## Navigation

- **Home** — storage state, Library/Torrent summaries, service health, storage capabilities and lifetime cleanup statistics.
- **Library** — searchable, filterable, sortable, server-paginated Radarr/Sonarr media ranked by Retention Value.
- **Torrents** — searchable, filterable, sortable, server-paginated qBittorrent inventory with provenance and reclaimable-space information.
- **Unmanaged files** — observational inventory of paths that no current service claims; not directly removable.
- **Tasks** — background maintenance tasks, their schedules/status, and **Run now** controls.
- **History** — durable event history. Removal simulations and live removal outcomes are recorded here.

## Core concepts

### Retention Value

A retention score for library media — higher means more valuable to keep when storage becomes scarce. It factors in rating, watch activity, library age, request state, popularity, favorites/keep tags, and torrent health (only when a torrent is proven to still back that media via a hardlink). It's deliberately separate from file size: how much space something *takes* and how much it's *worth keeping* are different questions.

### Torrent provenance

Every torrent Stewarr knows about is in one of three states:

- **Current** — it backs library media right now, proven by import data or by physical hardlink identity.
- **Superseded** — it backed an older release that's since been replaced by a newer import.
- **Unassociated** — no current relationship to library media is established. This isn't proof it's safe to remove; it just means Stewarr has no basis to link it to anything.

### Manual removal and dry run

Every removal — media, torrent, or unmanaged file — is first turned into a plan that shows exactly what would happen, then re-validated immediately before anything actually runs. `removal.dry_run` defaults to **true**: nothing is actually deleted until you turn it off. Managed media is removed only through Radarr/Sonarr, torrent data only through qBittorrent; unmanaged files (nothing else claims them) are the only kind Stewarr deletes directly, and even those go through the same validation path.

### Storage and devices

There's no single configured storage path. Stewarr derives storage devices from the roots each service already reports (Radarr/Sonarr root folders, qBittorrent save paths), grouping roots that resolve to the same physical device. Each device gets independent Target/Critical usage thresholds (defaulting to 90%/95%) — see [`config.example.json`](config.example.json).

## Docker

The Compose example above is the minimal setup. `PUID`/`PGID` should match the user that owns your media/config paths on the host.

## Development

```bash
go test ./...
go build ./cmd/stewarr
```

The real-browser reactivity suite uses Playwright and an installed Chromium:

```bash
PLAYWRIGHT_NODE_MODULES=/path/to/node_modules make test-browser
```

`make deploy`/`make logs` read `REMOTE`/`REMOTE_DIR` from the environment or an optional, gitignored `Makefile.local` — see the `Makefile` for defaults.

## Further reading

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — the full product model and technical architecture.
- [`Scheduler-Spec.md`](Scheduler-Spec.md) — the background task/scheduler contract.
- [`ROADMAP.md`](ROADMAP.md) — implemented behavior vs. planned direction.
- [`CHANGELOG.md`](CHANGELOG.md) — release history.

## Origins

Stewarr began as a storage-pressure cleanup experiment called **Spartarr**. Its first proposed name was the regrettable **Shovitupyoarr**. As it outgrew cleanup-only scope it became **Togetharr**, then **Connarr**, then **Stewarr** when a shorter, less unfortunate name proved preferable.

## License

MIT — see [`LICENSE`](LICENSE).
