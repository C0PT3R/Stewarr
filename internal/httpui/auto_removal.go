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
	"connarr/internal/store"
	"connarr/internal/tasks"
)

const autoRemovalTaskID = "auto-removal"
const autoRemovalEvalInterval = 15 * time.Minute

// runAutoRemovalEvaluation evaluates every known storage device's
// cross-domain Cleanup plan and submits removal requests for every Action
// whose integrations have all opted in. It is a no-op unless the global
// switch is on; both it and every per-integration opt-in default to false,
// so a fresh or upgraded install does nothing automatically.
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
	integrationByID := make(map[string]config.Integration, len(cfg.Integrations))
	for _, integration := range cfg.Integrations {
		integrationByID[integration.ID] = integration
	}
	for _, device := range server.inv.StorageDevices() {
		plan, err := cleanup.Build(device.RepresentativePath, cfg.Storage.TargetUsagePercent, cfg.Storage.CriticalUsagePercent, mediaByDevice[device.RepresentativePath], torrentsByDevice[device.RepresentativePath], reliable)
		if err != nil || !plan.Available {
			continue
		}
		for _, action := range plan.Actions {
			if !actionIntegrationsOptedIn(action, integrationByID) {
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

// actionIntegrationsOptedIn reports whether every integration an Action
// touches has explicitly allowed automatic removal. A HardlinkedBundle spans
// a Media integration and one or more Torrent integrations; all of them must
// have opted in, or the whole bundle is skipped, never partially executed.
func actionIntegrationsOptedIn(action cleanup.Action, integrationByID map[string]config.Integration) bool {
	if action.Kind != cleanup.StandaloneTorrent {
		integ, ok := integrationByID[action.Media.IntegrationID]
		if !ok || !integ.AllowAutomaticRemoval {
			return false
		}
	}
	if action.Kind != cleanup.StandaloneMedia {
		for _, t := range action.Torrents {
			integ, ok := integrationByID[t.IntegrationID]
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
		refs, _, err := server.inv.ManagedFileRefs(action.Media.Type, action.Media.SourceID, action.Media.IntegrationID)
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
		form.Set("integration_id", action.Media.IntegrationID)
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
		form.Set("integration_id", action.Torrents[0].IntegrationID)
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
