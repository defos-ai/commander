package host_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTaskSelectorSurvivesMCPQueriesWhileUnconstrainedWorkContinues(t *testing.T) {
	a := fixtures.Host(t)
	var started fixtures.CommandReceipt
	a.Call("start_operation", fixtures.Object{"mission_name": "review", "input": fixtures.Object{}}, &started)
	for _, invalid := range []fixtures.Object{
		{"selector": fixtures.Object{}},
		{"selector": fixtures.Object{"hostname": nil}},
		{"selector": fixtures.Object{"hostname": ""}},
		{"selector": fixtures.Object{"hostname": true}},
		{"selector": fixtures.Object{"hostname": "https://remote-host"}},
		{"selector": fixtures.Object{"type": "review", "hostname": nil}},
		{"selector": fixtures.Object{"hostname": "remote-host", "id": "review-agent-00000001"}},
		{"selector": fixtures.Object{"hostname": "remote-host", "type": "review"}},
		{"selector": fixtures.Object{"hostname": "remote-host", "version": "v1.0.0"}},
		{"selector": fixtures.Object{"hostname": "remote-host", "owner": "someone"}},
		{"agent_id": "review-agent-00000001"},
		{"session": fixtures.Object{"owner_agent_id": "review-agent-00000001"}},
	} {
		input := fixtures.Object{"operation_id": started.ID, "action_name": "inspect", "input": fixtures.Object{}}
		for name, value := range invalid {
			input[name] = value
		}
		_, err := a.Session.CallTool(a.Context, &mcp.CallToolParams{Name: "assign_task", Arguments: input})
		var invalidParams *jsonrpc.Error
		if !errors.As(err, &invalidParams) || invalidParams.Code != jsonrpc.CodeInvalidParams {
			t.Fatalf("invalid placement reached task execution: input=%+v error=%v", input, err)
		}
	}
	if rejected := a.Get(started.ID); len(rejected.Tasks) != 0 {
		t.Fatalf("invalid selectors created tasks: %+v", rejected.Tasks)
	}
	selector := operations.TaskSelector{Hostname: "remote-host"}
	var selected operations.Task
	a.Call("assign_task", fixtures.Object{
		"operation_id": started.ID, "action_name": "inspect", "input": fixtures.Object{"marker": "selected-host"},
		"selector": fixtures.Object{"hostname": selector.Hostname},
	}, &selected)
	var detail operations.Task
	a.Call("get_task", fixtures.Object{"operation_id": started.ID, "task_id": selected.ID}, &detail)
	var page struct {
		Tasks []operations.Task `json:"tasks"`
	}
	a.Call("operation_tasks", fixtures.Object{"operation_id": started.ID}, &page)
	operation := a.Get(started.ID)

	if len(page.Tasks) != 1 || len(operation.Tasks) != 1 {
		t.Fatalf("MCP queries lost the accepted task: page=%+v operation=%+v", page.Tasks, operation.Tasks)
	}
	for _, task := range []operations.Task{selected, detail, page.Tasks[0], operation.Tasks[0]} {
		if task.ID != selected.ID || task.Selector == nil || *task.Selector != selector || task.Status != operations.TaskQueued || task.AgentID != "" {
			t.Fatalf("MCP task query lost accepted placement or dispatched to another host: %+v", task)
		}
	}
	if string(detail.Input) != `{"marker":"selected-host"}` {
		t.Fatalf("placement changed action input: %s", detail.Input)
	}
	var withoutSelector, nullSelector operations.Task
	a.Call("assign_task", fixtures.Object{"operation_id": started.ID, "action_name": "inspect", "input": fixtures.Object{}}, &withoutSelector)
	a.Call("assign_task", fixtures.Object{"operation_id": started.ID, "action_name": "inspect", "input": fixtures.Object{}, "selector": nil}, &nullSelector)
	continued := a.Wait(started.ID, func(op fixtures.OperationDetail) bool {
		return len(op.Tasks) == 3 && op.Tasks[1].Status == operations.TaskSucceeded && op.Tasks[2].Status == operations.TaskSucceeded
	})

	if withoutSelector.Selector != nil || nullSelector.Selector != nil || continued.Tasks[0].Status != operations.TaskQueued || continued.Tasks[0].AgentID != "" {
		t.Fatalf("optional selector blocked other work or silently fell back: %+v", continued.Tasks)
	}
	for _, task := range continued.Tasks[1:] {
		if task.Selector != nil || task.AgentID == "" {
			t.Fatalf("unconstrained work inherited another task's selector: %+v", task)
		}
	}
	a.Call("cancel_operation", fixtures.Object{"operation_id": started.ID}, nil)
	var canceled operations.Task
	a.Call("get_task", fixtures.Object{"operation_id": started.ID, "task_id": selected.ID}, &canceled)
	if canceled.Status != operations.TaskCancelled || canceled.Selector == nil || *canceled.Selector != selector {
		t.Fatalf("cancellation lost saved placement: %+v", canceled)
	}
	tools, err := a.Session.ListTools(a.Context, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "assign_task" && tool.Name != "stop_agent" {
			continue
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(schema.Properties["selector"], []byte(`"hostname"`)) {
			t.Fatalf("%s does not advertise its supported hostname filter: %s", tool.Name, encoded)
		}
	}
}
