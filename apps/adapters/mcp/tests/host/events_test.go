package mcp_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPEventWaitRetainsInterruptedWorkAndTimeoutCursor(t *testing.T) {
	store, err := operationfs.NewOperationFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := operations.NewTask(id.OperationID(), "", "", actions.NextAction{ActionName: "review"})
	task.AgentID, task.Status, task.StartedAt = "fixture-agent-12345678", operations.TaskInterrupted, now
	op := operations.Operation{ID: task.OperationID, Status: operations.Cancelled, Tasks: []operations.Task{task}, CreatedAt: now, UpdatedAt: now}
	op.AppendEvent(operations.OperationCancelled, "", "", "cancelled while execution outcome is unknown")
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	observed := fakes.NewEventWaitStore(store)
	ctx, session := hostfixtures.Session(t, adapter.Config{OperationStore: observed})
	cursor := strconv.FormatUint(op.Events[len(op.Events)-1].Sequence, 10)
	arguments := map[string]any{"operation_id": op.ID, "cursor": cursor, "wait_seconds": 5}
	type response struct {
		result *sdkmcp.CallToolResult
		err    error
	}
	done := make(chan response, 1)
	finished := make(chan struct{})
	waitCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("event tool call did not stop")
		}
	}()
	go func() {
		defer close(finished)
		result, err := session.CallTool(waitCtx, &sdkmcp.CallToolParams{Name: "operation_events", Arguments: arguments})
		done <- response{result, err}
	}()
	var result response
	select {
	case <-observed.Waiting():
		// A second snapshot read proves the terminal operation remained in the
		// polling loop. Only then deliver its late execution outcome.
	case result = <-done:
		t.Fatalf("event wait ended before the interrupted execution settled: %+v", result)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := store.FinishTask(ctx, op.ID, operations.TaskResult{TaskID: task.ID, AgentID: task.AgentID,
		Status: operations.TaskFailed, Error: "execution failed", FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	decode := func(result *sdkmcp.CallToolResult, err error) adapter.OperationEventsResult {
		t.Helper()
		if err != nil || result == nil || result.IsError || len(result.Content) != 1 {
			t.Fatalf("operation_events failed: result=%+v err=%v", result, err)
		}
		content, ok := result.Content[0].(*sdkmcp.TextContent)
		if !ok {
			t.Fatal("operation_events did not return text JSON")
		}
		var page adapter.OperationEventsResult
		if err := json.Unmarshal([]byte(content.Text), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := decode(result.result, result.err)
	if len(page.Events) == 0 || page.Events[0].Type != operations.TaskFinished || page.NextCursor == cursor {
		t.Fatalf("event wait lost late task outcome: %+v", page)
	}

	// Keep another saved execution unresolved so this request ends by its own
	// deadline, not because the terminal operation is already settled.
	op.ID = id.OperationID()
	op.Tasks[0].OperationID = op.ID
	op.Events = nil
	op.AppendEvent(operations.OperationCancelled, "", "", "still waiting for execution outcome")
	if err := store.Create(ctx, op); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result.result, result.err = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "operation_events",
		Arguments: map[string]any{"operation_id": op.ID, "cursor": "1", "wait_seconds": 1}})
	page = decode(result.result, result.err)
	if len(page.Events) != 0 || page.Events == nil || page.NextCursor != "1" || time.Since(started) < time.Second {
		t.Fatalf("bounded wait lost its deadline or empty-page cursor: %+v", page)
	}
}
