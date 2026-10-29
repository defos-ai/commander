package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
)

func SeedMaintenanceTask(t *testing.T, config runner.Config, running bool) operations.Operation {
	t.Helper()
	now := time.Now().UTC()
	op := operations.Operation{ID: id.OperationID(), Mission: config.MissionStore.(*fakes.MissionStore).Snapshot(),
		Input: json.RawMessage(`{}`), Status: operations.Planning, CreatedAt: now, UpdatedAt: now}
	task := operations.NewTask(op.ID, "", actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{}`)})
	if _, err := op.QueueTask(task); err != nil {
		t.Fatal(err)
	}
	if running {
		if err := op.DispatchTask(operations.TaskDispatch{TaskID: task.ID, AgentID: AgentID, StartedAt: now, DeadlineAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.OperationStore.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op
}
