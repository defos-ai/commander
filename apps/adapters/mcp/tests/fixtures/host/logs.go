package host

import (
	"fmt"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	executionfs "github.com/defos-ai/commander/providers/fs/executions"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

type LogStores struct {
	Operations operations.OperationStore
	Executions executions.ExecutionStore
	Operation  operations.Operation
	Other      operations.Operation
}

func NewLogStores(t *testing.T) *LogStores {
	t.Helper()
	root := t.TempDir()
	operationsStore, err := operationfs.NewOperationFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	executionsStore, err := executionfs.NewExecutionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	operation := operations.Operation{ID: "10000000-0000-4000-8000-000000000001", Status: operations.Cancelled,
		Tasks: []operations.Task{
			{ID: "20000000-0000-4000-8000-000000000001", Status: operations.TaskInterrupted},
			{ID: "20000000-0000-4000-8000-000000000002", Status: operations.TaskCancelled},
		}}
	for index := range operation.Tasks {
		operation.Tasks[index].OperationID = operation.ID
	}
	other := operations.Operation{ID: "10000000-0000-4000-8000-000000000002", Status: operations.Running,
		Tasks: []operations.Task{{ID: "20000000-0000-4000-8000-000000000003", Status: operations.TaskRunning,
			OperationID: "10000000-0000-4000-8000-000000000002"}}}
	for _, value := range []operations.Operation{operation, other} {
		if err := operationsStore.Create(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	return &LogStores{Operations: operationsStore, Executions: executionsStore, Operation: operation, Other: other}
}

func ExecutionLog(operationID, taskID string, index int) executions.Log {
	return executions.Log{ID: fmt.Sprintf("30000000-0000-4000-8000-%012d", index), AgentID: "builder-agent-12345678",
		CommandID: "40000000-0000-4000-8000-000000000001", ActionName: "build-agent",
		Work:      messages.WorkContext{OperationID: operationID, TaskID: taskID, StepID: "build"},
		Message:   "  Compiling <module>…\nChecking packages ✓  ",
		CreatedAt: time.Date(2026, 10, 29, 12, 0, 0, 123456789-index, time.UTC)}
}

type LogEntry struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	CommandID   string    `json:"command_id"`
	ActionName  string    `json:"action_name"`
	OperationID string    `json:"operation_id"`
	TaskID      string    `json:"task_id"`
	StepID      string    `json:"step_id,omitempty"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
}

type LogPage struct {
	Logs       []LogEntry `json:"logs"`
	NextCursor string     `json:"next_cursor"`
}
