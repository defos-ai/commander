package fixtures

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
)

type LogController struct {
	Context    context.Context
	Events     controllerevents.Client
	Executions *fsexecutions.ExecutionFsStore
	Log        executions.Log
	Claim      executions.Execution
}

func NewLogController(t *testing.T) LogController {
	t.Helper()
	t.Setenv("ENV", "")
	t.Setenv("TASK_TIMEOUT", "")
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	database := filepath.Join(root, "database")
	claims, err := fsexecutions.NewExecutionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	log, command := executionLogData(t)
	claim, reserved, err := claims.Claim(ctx, command)
	if err != nil || !reserved {
		t.Fatalf("reserve execution: reserved=%t error=%v", reserved, err)
	}
	StartController(t, ctx, ControllerArgs(root))
	return LogController{
		Context: ctx, Events: controllerevents.Client{Bus: bus},
		Executions: claims, Log: log, Claim: claim,
	}
}

func executionLogData(t *testing.T) (executions.Log, messages.Message) {
	t.Helper()
	log := executions.Log{
		ID: "11111111-1111-4111-8111-111111111111", AgentID: "builder-agent-12345678",
		ActionName: "build-agent", Message: "Compiling packages ✓",
		Work: messages.WorkContext{
			OperationID: "22222222-2222-4222-8222-222222222222",
			TaskID:      "33333333-3333-4333-8333-333333333333", StepID: "build",
		},
		CreatedAt: time.Date(2026, 10, 27, 20, 21, 22, 123456789, time.UTC),
	}
	command, err := agentcommands.NewRunAction(log.AgentID, actions.ActionCall{
		ActionName: log.ActionName, Input: json.RawMessage(`{}`), Work: log.Work,
	})
	if err != nil {
		t.Fatal(err)
	}
	log.CommandID = command.ID
	return log, command
}
