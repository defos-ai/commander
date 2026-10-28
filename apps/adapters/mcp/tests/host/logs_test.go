package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
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

func TestMCPLogToolsAdvertiseScopedSchemas(t *testing.T) {
	stores := fixtures.NewLogStores(t)
	ctx, session := fixtures.Session(t, adapter.Config{OperationStore: stores.Operations, ExecutionStore: stores.Executions})

	listing, err := session.ListTools(ctx, nil)

	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"operation_logs", "task_logs"} {
		t.Run(name, func(t *testing.T) {
			var schema jsonschema.Schema
			found := false
			for _, tool := range listing.Tools {
				if tool.Name == name {
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
				t.Fatal("saved log queries are unavailable without a controller")
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			valid := map[string]any{"operation_id": "10000000-0000-4000-8000-000000000001", "limit": 1000,
				"agent_id": "builder-agent-12345678", "action_name": "build-agent", "cursor": ""}
			if name == "task_logs" {
				valid["task_id"] = "20000000-0000-4000-8000-000000000001"
			}
			if err := resolved.Validate(valid); err != nil {
				t.Fatalf("schema rejects supported log filters: %v", err)
			}
			defaults := maps.Clone(valid)
			defaults["cursor"], defaults["limit"] = nil, nil
			if err := resolved.Validate(defaults); err != nil {
				t.Fatalf("schema rejects nullable pagination defaults: %v", err)
			}

			page := callLogTool(t, ctx, session, name, defaults)

			assertLogEntries(t, page)
			if page.NextCursor != "" {
				t.Fatal("null pagination defaults changed the initial cursor")
			}
			for _, field := range []string{"operation_id", "task_id"} {
				if field == "task_id" && name == "operation_logs" {
					continue
				}
				invalid := maps.Clone(valid)
				delete(invalid, field)
				if err := resolved.Validate(invalid); err == nil {
					t.Errorf("schema does not require %s", field)
				}
			}
			for _, change := range []map[string]any{{"limit": 0}, {"limit": 1001}, {"wait_seconds": 1}, {"unknown": true}, {"action_name": " "}} {
				invalid := maps.Clone(valid)
				for field, value := range change {
					invalid[field] = value
				}
				if err := resolved.Validate(invalid); err == nil {
					t.Errorf("schema accepted unsupported input: %+v", invalid)
				}
			}
		})
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
	otherTask.AgentID, otherTask.ActionName, otherTask.Work.StepID = "builder-agent-87654321", "publish-agent", ""
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
		tool string
		args map[string]any
		want []executions.Log
	}{
		{"task_logs", map[string]any{"operation_id": op, "task_id": task, "limit": 1000}, logs[:101]},
		{"operation_logs", map[string]any{"operation_id": op, "agent_id": otherTask.AgentID, "action_name": otherTask.ActionName}, logs[101:]},
		{"task_logs", map[string]any{"operation_id": op, "task_id": task, "agent_id": otherTask.AgentID}, nil},
		{"operation_logs", map[string]any{"operation_id": op, "action_name": "Build-agent"}, nil},
		{"operation_logs", map[string]any{"operation_id": op, "action_name": " build-agent "}, nil},
	} {
		page := callLogTool(t, ctx, session, test.tool, test.args)
		assertLogEntries(t, page, test.want...)
	}
}

func TestMCPLogToolsRejectInvalidArgumentsBeforeStoreReads(t *testing.T) {
	for _, test := range []struct {
		name, tool string
		args       map[string]any
	}{
		{"missing operation", "operation_logs", map[string]any{}},
		{"malformed operation", "operation_logs", map[string]any{"operation_id": "not-a-uuid"}},
		{"missing task", "task_logs", map[string]any{"operation_id": "10000000-0000-4000-8000-000000000001"}},
		{"malformed task", "task_logs", map[string]any{"operation_id": "10000000-0000-4000-8000-000000000001", "task_id": "not-a-uuid"}},
		{"malformed cursor", "operation_logs", map[string]any{"cursor": "not-a-uuid"}},
		{"malformed agent", "operation_logs", map[string]any{"agent_id": "builder-agent"}},
		{"blank action", "operation_logs", map[string]any{"action_name": " \t"}},
		{"zero limit", "operation_logs", map[string]any{"limit": 0}},
		{"large limit", "operation_logs", map[string]any{"limit": 1001}},
		{"wrong limit type", "operation_logs", map[string]any{"limit": "100"}},
		{"unknown parameter", "operation_logs", map[string]any{"wait_seconds": 1}},
		{"task filter on operation tool", "operation_logs", map[string]any{"task_id": "20000000-0000-4000-8000-000000000001"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := maps.Clone(test.args)
			if test.name != "missing operation" && test.name != "malformed operation" {
				arguments["operation_id"] = "10000000-0000-4000-8000-000000000001"
			}
			operations := fakes.NewLogOperations(nil)
			logs := fakes.NewLogQueries(nil, false)
			ctx, session := fixtures.Session(t, adapter.Config{OperationStore: operations, ExecutionStore: logs})

			_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: test.tool, Arguments: arguments})

			var rpcError *jsonrpc.Error
			if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
				t.Fatalf("invalid log request did not return invalid params: %v", err)
			}
			select {
			case read := <-operations.Reads():
				t.Fatalf("invalid arguments read operation %s", read)
			default:
			}
			select {
			case read := <-logs.Queries():
				t.Fatalf("invalid arguments reached log storage: %+v", read)
			default:
			}
		})
	}
}

func TestMCPLogToolsRejectSuppliedEmptyNullOrNonstringFilters(t *testing.T) {
	for _, tool := range []string{"operation_logs", "task_logs"} {
		for _, field := range []string{"agent_id", "action_name"} {
			for _, value := range []any{"", nil, 42} {
				t.Run(fmt.Sprintf("%s/%s/%v", tool, field, value), func(t *testing.T) {
					arguments := map[string]any{"operation_id": "10000000-0000-4000-8000-000000000001", field: value}
					if tool == "task_logs" {
						arguments["task_id"] = "20000000-0000-4000-8000-000000000001"
					}
					operations := fakes.NewLogOperations(nil)
					logs := fakes.NewLogQueries(nil, false)
					ctx, session := fixtures.Session(t, adapter.Config{OperationStore: operations, ExecutionStore: logs})

					_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: arguments})

					var rpcError *jsonrpc.Error
					if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
						t.Fatalf("invalid supplied filter did not return invalid params: %v", err)
					}
					select {
					case read := <-operations.Reads():
						t.Fatalf("invalid filter read operation %s", read)
					default:
					}
					select {
					case read := <-logs.Queries():
						t.Fatalf("invalid filter reached log storage: %+v", read)
					default:
					}
				})
			}
		}
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
		tool, message string
		args          map[string]any
	}{
		{"operation_logs", "unknown operation", map[string]any{"operation_id": "10000000-0000-4000-8000-000000000099"}},
		{"task_logs", "unknown task", map[string]any{"operation_id": stores.Operation.ID, "task_id": stores.Other.Tasks[0].ID}},
	} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: test.tool, Arguments: test.args})

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

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "task_logs", Arguments: map[string]any{
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
	empty := callLogTool(t, ctx, session, "task_logs", args)
	assertLogEntries(t, empty)
	log := fixtures.ExecutionLog(stores.Operation.ID, stores.Operation.Tasks[0].ID, 1)

	if err := stores.Executions.AppendLog(ctx, log); err != nil {
		t.Fatal(err)
	}
	page := callLogTool(t, ctx, session, "task_logs", args)
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
	if strings.Contains(content.Text, `"step_id":""`) {
		t.Fatal("log entry did not omit its empty optional step")
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
			actual.StepID != expected.Work.StepID || actual.Message != expected.Message || !actual.CreatedAt.Equal(expected.CreatedAt) {
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
