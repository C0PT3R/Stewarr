package httpui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connarr/internal/cleanup"
	"connarr/internal/config"
	"connarr/internal/model"
	"connarr/internal/store"
	"connarr/internal/tasks"
)

const autoRemovalTaskID = "auto-removal"
const autoRemovalEvalInterval = 15 * time.Minute

// runAutoRemovalEvaluation evaluates every known storage device's
// cross-domain Cleanup plan and submits removal requests for every Action
// whose services have all opted in. It is a no-op unless the global
// switch is on; both it and every per-service opt-in default to false,
// so a fresh or upgraded install does nothing automatically. Unassociated
// torrents are additionally excluded by default regardless of opt-in,
// since Connarr has no owning-media relationship to judge them safe to
// delete unattended — see actionIsUnassociatedTorrent.
func (server *Server) runAutoRemovalEvaluation(ctx context.Context) error {
	if server.inv == nil || server.tasks == nil {
		return nil
	}
	cfg := server.inv.Config()
	if !cfg.Removal.AutoEnabled {
		return nil
	}
	items, _, err := server.inv.Snapshot()
	if err != nil {
		log.Printf("[auto-removal] skipped this cycle: snapshot unavailable: %v", err)
		return nil
	}
	torrents := server.inv.TorrentSnapshot()
	reliable := server.planningReliable(server.inv.ReliabilitySnapshot())
	mediaByDevice := server.inv.MediaByDevice(items)
	torrentsByDevice := server.inv.TorrentsByDevice(torrents)
	serviceByID := make(map[string]config.Service, len(cfg.Services))
	for _, service := range cfg.Services {
		serviceByID[service.ID] = service
	}
	for _, device := range server.inv.StorageDevices() {
		target, critical := cfg.ThresholdsFor(device.RepresentativePath)
		plan, err := cleanup.Build(device.RepresentativePath, target, critical, mediaByDevice[device.RepresentativePath], torrentsByDevice[device.RepresentativePath], reliable)
		if err != nil || !plan.Available {
			continue
		}
		for _, action := range plan.Actions {
			if actionIsUnassociatedTorrent(action) && !cfg.Removal.AutoRemoveUnassociatedTorrents {
				continue
			}
			if !actionServicesOptedIn(action, serviceByID) {
				continue
			}
			form, err := server.formForAction(action)
			if err != nil {
				log.Printf("[auto-removal] skipped action kind=%s: %v", action.Kind, err)
				continue
			}
			if err := server.submitAutoRemoval(form, action); err != nil {
				log.Printf("[auto-removal] submit failed kind=%s: %v", action.Kind, err)
			}
		}
	}
	return nil
}

// actionIsUnassociatedTorrent reports whether action is a StandaloneTorrent
// candidate with no owning-media relationship, current or historical — as
// opposed to one Connarr can prove is Superseded, a Current but independent
// (non-hardlinked) copy of managed media, or simply Unassociated today but
// with a FormerMediaItems relationship on record (it was managed once; that
// is provenance Superseded torrents also rely on, not the unknown case this
// guards). The unknown case this excludes by default is a torrent Connarr
// has never had any relationship for at all — it may simply be something
// the user downloaded through that client for their own purposes, or from a
// service Connarr doesn't track; automatic removal has no basis to judge
// those safe to delete unattended.
func actionIsUnassociatedTorrent(action cleanup.Action) bool {
	if action.Kind != cleanup.StandaloneTorrent || len(action.Torrents) == 0 {
		return false
	}
	torrent := action.Torrents[0]
	if len(torrent.FormerMediaItems) > 0 {
		return false
	}
	return model.NormalizeTorrentStatus(torrent.AssociationStatus) == model.TorrentUnassociated
}

// actionServicesOptedIn reports whether every service an Action
// touches has explicitly allowed automatic removal. A HardlinkedBundle spans
// a Media service and one or more Torrent services; all of them must
// have opted in, or the whole bundle is skipped, never partially executed.
func actionServicesOptedIn(action cleanup.Action, serviceByID map[string]config.Service) bool {
	if action.Kind != cleanup.StandaloneTorrent {
		integ, ok := serviceByID[action.Media.ServiceID]
		if !ok || !integ.AllowAutomaticRemoval {
			return false
		}
	}
	if action.Kind != cleanup.StandaloneMedia {
		for _, t := range action.Torrents {
			integ, ok := serviceByID[t.ServiceID]
			if !ok || !integ.AllowAutomaticRemoval {
				return false
			}
		}
	}
	return true
}

// formForAction builds the exact url.Values shape a browser's manual removal
// confirmation would submit for the given Action, reusing the same form
// vocabulary admitRemoval/executeRemovalNowContext already expect. No new
// endpoint or field names are introduced.
func (server *Server) formForAction(action cleanup.Action) (url.Values, error) {
	form := url.Values{}
	switch action.Kind {
	case cleanup.StandaloneMedia, cleanup.StandaloneSeason, cleanup.HardlinkedBundle:
		refs, _, err := server.inv.ManagedFileRefs(action.Media.Type, action.Media.SourceID, action.Media.ServiceID)
		if err != nil {
			return nil, err
		}
		if action.Season != nil {
			scoped := refs[:0]
			for _, ref := range refs {
				if len(ref.Parts) > 0 && ref.Parts[0].Group == action.Season.FileGroup {
					scoped = append(scoped, ref)
				}
			}
			refs = scoped
		}
		if len(refs) == 0 {
			return nil, fmt.Errorf("media %s:%d has no managed files to select for this action", action.Media.Type, action.Media.SourceID)
		}
		form.Set("kind", "media")
		form.Set("media_type", string(action.Media.Type))
		form.Set("media_id", strconv.Itoa(action.Media.SourceID))
		form.Set("service_id", action.Media.ServiceID)
		for _, ref := range refs {
			form.Add("managed_file", managedFileKey(ref))
		}
		for _, t := range action.Torrents {
			form.Add("torrent", strings.ToLower(t.Hash))
		}
	case cleanup.StandaloneTorrent:
		if len(action.Torrents) == 0 {
			return nil, fmt.Errorf("torrent action has no torrent")
		}
		form.Set("kind", "torrent")
		form.Set("hash", strings.ToLower(action.Torrents[0].Hash))
		form.Set("service_id", action.Torrents[0].ServiceID)
		form.Set("target", "1")
	default:
		return nil, fmt.Errorf("unsupported action kind %q", action.Kind)
	}
	return form, nil
}

// submitAutoRemoval journals and schedules an automatically-decided removal
// through the exact same admission/durable-history/scheduler path a manual
// browser submission uses (executeRemoval) — no new execution code, so it
// inherits every existing safety mechanism, including live final-boundary
// revalidation inside runScheduledRemoval and respecting removal.dry_run
// (read live from config at both admission and execution, never from the
// form).
func (server *Server) submitAutoRemoval(form url.Values, action cleanup.Action) error {
	descriptor, err := server.admitRemoval(form)
	if err != nil {
		return fmt.Errorf("admission rejected (the action may have gone stale since this plan was computed): %w", err)
	}
	database := server.inv.Store()
	if database == nil {
		return fmt.Errorf("durable operation history unavailable")
	}
	command := scheduledRemovalCommand{Form: cloneForm(form)}
	queuedPayload, err := json.Marshal(queuedRemovalPayload{Command: command})
	if err != nil {
		return err
	}
	historyID, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "queued", DryRun: descriptor.DryRun, RequestedKind: string(descriptor.Kind), RequestedKey: descriptor.Key, RequestedLabel: descriptor.Label, Payload: queuedPayload})
	if err != nil {
		return fmt.Errorf("record operation before scheduling: %w", err)
	}
	command.HistoryID = historyID
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	_, err = server.tasks.Submit(tasks.Request{TaskID: removalTaskID, Kind: tasks.TriggerEvent, Priority: tasks.PriorityMutation, Durable: true, CoalescingKey: fmt.Sprintf("operation:%d", historyID), Cause: "Automatic cleanup: " + descriptor.Label, Payload: payload})
	return err
}
