package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func CreateMaintenanceOperation(t *testing.T, store *operationfs.OperationFsStore) operations.Operation {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	op := operations.Operation{ID: id.OperationID(), Status: operations.Planning, CreatedAt: created, UpdatedAt: created,
		Mission: missions.Mission{RequiredActions: []string{"inspect"}},
	}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op
}

func MaintenanceInventory() []agents.Agent {
	return []agents.Agent{{AgentSnapshot: agents.AgentSnapshot{ID: "review-agent-00000001", Status: agents.AgentReady,
		Actions: []actions.Action{{Name: "inspect", Status: actions.Ready}},
	}}}
}
