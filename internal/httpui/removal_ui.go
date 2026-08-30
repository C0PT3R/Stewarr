package httpui

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"connarr/internal/model"
	"connarr/internal/removal"
)

// removalSelectionModel contains only facts already inspected while the modal
// was prepared. The browser can recompute every checkbox consequence locally;
// execution still rebuilds the plan from authoritative filesystem state.
type removalSelectionModel struct {
	Kind      removal.ObjectKind     `json:"kind"`
	DryRun    bool                   `json:"dryRun"`
	MediaType model.MediaType        `json:"mediaType,omitempty"`
	Files     []removalSelectionFile `json:"files"`
	Statuses  map[string]string      `json:"torrentStatuses,omitempty"`
	Warnings  []string               `json:"warnings,omitempty"`
}

type removalSelectionFile struct {
	Path          string            `json:"path"`
	Owner         removal.FileOwner `json:"owner"`
	OwnerKey      string            `json:"ownerKey"`
	ActionName    string            `json:"actionName"`
	ActionValue   string            `json:"actionValue"`
	Always        bool              `json:"always"`
	Selected      bool              `json:"selected"`
	Exists        bool              `json:"exists"`
	SizeBytes     int64             `json:"sizeBytes"`
	IdentityKnown bool              `json:"identityKnown"`
	Device        uint64            `json:"device,omitempty"`
	Inode         uint64            `json:"inode,omitempty"`
	Links         uint64            `json:"links,omitempty"`
	PhysicalKey   string            `json:"physicalKey"`
	Selectable    bool              `json:"selectable"`
}

func removalOperationToken() (string, error) {
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate removal operation identity: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func prepareRemovalData(data *removalData) error {
	token, err := removalOperationToken()
	if err != nil {
		return err
	}
	data.OperationToken = token

	primaryUnclaimed := make(map[string]bool, len(data.UnclaimedPaths))
	for _, path := range data.UnclaimedPaths {
		primaryUnclaimed[filepath.Clean(path)] = true
	}
	statuses := make(map[string]string, len(data.Related))
	for _, relatedTorrent := range data.Related {
		statuses[strings.ToLower(relatedTorrent.Torrent.Hash)] = model.NormalizeTorrentStatus(relatedTorrent.Torrent.AssociationStatus)
	}
	selection := removalSelectionModel{
		Kind: data.Plan.Kind, DryRun: data.Plan.DryRun, MediaType: model.MediaType(data.MediaType),
		Statuses: statuses, Warnings: append([]string(nil), data.Plan.Warnings...),
	}
	for _, file := range data.Plan.Files {
		fact := removalSelectionFile{
			Path: file.Path, Owner: file.Owner, OwnerKey: file.OwnerKey,
			Selected: file.Selected, Exists: file.Exists, SizeBytes: file.SizeBytes,
			IdentityKnown: file.IdentityKnown, Device: file.Device, Inode: file.Inode, Links: file.Links,
		}
		if file.Exists && file.IdentityKnown {
			fact.PhysicalKey = fmt.Sprintf("%d:%d", file.Device, file.Inode)
		} else {
			fact.PhysicalKey = "path:" + filepath.Clean(file.Path)
		}
		switch file.Owner {
		case removal.MediaOwner:
			fact.ActionName, fact.ActionValue = "managed_file", file.OwnerKey
			fact.Selectable = file.Selectable
		case removal.TorrentOwner:
			if data.Plan.Kind == removal.TorrentObject && strings.EqualFold(file.OwnerKey, data.Hash) {
				fact.ActionName, fact.ActionValue = "target", "1"
				fact.Selectable = file.Selectable
			} else {
				fact.ActionName, fact.ActionValue = "torrent", file.OwnerKey
				fact.Selectable = file.Selectable
			}
		case removal.UnclaimedOwner:
			if data.Plan.Kind == removal.UnclaimedObject && primaryUnclaimed[filepath.Clean(file.Path)] {
				fact.ActionName, fact.ActionValue, fact.Always = "path", file.Path, true
			} else {
				fact.ActionName, fact.ActionValue = "unclaimed_path", file.Path
			}
		}
		selection.Files = append(selection.Files, fact)
	}
	encoded, err := json.Marshal(selection)
	if err != nil {
		return fmt.Errorf("encode removal selection facts: %w", err)
	}
	data.SelectionModel = base64.StdEncoding.EncodeToString(encoded)
	return nil
}

func (server *Server) renderRemoval(response http.ResponseWriter, data removalData) {
	if err := prepareRemovalData(&data); err != nil {
		http.Error(response, "Removal plan could not be prepared: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := renderTemplate(response, server.removalTpl, data); err != nil {
		// renderTemplate already returned an HTTP error.
		return
	}
}
