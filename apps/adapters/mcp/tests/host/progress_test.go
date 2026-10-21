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
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPOperationQueriesShareProgressAndRetainHistory(t *testing.T) {
	database := t.TempDir()
	store, err := operationfs.NewOperationFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	const token = "progress-test-token"
	endpoint, client, _ := startMCPWithOptions(t, []string{"--database-url=" + database},
		cli.Options{Credentials: &host.BearerCredentials{Token: token}}, io.Discard)
	client.Transport = bearerTransport{token: token, next: client.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "progress-test", Version: "1"}, nil).Connect(ctx,
		&sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	read := func(t *testing.T, tool string, args map[string]any, out any) {
		t.Helper()
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: args})
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
		TaskCounts         map[operations.TaskStatus]int `json:"task_counts"`
		PendingDecisions   int                           `json:"pending_decisions"`
		PendingQuestions   int                           `json:"pending_questions"`
		LastQuestion       *operations.Question          `json:"last_question"`
		RemainingQuestions int                           `json:"remaining_questions"`
	}
	for _, status := range []operations.Status{operations.Waiting, operations.Cancelled} {
		t.Run(string(status), func(t *testing.T) {
			now := time.Now().UTC()
			op := operations.Operation{ID: id.OperationID(), Status: status,
				Mission: missions.Mission{Name: "progress"}, CreatedAt: now, UpdatedAt: now}
			for _, status := range []operations.TaskStatus{operations.TaskSucceeded, operations.TaskSucceeded, operations.TaskInterrupted} {
				task := operations.NewTask(op.ID, "", "", actions.NextAction{ActionName: "review"})
				task.Status = status
				op.Tasks = append(op.Tasks, task)
			}
			op.Tasks[0].Continuation = &operations.Continuation{Status: operations.ContinuationPending, DecisionBy: operations.DecisionModel, Version: 1}
			op.Tasks[1].Continuation = &operations.Continuation{Status: operations.ContinuationWaiting, DecisionBy: operations.DecisionCode, Version: 1}
			op.Questions = []operations.Question{
				{ID: "00000000-0000-0000-0000-000000000002", Text: "Latest question", Status: operations.QuestionPending, AskedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)},
				{ID: "00000000-0000-0000-0000-000000000001", Text: "Other question", Status: operations.QuestionPending, AskedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)},
				{ID: "00000000-0000-0000-0000-000000000003", Text: "Expired but not yet reconciled", Status: operations.QuestionPending, AskedAt: now.Add(-30 * time.Second), ExpiresAt: now.Add(-time.Second)},
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
			if want := (map[operations.TaskStatus]int{operations.TaskSucceeded: 2, operations.TaskInterrupted: 1}); !reflect.DeepEqual(summary.TaskCounts, want) {
				t.Fatalf("summary lost task states: %+v", summary.TaskCounts)
			}
			if status.Terminal() {
				if summary.PendingDecisions != 0 || summary.PendingQuestions != 0 {
					t.Fatal("terminal operation advertised actionable decisions or questions")
				}
			} else if summary.PendingDecisions != 1 || summary.PendingQuestions != 2 {
				t.Fatalf("summary pending decisions/questions = %d/%d; want 1/2", summary.PendingDecisions, summary.PendingQuestions)
			}
			for _, view := range []*operationView{&detail, summary} {
				if status.Terminal() {
					if view.LastQuestion != nil || view.RemainingQuestions != 0 {
						t.Fatal("terminal operation exposed a pending question")
					}
				} else if view.LastQuestion == nil || view.LastQuestion.ID != op.Questions[0].ID || view.RemainingQuestions != 1 {
					t.Fatalf("question selection lost expiry or tie-break semantics: last=%+v remaining=%d", view.LastQuestion, view.RemainingQuestions)
				}
			}
			if !reflect.DeepEqual(detail.Questions, before.Questions) || !reflect.DeepEqual(detail.Tasks, before.Tasks) {
				t.Fatal("query changed task or question history")
			}
			after, err := store.Get(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("query mutated saved operation")
			}
		})
	}
}
