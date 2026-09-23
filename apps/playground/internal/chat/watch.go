package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	bridge "commander/apps/playground/internal/mcp"
)

func (e *Engine) watch(id string) {
	var cursor uint64
	var updatedAt time.Time
	first, failed := true, false
	for e.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(e.ctx, 40*time.Second)
		var err error
		if !first {
			args, _ := json.Marshal(map[string]any{"operation_id": id, "after_sequence": cursor, "wait_seconds": 25})
			var result bridge.Result
			result, err = e.commander.Call(ctx, "operation_events", args)
			if err == nil && result.IsError {
				err = fmt.Errorf("%s", result.Text)
			}
			if err == nil {
				var page struct {
					Next uint64 `json:"next_sequence"`
				}
				err = json.Unmarshal(result.Data, &page)
				if err == nil {
					cursor = page.Next
				}
			}
		}
		if err == nil {
			var operation bridge.Operation
			operation, err = e.commander.Operation(ctx, id)
			if err == nil {
				if first {
					cursor = uint64(len(operation.Events))
				}
				if first || !operation.UpdatedAt.Equal(updatedAt) || failed {
					select {
					case e.updates <- operation:
					case <-e.ctx.Done():
						cancel()
						return
					}
				}
				updatedAt = operation.UpdatedAt
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
