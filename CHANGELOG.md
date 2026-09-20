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

## 0.2.11-0.2.12 — cross-domain cleanup planner, protection, automatic execution

A unified planner ranks Media and Torrent candidates together per storage device instead of two disconnected lists, respecting Protected/keep-tag state and each service's own automatic-removal opt-in. Season-level Retention Value lets a Series be reclaimed one season at a time instead of only as a whole.

## 0.2.15-0.2.18 — TypeScript rewrite, server split, Unmanaged renamed

Client-side JS was rewritten in TypeScript, `server.go` was broken apart into per-page files, several new pages and Home page changes landed, "unclaimed" was renamed to "Unmanaged" throughout, and the scheduler received a round of small fixes.

## 0.2.20-0.2.23 — live-editable services, multi-instance support

Services can be added, edited, and removed live without restarting, with storage-path discovery triggered immediately on any such change instead of waiting for the next scheduled refresh. Multiple Radarr/Sonarr instances are supported simultaneously. Fixed the "Updates available" button staying visible after navigating away.

## 0.2.28 — fresh-install empty states

File reconciliation no longer fails when zero services are configured (a legitimate starting state, not a misconfiguration). The Home page's Services/Library cards render sensible empty states instead of disappearing or showing both media types at zero, and the Add/Edit service forms only show the credential fields a given service type actually uses.

## 0.2.29 — app-wide slowness root-caused and fixed

Every page load needing cached state (nearly all of them) could stall for the full duration of a routine reconciliation's database write, since reconciliation held the same lock readers needed for its entire write, not just the brief in-memory commit around it. Root-caused via a captured goroutine dump after an earlier lock-release attempt didn't fix it, then fixed with a dedicated publish-serialization lock that a reader is never blocked behind.

## 0.2.31 — leaked EventSource reconnect loop

Found the actual cause of a separately reported slow-page issue: the browser's live-updates connection could silently die without firing an error event, leaving it retrying to reconnect forever in the background — and every such leaked retry still consumed one of the browser's ~6 connections-per-origin, eventually exhausting the pool for the whole origin on a tab left open for hours. Fixed by giving every live-updates connection a bounded server-side lifetime and closing/clearing a client-side connection explicitly before any reconnect attempt.

## 0.2.32-0.2.35 — connection-pool hardening, per-device thresholds move

Closed a second, independent connection-exhaustion path (fast page-to-page navigation piling a new page's connections on top of an old page's not-yet-closed one) by force-closing on `pagehide` and merging the JS bundle down to one script tag. Reclamation thresholds moved off the add-service form onto the Storage page itself, since they only make sense to set after a device is actually discovered — fixed alongside a real bug where the new handler silently rejected every save because it read the wrong form encoding.

## 0.2.34-0.2.35 — services rename, per-device thresholds, staged setup

"Integration" renamed to "Service" everywhere — Go identifiers, JSON fields, routes, templates, docs, the persisted DB column. Reclamation thresholds became a true per-device setting instead of one global percentage. The service setup overlay became a two-step form: a real connection test reveals the root-path and threshold fields only once it passes, then an automatic third step shows live reconciliation progress.

## 0.2.36-0.2.40 — Library filters, empty states, qBittorrent login compatibility

Library gained dynamic Type/Source filters (derived from what's actually present, not a hardcoded Movie/Series pair) and category-aware empty states ("No media library registered" / "No torrent client registered") with a scoped Add-service button. The Torrent filter hides itself when no torrent client is configured. qBittorrent login now accepts any 2xx response except the one documented failure body, covering deployments that respond 204 or bypass authentication for whitelisted IPs — neither of which the original "must be exactly 200 'Ok.'" check allowed.

## 0.2.41-0.2.44 — overlay polish, hardlink disclosure, season-crash fix

Overlay CSS/DOM naming generalized from removal-specific to shared (`modal-*`), since the same overlay backs service setup and removal alike. Fixed the Storage page crashing on a season-level cleanup action (no `.Torrents` to index into). Removing a torrent hardlinked to a library file now discloses that relationship and warns that removing the torrent alone won't reclaim the space, instead of silently showing nothing.

## 0.2.45-0.2.48 — Unmanaged file removal re-enabled

Standalone Unmanaged file removal (direct filesystem deletion, since nothing claims these files) was disabled during an earlier refactor and re-enabled here with real checkboxes, select-all, and a per-file trash icon — closing several gaps found in the process: the plan builder had no case for Unmanaged targets at all, the GET overlay handler was a hardcoded 403 stub, and grouping hardlinked paths incorrectly could both manufacture a false "missing hardlink" warning and silently drop the primary target from what actually got submitted.

## 0.3.0-0.3.1 — single-admin authentication

Stewarr had no identity check at all before this, only a same-origin CSRF guard. Adds a login gate in front of every route: bcrypt-hashed credentials, server-side sessions, a first-run setup screen, and a password-change flow that revokes every other session on rotation. A post-release review found and fixed a login-timing side channel, added lockout, and stopped silently failing open when no durable store is configured.

## 0.3.2 — automatic removal, end to end

The first working cut of automatic removal: a scheduled evaluation task, a dry-run gate, per-service opt-in enforced down through the planner (including vetoing a whole hardlinked bundle if either side's service hasn't opted in), and Settings/service-form UI to configure all of it — closing out what had been config-file-only since 0.2.12.

## 0.3.3 — Orphaned torrent status

Reconciliation used to collapse two different cases into Unassociated: no import history at all, versus import history with no proof of a specific replacement. The second case is now its own **Orphaned** status, distinct from both Superseded and Unassociated. Media removal can now also select any related Orphaned/Superseded torrent for cleanup in the same action, not just the one proven still hardlinked.

## 0.3.4 — season removal order tracks air date

Season Retention Value's recency factor now comes from Sonarr's own air-date data instead of import date — a show backfilled all at once used to give every season nearly the same import timestamp, letting unrelated noise decide removal order. A hard guarantee on top ensures an earlier season is never removed after a later one even if computed values ever tie.

## 0.3.5 — TMDB rating/popularity enrichment

Radarr/Sonarr never provided a real popularity signal; TMDB itself is now fetched directly as a more reliable source for both movies and series, covering newly discovered media immediately rather than waiting on a daily refresh cycle, fully opt-in through Settings.

## 0.3.6-0.3.7 — automatic removal reliability

A restart used to unconditionally mark every enrichment source stale, pausing automatic-removal planning for up to a full day even when the just-restored cache was already good. Also fixed editing a service silently resetting its automatic-removal opt-in back off on any unrelated change.

## 0.4.0-0.4.3 — three-state automatic removal, per-device settings overlay

Replaces the plain on/off automatic-removal switch with Disabled/Confirm/Auto — Confirm runs the full evaluation and every filter but never submits, leaving pre-vetted candidates for manual review. Services not opted into automatic removal are now excluded from the manual candidate list too, not just automatic execution. Per-device settings (Target/Critical %, later Name) moved into a dedicated wrench-icon overlay instead of an inline Storage-page form.

## 0.4.4-0.4.6 — user-nameable, stable-id storage devices

Each device gets an editable Name and a stable numeric id with a default "Device #N" name, assigned and pruned once per full file reconciliation so ids get reused rather than only climbing. Fixed TMDB enrichment discarding an entire in-progress pass (including already-refetched items) whenever the base catalog ticked mid-run, which happens routinely — nothing else re-fetched an already-enriched item, so this let popularity/rating data go stale indefinitely despite running on schedule.

## 0.4.7-0.4.8 — atomic enrichment at discovery, Stewarr rename

A newly discovered or re-identified item now fetches Jellyfin/Seerr/TMDB facts atomically right when the base refresh completes, instead of waiting on each source's own periodic cycle. Renamed the project from Connarr to Stewarr throughout (module path, binary, identifiers, docs) ahead of making the repository public, and added an MIT license.

## 0.5.0 — client-agnostic Torrent Value, usable-space storage accounting, fairness

Torrent Value dropped every qBittorrent-specific signal (tracker health, live upload/download speed — a torrent client's connectivity status and a live speed reading are instantaneous facts about the exact moment a check runs, not lasting properties of a torrent) in favor of client-agnostic ones derivable from any adapter: realized upload bytes over a trailing window, consistency of contribution, and a capped seeding-time bonus, alongside the existing ratio/recency/private-tracker signals.

Storage accounting now reasons in terms of *usable* space (total minus whatever's held by things outside Stewarr's own view) rather than raw disk capacity, with the filesystem's own reserved-for-root blocks shown as their own distinct, honest line item instead of folded into an ambiguous "Other" bucket. Reclamation thresholds are evaluated against that usable capacity, so space another service already holds on a shared pool is never something Stewarr's own target/critical silently has to compete against. Critical is no longer shown anywhere in the UI (it never gated cleanup planning to begin with).

Automatic removal gained a per-device enable/disable toggle (on top of the existing per-service one), settings to unmonitor/exclude-from-import-lists on automatic removal (previously only available manually), and a setting to allow removing incomplete torrents once inactive. When a device's overage has to come from more than one media service sharing it (e.g. Radarr and Sonarr), each service's share is now proportional to its own footprint rather than whichever items score lowest overall absorbing nearly all of it — series structurally scoring higher than movies in Retention Value no longer means movies take the brunt of every shared device's cleanup.

Fixed a real bug where a torrent's still-downloading files (in a separate incomplete-downloads directory) were reconstructed against its final destination path instead of where the content actually currently lives, silently misclassifying live download data as Unmanaged and blinding the pre-deletion ownership safety check to it. The History page stops auto-refreshing on unrelated background activity and now records which service a removed item belonged to, and Unmanaged gained reclaimable-status/root/minimum-size filters.
