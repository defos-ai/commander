package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPOperationQueriesExposeSavedAffinity(t *testing.T) {
	database := t.TempDir()
	store, err := operationfs.NewOperationFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	const token = "affinity-test-token"
	endpoint, client, _ := hostfixtures.StartWithOptions(t, []string{"--database-url=" + database},
		cli.Options{Credentials: &host.BearerCredentials{Token: token}}, io.Discard)
	client.Transport = fakes.BearerTransport{Token: token, Next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "affinity-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	read := func(t *testing.T, tool string, arguments map[string]any, out any) {
		t.Helper()
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: arguments})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s failed to read saved operation: %+v", tool, result)
		}
		content, ok := result.Content[0].(*sdkmcp.TextContent)
		if !ok {
			t.Fatalf("%s did not return text JSON", tool)
		}
		if err := json.Unmarshal([]byte(content.Text), out); err != nil {
			t.Fatal(err)
		}
	}
	type operationView struct {
		operations.Operation
		Affinity *operations.Affinity `json:"affinity"`
	}
	for _, test := range []struct {
		name     string
		bound    bool
		terminal bool
		invalid  bool
		noRule   bool
	}{
		{name: "no affinity", noRule: true},
		{name: "not yet bound"},
		{name: "bound owner absent from inventory", bound: true},
		{name: "terminal operation retains binding", bound: true, terminal: true},
		{name: "conflicting saved assignments remain readable", bound: true, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			deadline := now.Add(time.Minute)
			op := operations.Operation{ID: id.OperationID(), Status: operations.Planning,
				Mission: missions.Mission{Name: "build", Rules: missions.RuleSet{
					missions.SameAgent: {ForActions: []string{"checkout-template", "build-agent"}},
				}}, CreatedAt: now, UpdatedAt: now}
			if test.noRule {
				op.Mission.Rules = nil
			}
			op.Tasks = []operations.Task{
				operations.NewTask(op.ID, "", "", actions.NextAction{ActionName: "checkout-template"}),
				operations.NewTask(op.ID, "", "", actions.NextAction{ActionName: "build-agent"}),
			}
			if test.bound {
				op.Status = operations.Waiting
				op.Tasks[0].AgentID, op.Tasks[0].Status = "builder-agent-12345678", operations.TaskSucceeded
				op.Tasks[0].StartedAt, op.Tasks[0].FinishedAt, op.Tasks[0].DeadlineAt = now, &now, &deadline
			}
			if test.terminal {
				op.Status = operations.Cancelled
				op.Tasks[1].Status = operations.TaskCancelled
				op.Tasks[1].FinishedAt = &now
			}
			if test.invalid {
				op.Tasks[1].AgentID, op.Tasks[1].Status = "builder-agent-87654321", operations.TaskRunning
				op.Tasks[1].StartedAt, op.Tasks[1].DeadlineAt = now, &deadline
			} else {
				op.Preflight, err = op.AssessPreflight(nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Create(ctx, op); err != nil {
				t.Fatal(err)
			}
			before, err := store.Get(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			var detail operationView
			read(t, "get_operation", map[string]any{"operation_id": op.ID}, &detail)
			var listing struct {
				Operations []operationView `json:"operations"`
			}
			read(t, "list_operations", map[string]any{}, &listing)
			var summary *operationView
			for i := range listing.Operations {
				if listing.Operations[i].ID == op.ID {
					summary = &listing.Operations[i]
				}
			}
			if summary == nil {
				t.Fatal("operation absent from listing")
			}
			for _, view := range []*operationView{&detail, summary} {
				if test.noRule {
					if view.Affinity != nil {
						t.Fatalf("operation without same-agent gained affinity: %+v", view.Affinity)
					}
					continue
				}
				if view.Affinity == nil || !reflect.DeepEqual(view.Affinity.ForActions, []string{"checkout-template", "build-agent"}) {
					t.Fatalf("lost affinity action group: %+v", view.Affinity)
				}
				if test.invalid {
					if view.Affinity.Error == "" || view.Affinity.AgentID != "" {
						t.Fatalf("conflicting assignments became a valid binding: %+v", view.Affinity)
					}
					continue
				}
				wantAgent := ""
				if test.bound {
					wantAgent = op.Tasks[0].AgentID
				}
				if view.Affinity.AgentID != wantAgent || view.Affinity.Error != "" {
					t.Fatalf("unexpected affinity: %+v", view.Affinity)
				}
				if !reflect.DeepEqual(view.Preflight, before.Preflight) {
					t.Fatal("affinity query changed the saved readiness assessment")
				}
			}
			if !reflect.DeepEqual(detail.Tasks, before.Tasks) {
				t.Fatal("query changed saved tasks or assigned queued work")
			}
			after, err := store.Get(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("query persisted derived affinity")
			}
		})
	}
}
