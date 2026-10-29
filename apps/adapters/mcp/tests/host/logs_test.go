package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	fixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPLogToolAdvertisesConsolidatedSchema(t *testing.T) {
	stores := fixtures.NewLogStores(t)
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: stores.Operations, ExecutionStore: stores.Executions})

	listing, err := session.ListTools(ctx, nil)

	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	found := false
	for _, tool := range listing.Tools {
		if tool.Name == "task_logs" {
			t.Fatal("removed task log tool is still advertised")
		}
		if tool.Name == "operation_logs" {
			encoded, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &schema); err != nil {
				t.Fatal(err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("saved log query is unavailable without a controller")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]any{"operation_id": stores.Operation.ID}
	if err := resolved.Validate(valid); err != nil {
		t.Fatalf("schema requires fields besides operation_id: %v", err)
	}
	for _, order := range []string{"asc", "desc"} {
		filters := map[string]any{"operation_id": stores.Operation.ID, "task_id": stores.Operation.Tasks[0].ID,
			"agent_id": "builder-agent-12345678", "action_name": "build-agent", "order": order, "limit": 1000, "cursor": ""}
		if err := resolved.Validate(filters); err != nil {
			t.Fatalf("schema rejects supported order and filters: %v", err)
		}
	}
	defaults := maps.Clone(valid)
	defaults["cursor"], defaults["limit"] = nil, nil
	if err := resolved.Validate(defaults); err != nil {
		t.Fatalf("schema rejects nullable pagination defaults: %v", err)
	}

	page := callLogTool(t, ctx, session, "operation_logs", defaults)

	assertLogEntries(t, page)
	if page.NextCursor != "" {
		t.Fatal("null pagination defaults changed the initial cursor")
	}
	for _, invalid := range []map[string]any{
		{}, {"task_id": stores.Operation.Tasks[0].ID},
		{"operation_id": stores.Operation.ID, "limit": 0}, {"operation_id": stores.Operation.ID, "limit": 1001},
		{"operation_id": stores.Operation.ID, "order": nil}, {"operation_id": stores.Operation.ID, "order": ""},
		{"operation_id": stores.Operation.ID, "order": true}, {"operation_id": stores.Operation.ID, "order": "ASC"},
		{"operation_id": stores.Operation.ID, "task_id": nil}, {"operation_id": stores.Operation.ID, "task_id": ""},
		{"operation_id": stores.Operation.ID, "latest": true}, {"operation_id": stores.Operation.ID, "wait_seconds": 1},
		{"operation_id": stores.Operation.ID, "unknown": true}, {"operation_id": stores.Operation.ID, "action_name": " "},
	} {
		if err := resolved.Validate(invalid); err == nil {
			t.Errorf("schema accepted unsupported input: %+v", invalid)
		}
	}
}

func TestMCPLogToolPaginatesBothOrdersWithOptionalTaskFilter(t *testing.T) {
	for _, taskFilter := range []bool{false, true} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(fmt.Sprintf("task=%v/order=%s", taskFilter, order), func(t *testing.T) {
				stores := fixtures.NewLogStores(t)
				op, task := stores.Operation.ID, stores.Operation.Tasks[0].ID
				logs := make([]executions.Log, 13)
				for index := range logs {
					log := fixtures.ExecutionLog(op, task, len(logs)-index)
					log.CreatedAt = log.CreatedAt.Add(-time.Duration(index) * time.Hour)
					switch index + 1 {
					case 2, 13:
						log.Work.TaskID = stores.Operation.Tasks[1].ID
					case 4, 10:
						log.AgentID = "builder-agent-87654321"
					case 6, 11:
						log.ActionName = "publish-agent"
					case 8, 12:
						log.Work.OperationID, log.Work.TaskID = stores.Other.ID, stores.Other.Tasks[0].ID
					}
					logs[index] = log
					if err := stores.Executions.AppendLog(t.Context(), log); err != nil {
						t.Fatal(err)
					}
				}
				ctx, session := fixtures.Session(t, adapter.Config{OperationStore: stores.Operations, ExecutionStore: stores.Executions})
				args := map[string]any{"operation_id": op, "agent_id": logs[0].AgentID, "action_name": logs[0].ActionName, "order": order, "limit": 2}
				expected := []executions.Log{logs[0], logs[1], logs[2], logs[4], logs[6], logs[8], logs[12]}
				if taskFilter {
					args["task_id"] = task
					expected = []executions.Log{logs[0], logs[2], logs[4], logs[6], logs[8]}
				}
				if order == "desc" {
					slices.Reverse(expected)
				}
				var newest string
				for offset := 0; offset < len(expected); offset += 2 {

					page := callLogTool(t, ctx, session, "operation_logs", args)

					want := expected[offset:min(offset+2, len(expected))]
					assertLogEntries(t, page, want...)
					if page.NextCursor != want[len(want)-1].ID {
						t.Fatal("page cursor did not identify the last entry in selected order")
					}
					if order == "desc" && offset == 0 {
						newest = page.Logs[0].ID
					} else if order == "asc" {
						newest = page.NextCursor
					}
					args["cursor"] = page.NextCursor
				}

				tail := callLogTool(t, ctx, session, "operation_logs", args)

				assertLogEntries(t, tail)
				if tail.NextCursor != args["cursor"] {
					t.Fatal("empty page changed its directional cursor")
				}
				late := fixtures.ExecutionLog(op, task, 99)
				late.CreatedAt = late.CreatedAt.Add(-24 * time.Hour)
				if err := stores.Executions.AppendLog(ctx, late); err != nil {
					t.Fatal(err)
				}
				if order == "desc" {

					older := callLogTool(t, ctx, session, "operation_logs", args)

					assertLogEntries(t, older)
					if older.NextCursor != tail.NextCursor {
						t.Fatal("descending history cursor moved toward a newer append")
					}
				}
				delete(args, "order")
				args["cursor"] = newest

				continued := callLogTool(t, ctx, session, "operation_logs", args)

				assertLogEntries(t, continued, late)
				if continued.NextCursor != late.ID {
					t.Fatal("ascending follow from the newest observed ID missed the late append")
				}
			})
		}
	}
}

func TestMCPLogToolsPreserveProvenancePagingAndExactFilters(t *testing.T) {
	stores := fixtures.NewLogStores(t)
	op, task := stores.Operation.ID, stores.Operation.Tasks[0].ID
	logs := make([]executions.Log, 0, 102)
	for index := 1; index <= 101; index++ {
		logs = append(logs, fixtures.ExecutionLog(op, task, index))
	}
	otherTask := fixtures.ExecutionLog(op, stores.Operation.Tasks[1].ID, 102)
	otherTask.AgentID, otherTask.ActionName = "builder-agent-87654321", "publish-agent"
	logs = append(logs, otherTask)
	for _, log := range append(logs, fixtures.ExecutionLog(stores.Other.ID, stores.Other.Tasks[0].ID, 103)) {
		if err := stores.Executions.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: stores.Operations, ExecutionStore: stores.Executions})

	first := callLogTool(t, ctx, session, "operation_logs", map[string]any{"operation_id": op})

	assertLogEntries(t, first, logs[:100]...)
	if first.NextCursor != logs[99].ID {
		t.Fatal("default page did not retain its last log ID")
	}

	second := callLogTool(t, ctx, session, "operation_logs", map[string]any{"operation_id": op, "cursor": first.NextCursor, "limit": 10})

	assertLogEntries(t, second, logs[100:]...)
	if second.NextCursor != otherTask.ID {
		t.Fatal("second page did not reach its operation tail")
	}

	tail := callLogTool(t, ctx, session, "operation_logs", map[string]any{"operation_id": op, "cursor": second.NextCursor})

	assertLogEntries(t, tail)
	if tail.NextCursor != second.NextCursor {
		t.Fatal("empty page changed its cursor")
	}

	for _, test := range []struct {
		args map[string]any
		want []executions.Log
	}{
		{map[string]any{"operation_id": op, "task_id": task, "limit": 1000}, logs[:101]},
		{map[string]any{"operation_id": op, "agent_id": otherTask.AgentID, "action_name": otherTask.ActionName}, logs[101:]},
		{map[string]any{"operation_id": op, "task_id": task, "agent_id": otherTask.AgentID}, nil},
		{map[string]any{"operation_id": op, "action_name": "Build-agent"}, nil},
		{map[string]any{"operation_id": op, "action_name": " build-agent "}, nil},
	} {
		page := callLogTool(t, ctx, session, "operation_logs", test.args)
		assertLogEntries(t, page, test.want...)
	}
}

func TestMCPLogToolRejectsInvalidArgumentsBeforeStoreReads(t *testing.T) {
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{"missing operation", map[string]any{}},
		{"malformed operation", map[string]any{"operation_id": "not-a-uuid"}},
		{"malformed task", map[string]any{"task_id": "not-a-uuid"}},
		{"malformed cursor", map[string]any{"cursor": "not-a-uuid"}},
		{"malformed agent", map[string]any{"agent_id": "builder-agent"}},
		{"blank action", map[string]any{"action_name": " \t"}},
		{"zero limit", map[string]any{"limit": 0}},
		{"large limit", map[string]any{"limit": 1001}},
		{"wrong limit type", map[string]any{"limit": "100"}},
		{"unsupported order", map[string]any{"order": "newest"}},
		{"noncanonical order", map[string]any{"order": "ASC"}},
		{"removed latest field", map[string]any{"latest": true}},
		{"unknown parameter", map[string]any{"wait_seconds": 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := maps.Clone(test.args)
			if test.name != "missing operation" && test.name != "malformed operation" {
				arguments["operation_id"] = "10000000-0000-4000-8000-000000000001"
			}
			assertInvalidLogRequest(t, arguments)
		})
	}
	for _, field := range []string{"task_id", "agent_id", "action_name", "order"} {
		for _, value := range []any{"", nil, 42, true} {
			t.Run(fmt.Sprintf("%s/%v", field, value), func(t *testing.T) {
				arguments := map[string]any{"operation_id": "10000000-0000-4000-8000-000000000001", field: value}
				assertInvalidLogRequest(t, arguments)
			})
		}
	}
}

func assertInvalidLogRequest(t *testing.T, arguments map[string]any) {
	t.Helper()
	operations := fakes.NewLogOperations(nil)
	logs := fakes.NewLogQueries(nil, false)
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: operations, ExecutionStore: logs})

	_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "operation_logs", Arguments: arguments})

	var rpcError *jsonrpc.Error
	if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("invalid log request did not return invalid params: %v", err)
	}
	select {
	case read := <-operations.Reads():
		t.Fatalf("invalid log request read operation %s", read)
	default:
	}
	select {
	case read := <-logs.Queries():
		t.Fatalf("invalid log request reached log storage: %+v", read)
	default:
	}
}

func TestMCPLogToolsEnforceParentAndCursorScopes(t *testing.T) {
	stores := fixtures.NewLogStores(t)
	log := fixtures.ExecutionLog(stores.Operation.ID, stores.Operation.Tasks[0].ID, 1)
	foreign := fixtures.ExecutionLog(stores.Other.ID, stores.Other.Tasks[0].ID, 2)
	otherTask := fixtures.ExecutionLog(stores.Operation.ID, stores.Operation.Tasks[1].ID, 3)
	for _, value := range []executions.Log{log, foreign, otherTask} {
		if err := stores.Executions.AppendLog(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	logs := fakes.NewLogQueries(stores.Executions, false)
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: stores.Operations, ExecutionStore: logs})
	for _, test := range []struct {
		message string
		args    map[string]any
	}{
		{"unknown operation", map[string]any{"operation_id": "10000000-0000-4000-8000-000000000099"}},
		{"unknown task", map[string]any{"operation_id": stores.Operation.ID, "task_id": stores.Other.Tasks[0].ID}},
	} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "operation_logs", Arguments: test.args})

		assertLogToolError(t, result, err, test.message)
		select {
		case query := <-logs.Queries():
			t.Fatalf("unknown parent reached log storage: %+v", query)
		default:
		}
	}
	for _, args := range []map[string]any{
		{"operation_id": stores.Operation.ID, "cursor": foreign.ID},
		{"operation_id": stores.Operation.ID, "cursor": log.ID, "agent_id": "builder-agent-87654321"},
		{"operation_id": stores.Operation.ID, "cursor": "30000000-0000-4000-8000-000000000099"},
	} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "operation_logs", Arguments: args})

		assertLogToolError(t, result, err, "unknown execution log")
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "operation_logs", Arguments: map[string]any{
		"operation_id": stores.Operation.ID, "task_id": stores.Operation.Tasks[0].ID, "cursor": otherTask.ID}})

	assertLogToolError(t, result, err, "unknown execution log")
}

func TestMCPLogToolsReadDelayedProgressWithoutChangingTerminalState(t *testing.T) {
	stores := fixtures.NewLogStores(t)
	before, err := stores.Operations.Get(t.Context(), stores.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: stores.Operations, ExecutionStore: stores.Executions})
	args := map[string]any{"operation_id": stores.Operation.ID, "task_id": stores.Operation.Tasks[0].ID}
	empty := callLogTool(t, ctx, session, "operation_logs", args)
	assertLogEntries(t, empty)
	log := fixtures.ExecutionLog(stores.Operation.ID, stores.Operation.Tasks[0].ID, 1)

	if err := stores.Executions.AppendLog(ctx, log); err != nil {
		t.Fatal(err)
	}
	page := callLogTool(t, ctx, session, "operation_logs", args)
	after, err := stores.Operations.Get(ctx, stores.Operation.ID)

	assertLogEntries(t, page, log)
	if err != nil || !reflect.DeepEqual(before, after) || page.NextCursor != log.ID {
		t.Fatalf("reading delayed progress changed terminal operation state: %v", err)
	}
}

func TestMCPLogToolCancellationStopsStorageRead(t *testing.T) {
	stores := fixtures.NewLogStores(t)
	logs := fakes.NewLogQueries(stores.Executions, true)
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: stores.Operations, ExecutionStore: logs})
	callCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("canceled log tool call did not finish")
		}
	})
	go func() {
		_, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "operation_logs", Arguments: map[string]any{"operation_id": stores.Operation.ID}})
		done <- err
	}()
	select {
	case <-logs.Queries():
	case <-ctx.Done():
		t.Fatal("log request did not reach storage")
	}

	cancel()

	select {
	case err := <-logs.Finished():
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("storage did not see cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("storage read outlived the canceled request")
	}
}

func callLogTool(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, name string, args map[string]any) fixtures.LogPage {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || result == nil || result.IsError || len(result.Content) != 1 || result.StructuredContent != nil {
		t.Fatalf("%s failed: result=%+v error=%v", name, result, err)
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatal("log tool did not return text JSON")
	}
	var page fixtures.LogPage
	if err := json.Unmarshal([]byte(content.Text), &page); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content.Text, `"work":`) || strings.Contains(content.Text, `"sequence":`) {
		t.Fatal("log entry exposed nested storage/wire metadata")
	}
	if strings.Contains(content.Text, `"step_id"`) {
		t.Fatal("log entry exposed removed mission-step provenance")
	}
	return page
}

func assertLogEntries(t *testing.T, page fixtures.LogPage, logs ...executions.Log) {
	t.Helper()
	if page.Logs == nil || len(page.Logs) != len(logs) {
		t.Fatalf("log count=%d, want %d", len(page.Logs), len(logs))
	}
	for index, expected := range logs {
		actual := page.Logs[index]
		if actual.ID != expected.ID || actual.AgentID != expected.AgentID || actual.CommandID != expected.CommandID ||
			actual.ActionName != expected.ActionName || actual.OperationID != expected.Work.OperationID || actual.TaskID != expected.Work.TaskID ||
			actual.Message != expected.Message || !actual.CreatedAt.Equal(expected.CreatedAt) {
			t.Fatalf("log provenance changed at index %d: %+v, want %+v", index, actual, expected)
		}
	}
}

func assertLogToolError(t *testing.T, result *sdkmcp.CallToolResult, err error, message string) {
	t.Helper()
	if err != nil || result == nil || !result.IsError || len(result.Content) != 1 {
		t.Fatalf("expected log query tool error: result=%+v err=%v", result, err)
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok || !strings.Contains(content.Text, message) {
		t.Fatalf("tool error did not identify %q: %+v", message, result.Content)
	}
}
