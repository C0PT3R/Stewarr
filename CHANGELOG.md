# Changelog

Notable changes worth calling out beyond ordinary bug fixes. See `git log` for full history.

## 0.1.10 — UI

Media detail pages group torrent relationships into current, superseded, and unassociated sections, with former relationships identified as historical context and release names shown as the primary identifier.

## 0.1.11 — media relationship/UI corrections

- Media detail pages receive torrent relationships bidirectionally from both current and historical provenance. Superseded and formerly related torrents therefore appear on the media they previously backed.
- Historical torrents are visible context only for media Retention Value; torrent activity contributes only from current, physically hardlinked torrents.
- The Torrents table shows historical media for superseded or formerly related torrents instead of an unexplained dash when provenance is known.
- The media detail page was compacted into a denser profile layout with summary panels followed by full-width Files and Torrents sections.

## 0.1.13 — File topology

- File is now the storage bridge between Media and Torrent ownership.
- Same physical data is derived from filesystem device+inode identity and exposed on media/torrent detail pages.
- Media pages show the storage effect of unlinking media files alone versus media plus current torrent files.
- Torrent pages show the storage effect of unlinking torrent files.
- Torrent reclaimability now uses the reconciled File topology; the old per-torrent directory-walking inspector was removed.
- Normal inventory refresh therefore reuses the latest File reconciliation snapshot and does not perform storage walks for torrent reclaimability.

## 0.2.8 — relationship and removal clarity

Proven physical backing promotes a torrent to Current, and media removal presents the complete Current/Superseded set without exposing the physical graph as the primary interface.

## 0.2.9 — scheduler rewrite

Background and mutation work now runs on a durable, domain-neutral engine with trigger/execution identity, coverage-aware coalescing, resource arbitration, and workflow-driven post-removal consistency. See `Scheduler-Spec.md` for the full contract.

## 0.2.10 — storage devices

There is no configured global storage path. Stewarr derives known storage devices from the roots each service already discovers on its own, and Home shows one usage graphic per device broken down by which service's files occupy it.
