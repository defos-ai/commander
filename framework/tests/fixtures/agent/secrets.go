package fixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func (f *SecretAgent) Command(t *testing.T, name, input string, work ...messages.WorkContext) string {
	t.Helper()
	call := actions.ActionCall{ActionName: name, Input: json.RawMessage(input)}
	if len(work) > 0 {
		call.Work = work[0]
	}
	message, err := agentcommands.NewRunAction(f.runtime.ID(), call)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.bus.SendCommand(f.ctx, fakes.NewSecretDelivery(message)); err != nil {
		t.Fatal("agent stopped before accepting command")
	}
	return message.ID
}

func (f *SecretAgent) Outcome(t *testing.T, commandID string, kind messages.MessageType) messages.Message {
	t.Helper()
	for {
		select {
		case message := <-f.bus.Outgoing():
			if message.CorrelationID != commandID || (message.Type != controllerevents.ActionSucceeded && message.Type != controllerevents.CommandFailed) {
				continue
			}
			if message.Type != kind {
				t.Fatalf("unexpected command outcome: %s", message.Type)
			}
			return message
		case <-f.ctx.Done():
			t.Fatal("agent did not finish command")
			return messages.Message{}
		}
	}
}

type SecretAgent struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan error
	runtime  *agent.Agent
	executor *fakes.ContextSecretAction
	bus      *fakes.SecretBus
	stopped  bool
	logs     bytes.Buffer
}

func StartSecretAgent(t *testing.T, ttl, refresh time.Duration, requireOnDiscovery ...bool) *SecretAgent {
	t.Helper()
	require := len(requireOnDiscovery) > 0 && requireOnDiscovery[0]
	executor := fakes.NewContextSecretAction(require)
	unrelated := &fakes.SecretAction{Base: action.NewBase(actions.Action{Name: "ping", InputSchema: []byte(`{}`), OutputSchema: []byte(`{}`)})}
	fixture := StartSecretAgentActions(t, ttl, refresh, executor, unrelated)
	fixture.executor = executor
	return fixture
}

func StartSecretAgentActions(t *testing.T, ttl, refresh time.Duration, executors ...action.Executor) *SecretAgent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	fixture := &SecretAgent{ctx: ctx, cancel: cancel,
		bus: fakes.NewSecretBus(), done: make(chan error, 1)}
	noHeartbeat := time.Duration(0)
	runtime, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Actions: executors, ArtifactFileStore: fakes.UnusedArtifacts{},
		Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"}, HeartbeatInterval: &noHeartbeat,
		Logger:  slog.New(slog.NewTextHandler(&fixture.logs, nil)),
		Secrets: &agent.SecretConfig{RequestTTL: ttl, RefreshInterval: refresh, RetryInterval: 20 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime = runtime
	go func() { fixture.done <- runtime.Serve(ctx, fixture.bus, nil) }()
	t.Cleanup(func() { fixture.Stop(t) })
	fixture.Next(t, controllerevents.AgentStarted)
	return fixture
}

func (f *SecretAgent) Stop(t *testing.T) {
	t.Helper()
	if f.stopped {
		return
	}
	f.stopped = true
	f.cancel()
	select {
	case err := <-f.done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("secret agent shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("secret agent did not stop")
	}
}

func (f *SecretAgent) Next(t *testing.T, kind messages.MessageType) messages.Message {
	t.Helper()
	for {
		select {
		case message := <-f.bus.Outgoing():
			if message.Type == kind {
				return message
			}
		case <-f.ctx.Done():
			t.Fatalf("no %s event", kind)
			return messages.Message{}
		}
	}
}

func (f *SecretAgent) Request(t *testing.T, previous string) secrets.Request {
	t.Helper()
	for {
		message := f.Next(t, controllerevents.SecretSyncRequested)
		payload, err := controllerevents.DecodeSecretSyncRequested(message)
		if err != nil {
			t.Fatal(err)
		}
		if payload.Request.ID != previous {
			return payload.Request
		}
	}
}

func (f *SecretAgent) SecretMetadata(request secrets.Request, expiry time.Time) secrets.Metadata {
	now := time.Now().UTC()
	return secrets.Metadata{ID: id.New(), Key: request.Key, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: expiry}
}

func (f *SecretAgent) Deliver(t *testing.T, message messages.Message) {
	t.Helper()
	delivery := fakes.NewSecretDelivery(message)
	if err := f.bus.SendIncoming(f.ctx, delivery); err != nil {
		t.Fatal("agent stopped before delivery")
	}
	select {
	case <-delivery.Acknowledged():
	case <-f.ctx.Done():
		t.Fatal("agent did not process delivery")
	}
}

func (f *SecretAgent) Context() context.Context       { return f.ctx }
func (f *SecretAgent) Runtime() *agent.Agent          { return f.runtime }
func (f *SecretAgent) SecretContext() context.Context { return f.executor.Context() }
func (f *SecretAgent) PauseDiscovery(started, release chan struct{}) {
	f.executor.PauseDiscovery(started, release)
}
func (f *SecretAgent) ExecutionCount() int32             { return f.executor.Calls() }
func (f *SecretAgent) FailNextAcknowledgement()          { f.bus.FailNextAcknowledgement() }
func (f *SecretAgent) History() []messages.Message       { return f.bus.History() }
func (f *SecretAgent) Outgoing() <-chan messages.Message { return f.bus.Outgoing() }
func (f *SecretAgent) Logs() string                      { return f.logs.String() }
func (f *SecretAgent) SendCommand(t *testing.T, delivery *fakes.SecretDelivery) {
	t.Helper()
	if err := f.bus.SendCommand(f.ctx, delivery); err != nil {
		t.Fatal(err)
	}
}
