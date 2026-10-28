package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	mongoexecutions "github.com/defos-ai/commander/providers/mongo/executions"
	"github.com/defos-ai/commander/providers/mongo/store"
)

func OpenExecutionStore(t *testing.T, database *store.MongoStore) *mongoexecutions.ExecutionMongoStore {
	t.Helper()
	catalog, err := mongoexecutions.NewExecutionMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func ExecutionLog() executions.Log {
	return executions.Log{
		ID: "10000000-0000-4000-8000-000000000001", AgentID: "builder-agent-01234567",
		CommandID: "20000000-0000-4000-8000-000000000001", ActionName: "build-agent",
		Work: messages.WorkContext{
			OperationID: "30000000-0000-4000-8000-000000000001",
			TaskID:      "40000000-0000-4000-8000-000000000001", StepID: "build",
		},
		Message:   "  Compiling <module>\nChecking UTF-8: готово 👋\t",
		CreatedAt: time.Date(2026, 10, 29, 12, 0, 0, 123456789, time.UTC),
	}
}
