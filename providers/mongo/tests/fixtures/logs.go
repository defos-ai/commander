package fixtures

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	mongoexecutions "github.com/defos-ai/commander/providers/mongo/executions"
	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func OpenLogDatabase(t *testing.T) *store.MongoStore {
	t.Helper()
	database := OpenStore(t, "execution_logs")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := database.Delete(ctx, "execution_log_counters", bson.D{}); err != nil {
			t.Error(err)
		}
	})
	if _, err := database.Delete(t.Context(), "execution_log_counters", bson.D{}); err != nil {
		t.Fatal(err)
	}
	return database
}

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

func NumberedExecutionLogs(count int) []executions.Log {
	logs := make([]executions.Log, count)
	for index := range logs {
		logs[index] = ExecutionLog()
		logs[index].ID = fmt.Sprintf("10000000-0000-4000-8000-%012d", index+1)
		logs[index].Message = fmt.Sprintf("Progress %d", index+1)
		logs[index].CreatedAt = logs[index].CreatedAt.Add(-time.Duration(index) * time.Hour)
	}
	return logs
}
