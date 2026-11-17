package fixtures

import (
	"context"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/internal/state"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	controllerfakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

type DependencyAgent struct {
	Context     context.Context
	Runtime     *agent.Agent
	Environment *fakes.ExecutableEnvironment
	Inventory   *controllerfakes.Inventory
	Consumer    consumers.AgentEventConsumer
	bus         *fakes.ResultBus
	observedAt  time.Time
}

type DependencyAgentOptions struct {
	Version string
}

func NewDependencyAgent(t *testing.T, dependencies []agents.Dependency, options ...DependencyAgentOptions) *DependencyAgent {
	t.Helper()
	if len(options) > 1 {
		t.Fatal("dependency agent accepts at most one options value")
	}
	var version string
	if len(options) == 1 {
		version = options[0].Version
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	fixture := &DependencyAgent{Context: ctx, Environment: &fakes.ExecutableEnvironment{},
		Inventory: &controllerfakes.Inventory{}, bus: fakes.NewResultBus(), observedAt: time.Now().UTC().Add(-time.Second)}
	var err error
	fixture.Runtime, err = agent.NewAgent(agent.AgentConfig{
		Type: "fixture", Version: version, Dependencies: dependencies, Environment: fixture.Environment,
		Server:            agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "dependency-fixture"},
		Actions:           []action.Executor{fakes.NewExecutableAction("browse", "lightpanda"), fakes.NewExecutableAction("inspect", "")},
		ArtifactFileStore: fakes.UnusedArtifacts{},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.Consumer = consumers.AgentEventConsumer{Inventory: fixture.Inventory,
		Actions: fakes.DiscardActionCatalog{}, State: state.New(ctx, time.Minute)}
	return fixture
}

func (f *DependencyAgent) Announce(t *testing.T, kind messages.MessageType, snapshot agents.AgentSnapshot) messages.Message {
	t.Helper()
	if err := (controllerevents.Client{Bus: f.bus}).State(f.Context, kind, snapshot, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-f.bus.Results():
		f.observedAt = f.observedAt.Add(time.Millisecond)
		message.CreatedAt = f.observedAt
		return message
	case <-f.Context.Done():
		t.Fatal("agent did not announce its dependency metadata")
		return messages.Message{}
	}
}
