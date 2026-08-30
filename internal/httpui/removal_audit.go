package httpui

import (
	"fmt"
	"log"
	"net/url"
)

func auditRemovalPlan(operationID int64, data removalData, form url.Values) {
	mode := "live"
	if data.Plan.DryRun {
		mode = "dry_run"
	}
	log.Printf("[removal] [operation=%d] plan kind=%s key=%q label=%q mode=%s selected_actions=%d selected_path_bytes=%d planned_reclaimable_bytes=%d", operationID, data.Plan.Kind, data.Plan.RequestedKey, data.Plan.RequestedLabel, mode, data.SelectedActions, data.Plan.SelectedPathBytes, data.Plan.ReclaimableBytes)
	if data.TorrentSelected {
		log.Printf("[removal] [operation=%d] action planned owner=qbittorrent operation=remove_torrent hash=%q", operationID, data.Hash)
	}
	for _, reference := range data.SelectedManaged {
		log.Printf("[removal] [operation=%d] action planned owner=%s operation=remove_managed_file media_type=%s media_id=%d source_file_id=%d path=%q", operationID, reference.Source, reference.MediaType, reference.MediaID, reference.SourceFileID, reference.Path)
	}
	for _, relatedTorrent := range data.Related {
		if relatedTorrent.Selected {
			log.Printf("[removal] [operation=%d] action planned owner=qbittorrent operation=remove_torrent hash=%q label=%q", operationID, relatedTorrent.Torrent.Hash, relatedTorrent.Torrent.Name)
		}
	}
	for _, file := range data.Plan.Files {
		selection := "preserved"
		if file.Selected {
			selection = "selected"
		}
		log.Printf("[removal] [operation=%d] path state=%s owner=%s owner_key=%q exists=%t size_bytes=%d identity_known=%t device=%d inode=%d links=%d path=%q error=%q", operationID, selection, file.Owner, file.OwnerKey, file.Exists, file.SizeBytes, file.IdentityKnown, file.Device, file.Inode, file.Links, file.Path, file.Error)
	}
	for _, warning := range data.Plan.Warnings {
		log.Printf("[removal] [operation=%d] warning=%q", operationID, warning)
	}
	for _, option := range []struct {
		name string
		set  bool
	}{
		{"unmonitor_movies", form.Get("unmonitor_movies") == "1"},
		{"unmonitor_episodes", form.Get("unmonitor_episodes") == "1"},
		{"exclude_movies", form.Get("exclude_movies") == "1"},
		{"exclude_series", form.Get("exclude_series") == "1"},
	} {
		if option.set {
			log.Printf("[removal] [operation=%d] option selected name=%s", operationID, option.name)
		}
	}
}

func auditRemovalResult(operationID int64, result string) {
	log.Printf("[removal] [operation=%d] result=%q", operationID, result)
}

func auditRemovalError(operationID int64, operationError string) {
	log.Printf("[removal] [operation=%d] error=%q", operationID, operationError)
}

func auditRemovalComplete(operationID int64, status string, data removalData, resultCount, errorCount int) {
	log.Printf("[removal] [operation=%d] completed status=%s mode=%s results=%d errors=%d planned_reclaimable_bytes=%d", operationID, status, map[bool]string{true: "dry_run", false: "live"}[data.Plan.DryRun], resultCount, errorCount, data.Plan.ReclaimableBytes)
}

func auditRemovalRejected(operationID int64, form url.Values, reason error) {
	identity := form.Get("hash")
	if identity == "" {
		identity = fmt.Sprintf("%s:%s", form.Get("media_type"), form.Get("media_id"))
	}
	log.Printf("[removal] [operation=%d] rejected kind=%q identity=%q reason=%q", operationID, form.Get("kind"), identity, reason.Error())
}
