package agent_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestAgentDependenciesExplainMissingExecutablesWithoutBlockingOtherActions(t *testing.T) {
	dependency := agents.Dependency{Name: "lightpanda", Description: "Install the browser runtime for this host platform.", Version: "v1.2.3"}
	configured := []agents.Dependency{dependency}
	fixture := fixtures.NewDependencyAgent(t, configured)
	configured[0].Description = "changed by the caller"

	if err := fixture.Runtime.DiscoverActions(fixture.Context, nil); err != nil {
		t.Fatal(err)
	}
	message := fixture.Announce(t, controllerevents.AgentStarted, fixture.Runtime.Describe())
	if err := fixture.Consumer.Consume(fixture.Context, message); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(stored.Dependencies, []agents.Dependency{dependency}) {
		t.Fatalf("registration changed the declared dependencies: %+v", stored.Dependencies)
	}
	if stored.Actions[0].Status != actions.Unavailable || len(stored.Actions[0].MissingRequirements) != 1 || stored.Actions[1].Status != actions.Ready {
		t.Fatalf("missing browser runtime affected unrelated actions: %+v", stored.Actions)
	}
	if guidance, ok := stored.DependencyFor(stored.Actions[0].MissingRequirements[0]); !ok || guidance != dependency {
		t.Fatalf("missing executable did not resolve to its installation guidance: %+v, found=%v", guidance, ok)
	}
	for _, requirement := range []actions.Requirement{
		{Kind: actions.ConfigurationRequirement, Name: "lightpanda"},
		{Kind: actions.SecretRequirement, Name: "lightpanda"},
		{Kind: actions.ExecutableRequirement, Name: "Lightpanda"},
	} {
		if guidance, ok := stored.DependencyFor(requirement); ok {
			t.Fatalf("unrelated requirement inherited executable guidance: %+v => %+v", requirement, guidance)
		}
	}
	returned := fixture.Runtime.Describe()
	returned.Dependencies[0].Version = "changed by a reader"
	stored.Dependencies[0].Name = "changed inventory snapshot"
	unchanged, err := fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
	if err != nil || !slices.Equal(unchanged.Dependencies, []agents.Dependency{dependency}) ||
		!slices.Equal(fixture.Runtime.Describe().Dependencies, []agents.Dependency{dependency}) {
		t.Fatalf("returned snapshots shared dependency state: inventory=%+v error=%v", unchanged.Dependencies, err)
	}
	fixture.Environment.Install()

	if err := fixture.Runtime.DiscoverActions(fixture.Context, nil); err != nil {
		t.Fatal(err)
	}
	message = fixture.Announce(t, controllerevents.AgentUpdated, fixture.Runtime.Describe())
	if err := fixture.Consumer.Consume(fixture.Context, message); err != nil {
		t.Fatal(err)
	}
	stored, err = fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
	if err != nil {
		t.Fatal(err)
	}

	if stored.Actions[0].Status != actions.Ready || len(stored.Actions[0].MissingRequirements) != 0 ||
		stored.Actions[1].Status != actions.Ready || !slices.Equal(stored.Dependencies, []agents.Dependency{dependency}) {
		t.Fatalf("rediscovery lost declarations or retained missing requirements: %+v", stored.AgentSnapshot)
	}
}

func TestInvalidDependencyAnnouncementsPreserveAcceptedAgentInventory(t *testing.T) {
	dependency := agents.Dependency{Name: "lightpanda", Description: "Browser runtime"}
	for _, test := range []struct {
		name         string
		dependencies []agents.Dependency
	}{
		{name: "duplicate names", dependencies: []agents.Dependency{dependency, dependency}},
		{name: "blank description", dependencies: []agents.Dependency{{Name: "lightpanda"}}},
		{name: "untrimmed version", dependencies: []agents.Dependency{{Name: "lightpanda", Description: "Browser runtime", Version: " v1.2.3"}}},
		{name: "oversized version", dependencies: []agents.Dependency{{Name: "lightpanda", Description: "Browser runtime", Version: strings.Repeat("v", 257)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := fixtures.NewDependencyAgent(t, []agents.Dependency{dependency})
			if _, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Dependencies: test.dependencies,
				Server: fixture.Runtime.Describe().Server}); err == nil || !strings.Contains(err.Error(), "dependenc") {
				t.Fatalf("invalid configured dependencies were not rejected: %v", err)
			}
			if err := fixture.Runtime.DiscoverActions(fixture.Context, nil); err != nil {
				t.Fatal(err)
			}
			accepted := fixture.Announce(t, controllerevents.AgentStarted, fixture.Runtime.Describe())
			if err := fixture.Consumer.Consume(fixture.Context, accepted); err != nil {
				t.Fatal(err)
			}
			snapshot := fixture.Runtime.Describe()
			snapshot.Dependencies = test.dependencies
			invalid := fixture.Announce(t, controllerevents.AgentHeartbeat, snapshot)

			err := fixture.Consumer.Consume(fixture.Context, invalid)

			if !errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("invalid dependencies were accepted: %v", err)
			}
			stored, err := fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
			if err != nil || !stored.ObservedAt.Equal(accepted.CreatedAt) || !stored.LastHeartbeat.Equal(accepted.CreatedAt) ||
				!slices.Equal(stored.Dependencies, []agents.Dependency{dependency}) {
				t.Fatalf("rejected metadata changed accepted inventory or renewed its lease: %+v, error=%v", stored, err)
			}
		})
	}
}
