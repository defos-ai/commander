package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
	agentfs "github.com/defos-ai/commander/providers/fs/agents"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPTaskQueriesProjectLiveExecutionRequirements(t *testing.T) {
	database := t.TempDir()
	agentStore, err := agentfs.NewAgentFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	operationStore, err := operationfs.NewOperationFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	deadline := now.Add(time.Minute)
	task := operations.NewTask(id.OperationID(), "", "", actions.NextAction{ActionName: "runtime-secret"})
	task.AgentID, task.Status, task.StartedAt, task.DeadlineAt = "builder-agent-12345678", operations.TaskRunning, now, &deadline
	op := operations.Operation{ID: task.OperationID, Status: operations.Running,
		Preflight: &operations.Preflight{Ready: true, Version: 1}, Tasks: []operations.Task{task}, CreatedAt: now, UpdatedAt: now}
	if err := operationStore.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	want := []actions.Requirement{{Kind: actions.SecretRequirement, Name: "GITHUB_TOKEN"}}
	record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{
		ID: task.AgentID, Type: "builder-agent", Status: agents.AgentWorking,
		Work: &agents.Work{OperationID: op.ID, TaskID: task.ID, CommandID: task.CommandID,
			ActionName: task.ActionName, MissingRequirements: want},
	}, ObservedAt: now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Hour)}
	if err := agentStore.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	const token = "task-requirements-test-token"
	endpoint, client, _ := startMCPWithOptions(t, []string{"--database-url=" + database},
		cli.Options{Credentials: &host.BearerCredentials{Token: token}}, io.Discard)
	client.Transport = bearerTransport{token: token, next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "task-requirements-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	type taskView struct {
		operations.Task
		AgentType           string                `json:"agent_type"`
		MissingRequirements []actions.Requirement `json:"missing_requirements"`
	}
	readTasks := func(tool string) []taskView {
		t.Helper()
		args := map[string]any{"operation_id": op.ID}
		if tool == "get_task" {
			args["task_id"] = task.ID
		}
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s failed to read saved task", tool)
		}
		content, ok := result.Content[0].(*sdkmcp.TextContent)
		if !ok {
			t.Fatalf("%s did not return text JSON", tool)
		}
		if tool == "get_task" {
			var item taskView
			if err := json.Unmarshal([]byte(content.Text), &item); err != nil {
				t.Fatal(err)
			}
			return []taskView{item}
		}
		var view struct {
			Tasks     []taskView            `json:"tasks"`
			Preflight *operations.Preflight `json:"preflight"`
			UpdatedAt time.Time             `json:"updated_at"`
		}
		if err := json.Unmarshal([]byte(content.Text), &view); err != nil {
			t.Fatal(err)
		}
		if tool == "get_operation" && (view.Preflight == nil || !view.Preflight.Ready) {
			t.Fatal("runtime wait changed static preflight")
		}
		return view.Tasks
	}
	for _, tool := range []string{"get_operation", "get_task", "operation_tasks"} {
		items := readTasks(tool)
		if len(items) != 1 || items[0].ID != task.ID || items[0].Status != operations.TaskRunning ||
			items[0].AgentType != string(record.Type) || !reflect.DeepEqual(items[0].MissingRequirements, want) ||
			items[0].DeadlineAt == nil || !items[0].DeadlineAt.Equal(deadline) {
			t.Fatalf("%s lost live task wait or saved execution fields: %+v", tool, items)
		}
	}
	if saved, err := operationStore.Get(ctx, op.ID); err != nil || !saved.UpdatedAt.Equal(op.UpdatedAt) || len(saved.Events) != 0 {
		t.Fatal("query changed saved operation")
	}
	record.ObservedAt = record.ObservedAt.Add(time.Second)
	record.Work.TaskID = id.TaskID()
	if err := agentStore.Upsert(ctx, record); err != nil {
		t.Fatal(err)
	}
	if items := readTasks("get_operation"); len(items[0].MissingRequirements) != 0 || items[0].AgentType != "" {
		t.Fatal("unrelated agent work became an operation secret wait")
	}
	record.ObservedAt = record.ObservedAt.Add(time.Second)
	record.Work.TaskID = task.ID
	if err := agentStore.Upsert(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := operationStore.Fail(ctx, op.ID, "fixture finished"); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"get_operation", "get_task", "operation_tasks"} {
		if items := readTasks(tool); len(items[0].MissingRequirements) != 0 || items[0].AgentType != "" {
			t.Fatalf("%s exposed a wait on a terminal operation", tool)
		}
	}
}
