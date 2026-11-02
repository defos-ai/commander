package fixtures

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

const ContextActionName = "receive-context"

type ContextAgent struct {
	*SecretAgent
	bus *fakes.ContextBus
}

func StartContextAgent(t *testing.T, pauseWorking bool, run func(context.Context) error) *ContextAgent {
	t.Helper()
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actions.Action{Name: ContextActionName},
		Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
			return struct{}{}, run(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	base := &SecretAgent{ctx: ctx, cancel: cancel, done: make(chan error, 1), bus: fakes.NewSecretBus()}
	fixture := &ContextAgent{SecretAgent: base, bus: fakes.NewContextBus(base.bus, pauseWorking)}
	noHeartbeat := time.Duration(0)
	runtime, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Actions: []action.Executor{executor},
		ArtifactFileStore: fakes.UnusedArtifacts{}, Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
		HeartbeatInterval: &noHeartbeat, Logger: slog.New(slog.NewTextHandler(&base.logs, nil))})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	base.runtime = runtime
	go func() { base.done <- runtime.Serve(ctx, fixture.bus, nil) }()
	t.Cleanup(func() { fixture.bus.ReleaseWorking(); base.Stop(t) })
	base.Next(t, controllerevents.AgentStarted)
	return fixture
}

func (f *ContextAgent) Working(t *testing.T) messages.Message {
	t.Helper()
	select {
	case message := <-f.bus.Working():
		return message
	case <-f.ctx.Done():
		t.Fatal("agent did not reach its working announcement")
		return messages.Message{}
	}
}

func (f *ContextAgent) ReleaseWorking() { f.bus.ReleaseWorking() }

func ContextCommand(t *testing.T, agentID string, work messages.WorkContext, deadline time.Time) messages.Message {
	t.Helper()
	command, err := agentcommands.NewRunAction(agentID, actions.ActionCall{ActionName: ContextActionName, Work: work})
	if err != nil {
		t.Fatal(err)
	}
	command.ExpiresAt = deadline
	return command
}

func SharedContext(work messages.WorkContext) agentevents.ContextUpdate {
	now := time.Now().UTC()
	update := agentevents.ContextUpdate{
		Source: agentevents.ContextSource{AgentID: "scanner-agent-00000001", CommandID: id.MessageID(), ActionName: "scan",
			Work: messages.WorkContext{OperationID: work.OperationID, TaskID: id.TaskID()}},
		Result: json.RawMessage(`{"finding":9007199254740993,"ratio":1.2300e+42}`),
	}
	update.Artifacts = []artifacts.Artifact{{ID: id.ArtifactID(), Name: "evidence", AgentID: update.Source.AgentID,
		ActionName: update.Source.ActionName, CommandID: update.Source.CommandID, CreatedAt: now,
		Files: []artifacts.ArtifactFile{{ID: id.New(), Name: "finding.json", MediaType: "application/json", Size: 2, SHA256: strings.Repeat("a", 64)}}}}
	update.Decision = &operations.Question{ID: id.QuestionID(), TaskID: update.Source.Work.TaskID,
		CommandID: update.Source.CommandID, Key: "prioritize", Text: "Analyze this finding?", Options: []string{"Yes", "No"},
		Status: operations.QuestionAnswered, AskedAt: now.Add(-time.Minute), FallbackAt: now.Add(time.Minute),
		Answer: "Yes", AnsweredBy: operations.AnswerOperator, AnsweredAt: &now}
	return update
}

func SharedContextMessage(t *testing.T, agentID, commandID string, work messages.WorkContext, update agentevents.ContextUpdate) messages.Message {
	t.Helper()
	message, err := agentevents.NewContextUpdate(agentID, commandID, work, update)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
