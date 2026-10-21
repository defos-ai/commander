package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

func (e *Engine) watch(id string) {
	var cursor string
	var updatedAt time.Time
	var progress bridge.Progress
	var missing []secrets.Key
	first, failed := true, false
	for e.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(e.ctx, 40*time.Second)
		// Agent-reported waits can change without a saved operation event.
		waitSeconds := 2
		if first {
			waitSeconds = 0
		}
		args, _ := json.Marshal(map[string]any{"operation_id": id, "cursor": cursor, "wait_seconds": waitSeconds})
		result, err := e.commander.Call(ctx, "operation_events", args)
		if err == nil && result.IsError {
			err = fmt.Errorf("%s", result.Text)
		}
		if err == nil {
			var page struct {
				Next string `json:"next_cursor"`
			}
			err = json.Unmarshal(result.Data, &page)
			if err == nil {
				cursor = page.Next
			}
		}
		if err == nil {
			var operation bridge.Operation
			operation, err = e.commander.Operation(ctx, id)
			if err == nil {
				currentMissing := operation.MissingSecrets()
				if first || !operation.UpdatedAt.Equal(updatedAt) || !sameProgress(operation.Progress, progress) || !slices.Equal(currentMissing, missing) || failed {
					select {
					case e.updates <- operation:
					case <-e.ctx.Done():
						cancel()
						return
					}
				}
				updatedAt = operation.UpdatedAt
				progress = operation.Progress
				missing = currentMissing
				first = false
				if failed {
					e.emit(Event{Kind: "success", Text: "Operation updates resumed: " + id})
				}
				failed = false
				if operation.Settled() {
					cancel()
					return
				}
			}
		}
		cancel()
		if err != nil {
			if !failed {
				e.emit(Event{Kind: "error", Text: "Operation updates unavailable for " + id + "; retrying in the background."})
			}
			failed = true
		}
		// Also avoids a busy loop against servers that return an empty page early.
		delay := 250 * time.Millisecond
		if failed {
			delay = 3 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-e.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Derived progress can change when a question expires without a saved update.
func sameProgress(a, b bridge.Progress) bool {
	return a.TotalTasks == b.TotalTasks && a.FinishedTasks == b.FinishedTasks &&
		a.PendingDecisions == b.PendingDecisions && a.PendingQuestions == b.PendingQuestions &&
		a.Settled == b.Settled && maps.Equal(a.TaskCounts, b.TaskCounts)
}
