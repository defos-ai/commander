package fixtures

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

const LoggingActionName = "report-progress"

type LoggingAgent struct {
	Runtime *agent.Agent
	Bus     *fakes.LogBus
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

func LoggingAction(t *testing.T, run func(context.Context) error) action.Executor {
	t.Helper()
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actions.Action{Name: LoggingActionName},
		Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
			return struct{}{}, run(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func StartLoggingAgent(t *testing.T, parent context.Context, executor action.Executor, config fakes.LogBusConfig) *LoggingAgent {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	t.Cleanup(cancel)
	runtime := StateAgent(t, executor)
	fixture := &LoggingAgent{Runtime: runtime, Bus: fakes.NewLogBus(runtime.ID(), config),
		ctx: ctx, cancel: cancel, done: make(chan struct{})}
	t.Cleanup(func() { fixture.Stop(t) })
	go func() {
		defer close(fixture.done)
		fixture.err = runtime.Serve(ctx, fixture.Bus, nil)
	}()
	fixture.next(t, controllerevents.AgentStarted, "")
	return fixture
}

func (f *LoggingAgent) Cancel() { f.cancel() }

func (f *LoggingAgent) Stop(t *testing.T) {
	t.Helper()
	f.cancel()
	select {
	case <-f.done:
		if f.err != nil && !errors.Is(f.err, context.Canceled) {
			t.Errorf("logging agent shutdown: %v", f.err)
		}
	case <-time.After(5 * time.Second):
		t.Error("logging agent did not stop")
	}
}

func (f *LoggingAgent) Command(t *testing.T, work messages.WorkContext, expiresAt ...time.Time) messages.Message {
	t.Helper()
	command, err := agentcommands.NewRunAction(f.Runtime.ID(), actions.ActionCall{ActionName: LoggingActionName, Work: work})
	if err != nil {
		t.Fatal(err)
	}
	if len(expiresAt) > 0 {
		command.ExpiresAt = expiresAt[0]
	}
	if err := f.Bus.SendCommand(f.ctx, command); err != nil {
		t.Fatal(err)
	}
	return command
}

func (f *LoggingAgent) Attempt(t *testing.T) fakes.LogAttempt {
	t.Helper()
	select {
	case publication := <-f.Bus.Attempts():
		return publication
	case <-f.ctx.Done():
		t.Fatal("agent did not attempt log publication")
		return fakes.LogAttempt{}
	}
}

func (f *LoggingAgent) Outcome(t *testing.T, commandID string) messages.Message {
	t.Helper()
	return f.next(t, "", commandID)
}

func (f *LoggingAgent) next(t *testing.T, kind messages.MessageType, commandID string) messages.Message {
	t.Helper()
	for {
		select {
		case message := <-f.Bus.Results():
			if kind != "" && message.Type == kind || commandID != "" && message.CorrelationID == commandID &&
				(message.Type == controllerevents.ActionSucceeded || message.Type == controllerevents.CommandFailed) {
				return message
			}
		case <-f.ctx.Done():
			t.Fatal("logging agent did not publish the expected event")
			return messages.Message{}
		}
	}
}
