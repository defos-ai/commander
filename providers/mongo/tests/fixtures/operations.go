package fixtures

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	mongooperations "github.com/defos-ai/commander/providers/mongo/operations"
	"github.com/defos-ai/commander/providers/mongo/store"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func OpenOperations(t *testing.T) (*mongooperations.OperationMongoStore, *store.MongoStore) {
	t.Helper()
	database := OpenStore(t, "operations")
	clean := func(ctx context.Context) {
		for _, collection := range []string{"operation_tasks", "operation_questions", "operation_events"} {
			if _, err := database.Delete(ctx, collection, bson.D{}); err != nil {
				t.Error(err)
			}
		}
	}
	clean(t.Context())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		clean(ctx)
	})
	catalog, err := mongooperations.NewOperationMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, database
}

func Operation() operations.Operation {
	created := time.Date(2026, time.September, 30, 12, 0, 0, 123456789, time.UTC)
	op := operations.Operation{ID: id.OperationID(), Status: operations.Planning, CreatedAt: created, UpdatedAt: created,
		Mission: missions.Mission{Name: "review", Objectives: []string{"Inspect pages"}, RequiredActions: []string{"inspect"}, InputSchema: json.RawMessage(`{ "type":"object", "x-example":9007199254740993 }`), OutputSchema: json.RawMessage(`{"type":"object"}`)},
		Input:   json.RawMessage(`{ "number":9007199254740993 }`), Tasks: []operations.Task{}, Questions: []operations.Question{}, Events: []operations.Event{}}
	op.Events = append(op.Events, operations.Event{Sequence: 1, OperationID: op.ID, Type: operations.OperationStarted, Status: operations.Planning, CreatedAt: created})
	return op
}

func CreateOperation(t *testing.T, catalog operations.OperationStore, op operations.Operation) operations.Operation {
	t.Helper()
	if err := catalog.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return ReadOperation(t, catalog, op.ID)
}

func ReadOperation(t *testing.T, catalog operations.OperationStore, operationID string) operations.Operation {
	t.Helper()
	op, err := catalog.Get(t.Context(), operationID)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func QueueAndDispatch(t *testing.T, catalog operations.OperationStore, operationID, action string) operations.Task {
	t.Helper()
	task := operations.NewTask(operationID, "", actions.NextAction{ActionName: action, Input: json.RawMessage(`{"value":9007199254740993}`)})
	if _, err := catalog.QueueTask(t.Context(), operationID, task); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.DispatchTask(t.Context(), operationID, Dispatch(task.ID)); err != nil {
		t.Fatal(err)
	}
	saved, err := catalog.GetTask(t.Context(), operationID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func Dispatch(taskID string) operations.TaskDispatch {
	started := time.Now().UTC()
	return operations.TaskDispatch{TaskID: taskID, AgentID: "review-agent-00000001", StartedAt: started, DeadlineAt: started.Add(time.Hour)}
}

func SuccessfulTask(task operations.Task) operations.TaskResult {
	return operations.TaskResult{TaskID: task.ID, AgentID: task.AgentID, Status: operations.TaskSucceeded, FinishedAt: time.Now().UTC(), Result: &actions.ActionOutput{ActionName: task.ActionName, Output: json.RawMessage(`{"number":9007199254740993}`)}}
}

func Question(taskID string) operations.Question {
	asked := time.Now().UTC()
	return operations.Question{ID: id.QuestionID(), TaskID: taskID, Status: operations.QuestionPending, Text: "Continue the review?", Options: []string{"Continue", "Stop"}, AskedAt: asked, FallbackAt: asked.Add(time.Hour)}
}
