package httpui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"connarr/internal/inventory"
	"connarr/internal/store"
	"connarr/internal/tasks"
)

const removalTaskID = "removal-operation"

type scheduledRemovalCommand struct {
	HistoryID int64      `json:"historyId"`
	Form      url.Values `json:"form"`
}

type capturedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

type queuedRemovalPayload struct {
	Command scheduledRemovalCommand `json:"command"`
}

func (server *Server) recoverScheduledRemovals(database *store.Store) error {
	events, err := database.InFlightRemovalHistoryEvents()
	if err != nil {
		return fmt.Errorf("read in-flight removal journal: %w", err)
	}
	restoreConsistency := false
	for _, event := range events {
		if event.Status == "started" {
			if !event.DryRun {
				restoreConsistency = true
			}
			continue
		}
		key := fmt.Sprintf("operation:%d", event.ID)
		if trigger, exists := server.tasks.LatestTriggerForKey(removalTaskID, key); exists {
			switch trigger.State {
			case "pending", "waiting", "running", "attached":
				continue
			}
			event.Status = "interrupted"
			event.Error = "the scheduler cannot prove that the journaled removal is safe to replay; verification is required"
			if err := database.UpdateHistoryEvent(event); err != nil {
				return fmt.Errorf("mark removal operation %d for attention: %w", event.ID, err)
			}
			if !event.DryRun {
				restoreConsistency = true
			}
			continue
		}
		var queued queuedRemovalPayload
		if err := json.Unmarshal(event.Payload, &queued); err != nil || len(queued.Command.Form) == 0 {
			event.Status = "failed"
			event.Error = "queued removal journal is incomplete and cannot be scheduled"
			if updateErr := database.UpdateHistoryEvent(event); updateErr != nil {
				return fmt.Errorf("reject incomplete removal operation %d: %w", event.ID, updateErr)
			}
			continue
		}
		queued.Command.HistoryID = event.ID
		payload, err := json.Marshal(queued.Command)
		if err != nil {
			return fmt.Errorf("encode recovered removal operation %d: %w", event.ID, err)
		}
		if _, err := server.tasks.Submit(tasks.Request{TaskID: removalTaskID, Kind: tasks.TriggerEvent, Priority: tasks.PriorityMutation, Durable: true, CoalescingKey: key, Cause: "Recovered queued removal: " + event.RequestedLabel, Payload: payload}); err != nil {
			return fmt.Errorf("resubmit queued removal operation %d: %w", event.ID, err)
		}
	}
	if err := database.InterruptStartedHistoryEvents(); err != nil {
		return fmt.Errorf("recover interrupted removals: %w", err)
	}
	if restoreConsistency {
		if err := server.inv.QueueReconciliation(inventory.ReconciliationScope{Full: true, Reasons: []string{"recovery after interrupted removal"}}); err != nil {
			return fmt.Errorf("record full consistency recovery scope: %w", err)
		}
		if _, err := server.tasks.AdvanceWorkflow("post-removal-consistency", "global", 0, 5*time.Minute, "Recovery after an interrupted removal"); err != nil {
			return fmt.Errorf("schedule consistency recovery: %w", err)
		}
	}
	return nil
}

func (response *capturedResponse) Header() http.Header {
	if response.header == nil {
		response.header = make(http.Header)
	}
	return response.header
}

func (response *capturedResponse) WriteHeader(status int) { response.status = status }

func (response *capturedResponse) Write(content []byte) (int, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return response.body.Write(content)
}

func (server *Server) runScheduledRemoval(schedulerContext context.Context, payload json.RawMessage) error {
	var command scheduledRemovalCommand
	if err := json.Unmarshal(payload, &command); err != nil {
		return fmt.Errorf("decode removal operation: %w", err)
	}
	if command.HistoryID <= 0 {
		return fmt.Errorf("removal operation has no durable history identity")
	}
	form := cloneForm(command.Form)
	form.Set("_history_id", strconv.FormatInt(command.HistoryID, 10))
	server.publishUIChange("operation-started")
	request := &http.Request{Method: http.MethodPost, Form: form, Header: make(http.Header)}
	request.Header.Set("X-Connarr-Overlay", "1")
	operationContext, cancel := context.WithTimeout(schedulerContext, 30*time.Minute)
	defer cancel()
	response := &capturedResponse{}
	server.executeRemovalNowContext(response, request, operationContext)
	if response.status >= http.StatusBadRequest {
		operationError := fmt.Sprintf("removal operation %d: %s", command.HistoryID, bytes.TrimSpace(response.body.Bytes()))
		auditRemovalRejected(command.HistoryID, command.Form, fmt.Errorf("%s", bytes.TrimSpace(response.body.Bytes())))
		database := server.inv.Store()
		if event, historyError := database.HistoryEventByID(command.HistoryID); historyError == nil {
			event.Status = "failed"
			event.Error = operationError
			_ = database.UpdateHistoryEvent(event)
		}
		server.publishUIChange("removal-failed")
		return fmt.Errorf("%s", operationError)
	}
	event, err := server.inv.Store().HistoryEventByID(command.HistoryID)
	if err != nil {
		return err
	}
	if event.Status == "failed" {
		server.publishUIChange("removal-failed")
		return fmt.Errorf("removal operation %d failed: %s", command.HistoryID, event.Error)
	}
	if event.Status == "partial" || event.Status == "interrupted" || event.Status == "attention" {
		server.publishUIChange("removal-failed")
	} else {
		server.publishUIChange("removal-completed")
	}
	return nil
}
