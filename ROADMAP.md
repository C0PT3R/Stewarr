# Roadmap / Decision Ledger

This is intentionally lightweight. It separates current work from architectural ideas so future iterations do not confuse "discussed" with "implemented".

## Implemented in 0.1.4

- Home, Library, Torrents and History surfaces.
- Media Strength and breakdowns.
- Server-side pagination and sorting for Library/Torrents.
- SQLite state fixed at `/config/state/spartarr.db`; no DB-path option.
- Incremental qBittorrent sync.
- Radarr/Sonarr import-history indexing.
- Torrent provenance: Associated / Superseded / Orphaned / Unassociated.
- Hardlink-aware reclaim inspection for visible superseded/orphaned torrent content.
- Aggregate known reclaimable torrent storage.
- Storage capability reporting.
- Cleanup preview/history schema; destructive cleanup remains disabled.

## Next useful UI work

- Server-side search for Library and Torrents.
- Server-side filters for Library and Torrents.
- Combine search/filter -> sort -> paginate.
- Make Home counts link to filtered views.
- Torrent filters: provenance, category/client, reclaimability, activity.
- Search torrents by name/hash/tracker/category/tags/associated media.

## Cleanup model simplification

Intended change:

- Target becomes the operational threshold.
- If a pool is above Target, reclaim only enough to return to Target.
- Remove Critical from routine cleanup triggering.
- Critical may later return as an alert/emergency-policy threshold only.
- Do not add hysteresis unless a measured operational problem actually requires it.

## Storage model evolution

- Introduce explicit storage-pool/device model.
- Allow per-pool Target.
- Detect when multiple paths belong to the same underlying pool where possible.
- Model independent library and torrent storage claims.
- Improve reclaimability beyond hardlinks: reflinks/shared extents, snapshots, dedup and remote mappings where detectable.
- Prefer least-destructive reclamation across all claims on the pressured pool.

## Torrent evolution

- Retention/value score for torrents.
- Treat Associated/Superseded/Orphaned as context, not automatic deletion policy.
- Consider ratio, seeding time, activity, leechers, seeds/rarity, tracker/category/tags, age and explicit protection.
- Allow useful superseded/orphaned torrents to remain when capacity permits.
- Eventually support safe torrent removal actions with audit history.

## Strength evolution

- Normalize series activity so episode count does not inflate Strength.
- Normalize review-rating contribution by media type; series and movie numeric ratings should not automatically mean the same thing.
- Consider source-specific/percentile normalization later.

## Integration evolution

- Refactor toward capability-based adapters before integration count grows significantly.
- Support multiple instances of the same integration.
- Add graphical Settings -> Integrations management with Test Connection.
- Future adapters: Lidarr, Readarr/ecosystem equivalent, Bazarr, Plex, Transmission, Deluge and other major clients.

## Product direction

The product is a coordination/context layer for the media stack. Cleanup is one use of the unified model.

Working code name: Spartarr.
Naming candidates discussed: Whatevarr, Connectarr, Togetharr.
No rename decision is recorded yet.
