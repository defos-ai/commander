package fixtures

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/domain/actions"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func StartCheckpointAgent(t *testing.T, executor action.Executor) *SecretAgent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	fixture := &SecretAgent{ctx: ctx, cancel: cancel, done: make(chan error, 1),
		bus: fakes.NewSecretBus()}
	noHeartbeat := time.Duration(0)
	runtime, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Actions: []action.Executor{executor},
		ArtifactFileStore: fakes.UnusedArtifacts{}, Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
		HeartbeatInterval: &noHeartbeat, Logger: slog.New(slog.NewTextHandler(&fixture.logs, nil))})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	fixture.runtime = runtime
	go func() { fixture.done <- runtime.Serve(ctx, fixture.bus, nil) }()
	t.Cleanup(func() { fixture.Stop(t) })
	fixture.Next(t, controllerevents.AgentStarted)
	return fixture
}

func CheckpointAction(t *testing.T, run func(context.Context) error) action.Executor {
	t.Helper()
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actions.Action{Name: "checkpoint"},
		Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
			return struct{}{}, run(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}
