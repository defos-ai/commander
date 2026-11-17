package agent_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/agent"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestAgentVersionSurvivesAnnouncementsAndInventoryReadback(t *testing.T) {
	for _, test := range fixtures.ReportedAgentVersions() {
		t.Run(test.Name, func(t *testing.T) {
			fixture := fixtures.NewDependencyAgent(t, nil, fixtures.DependencyAgentOptions{Version: test.Version})
			if err := fixture.Runtime.DiscoverActions(fixture.Context, nil); err != nil {
				t.Fatal(err)
			}

			started := fixture.Announce(t, controllerevents.AgentStarted, fixture.Runtime.Describe())
			if err := fixture.Consumer.Consume(fixture.Context, started); err != nil {
				t.Fatal(err)
			}
			registered, err := fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
			if err != nil {
				t.Fatal(err)
			}

			assertReportedAgentVersion(t, test.Version, started, registered)
			fixture.Environment.Install()

			if err := fixture.Runtime.DiscoverActions(fixture.Context, nil); err != nil {
				t.Fatal(err)
			}
			updated := fixture.Announce(t, controllerevents.AgentUpdated, fixture.Runtime.Describe())
			if err := fixture.Consumer.Consume(fixture.Context, updated); err != nil {
				t.Fatal(err)
			}
			rediscovered, err := fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
			if err != nil {
				t.Fatal(err)
			}

			assertReportedAgentVersion(t, test.Version, updated, rediscovered)
			if rediscovered.Actions[0].Status != actions.Ready || !rediscovered.LastHeartbeat.Equal(registered.LastHeartbeat) {
				t.Fatalf("rediscovery did not update readiness independently of the heartbeat: %+v", rediscovered)
			}

			heartbeat := fixture.Announce(t, controllerevents.AgentHeartbeat, fixture.Runtime.Describe())
			if err := fixture.Consumer.Consume(fixture.Context, heartbeat); err != nil {
				t.Fatal(err)
			}
			live, err := fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
			if err != nil {
				t.Fatal(err)
			}

			assertReportedAgentVersion(t, test.Version, heartbeat, live)
			if !live.LastHeartbeat.Equal(heartbeat.CreatedAt) || !live.LeaseExpiresAt.After(registered.LeaseExpiresAt) {
				t.Fatalf("heartbeat did not renew the registered agent's lease: %+v", live)
			}
		})
	}
}

func TestInvalidAgentVersionAnnouncementsPreserveAcceptedInventoryAndLease(t *testing.T) {
	for _, test := range fixtures.InvalidAgentVersions() {
		t.Run(test.Name, func(t *testing.T) {
			if _, err := agent.NewAgent(fixtures.VersionConfig(test.Version)); err == nil || !strings.Contains(err.Error(), "version") {
				t.Fatalf("invalid configured version was not rejected: %v", err)
			}
			fixture := fixtures.NewDependencyAgent(t, nil, fixtures.DependencyAgentOptions{Version: "v1.2.3"})
			if err := fixture.Runtime.DiscoverActions(fixture.Context, nil); err != nil {
				t.Fatal(err)
			}
			started := fixture.Announce(t, controllerevents.AgentStarted, fixture.Runtime.Describe())
			if err := fixture.Consumer.Consume(fixture.Context, started); err != nil {
				t.Fatal(err)
			}
			accepted, err := fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
			if err != nil {
				t.Fatal(err)
			}
			snapshot := fixture.Runtime.Describe()
			snapshot.Version = test.Version
			snapshot.Status = agents.AgentUnavailable

			for _, kind := range []messages.MessageType{controllerevents.AgentStarted, controllerevents.AgentUpdated, controllerevents.AgentHeartbeat} {
				announcement := fixture.Announce(t, kind, snapshot)
				if !utf8.ValidString(test.Version) {
					announcement = fixtures.WithRawAgentVersion(t, announcement, test.Version)
				}

				err := fixture.Consumer.Consume(fixture.Context, announcement)

				if !errors.Is(err, consumers.ErrInvalidMessage) {
					t.Fatalf("%s accepted an invalid version: %v", kind, err)
				}
				stored, err := fixture.Inventory.Get(fixture.Context, fixture.Runtime.ID())
				if err != nil || !reflect.DeepEqual(stored, accepted) {
					t.Fatalf("rejected %s changed accepted inventory or renewed its lease: %+v, error=%v", kind, stored, err)
				}
			}
		})
	}
}

func assertReportedAgentVersion(t *testing.T, version string, announcement messages.Message, stored agents.Agent) {
	t.Helper()
	if stored.Version != version {
		t.Fatalf("inventory changed the supplied version: got %q, want %q", stored.Version, version)
	}
	readback, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string][]byte{"announcement": announcement.Payload, "inventory readback": readback} {
		var fields map[string]any
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatal(err)
		}
		reported, exists := fields["version"]
		if exists != (version != "") || exists && reported != version {
			t.Fatalf("%s changed the optional version: got %v, present=%v, want %q", name, reported, exists, version)
		}
	}
}
