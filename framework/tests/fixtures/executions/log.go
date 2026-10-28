package fixtures

import (
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
)

func Log() executions.Log {
	return executions.Log{
		ID:         "11111111-1111-4111-8111-111111111111",
		AgentID:    "builder-agent-00000001",
		CommandID:  "22222222-2222-4222-8222-222222222222",
		ActionName: "build-agent",
		Work: messages.WorkContext{
			OperationID: "33333333-3333-4333-8333-333333333333",
			TaskID:      "44444444-4444-4444-8444-444444444444",
			StepID:      "build",
		},
		Message:   "Compiling agent packages",
		CreatedAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC),
	}
}
