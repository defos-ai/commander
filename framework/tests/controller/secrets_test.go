package controller_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestAgentAnnouncementsPreserveSecretDeclarations(t *testing.T) {
	consumer, store, catalog := fixtures.ObservationConsumer(t)
	snapshot := fixtures.ObservationSnapshot()
	snapshot.Actions[0].RequiredSecrets = []string{"API_TOKEN"}
	if err := consumer.Consume(t.Context(), fixtures.ObservationMessage(t, controllerevents.AgentStarted, time.Now().UTC(), snapshot)); err != nil {
		t.Fatal(err)
	}
	record := store.Snapshot()
	if record.Type != snapshot.Type || !slices.Equal(record.Actions[0].RequiredSecrets, []string{"API_TOKEN"}) {
		t.Fatalf("inventory lost secret type/declarations: %+v", record)
	}
	if catalog.Last().Status != actions.Unknown || !slices.Equal(catalog.Last().RequiredSecrets, []string{"API_TOKEN"}) {
		t.Fatalf("catalog projection lost stable declarations: %+v", catalog.Last())
	}
}

func TestRuntimeRequirementsAreScopedToExecutingWork(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*agents.AgentSnapshot)
	}{
		{name: "valid"},
		{name: "invalid name", change: func(s *agents.AgentSnapshot) { s.Work.MissingRequirements[0].Name = "../TOKEN" }},
		{name: "duplicate", change: func(s *agents.AgentSnapshot) {
			s.Work.MissingRequirements = append(s.Work.MissingRequirements, s.Work.MissingRequirements[0])
		}},
		{name: "not working", change: func(s *agents.AgentSnapshot) { s.Status = agents.AgentReady }},
		{name: "no command", change: func(s *agents.AgentSnapshot) { s.Work.CommandID = "" }},
		{name: "incomplete work", change: func(s *agents.AgentSnapshot) { s.Work.TaskID = "" }},
		{name: "unknown action", change: func(s *agents.AgentSnapshot) { s.Work.ActionName = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			consumer, store, catalog := fixtures.ObservationConsumer(t)
			snapshot := fixtures.ObservationSnapshot()
			snapshot.Status = agents.AgentWorking
			snapshot.Work = &agents.Work{CommandID: id.New(), OperationID: id.New(), TaskID: id.New(), ActionName: "inspect",
				MissingRequirements: []actions.Requirement{{Kind: actions.SecretRequirement, Name: "API_TOKEN"}}}
			if test.change != nil {
				test.change(&snapshot)
			}
			err := consumer.Consume(t.Context(), fixtures.ObservationMessage(t, controllerevents.AgentHeartbeat, time.Now().UTC(), snapshot))
			if test.change != nil {
				if !errors.Is(err, consumers.ErrInvalidMessage) || store.Snapshot() != nil || catalog.Count() != 0 {
					t.Fatal("invalid runtime requirement changed inventory")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			record := store.Snapshot()
			if record.Work == nil || !slices.Equal(record.Work.MissingRequirements, snapshot.Work.MissingRequirements) ||
				len(record.Actions[0].MissingRequirements) != 0 || len(catalog.Last().MissingRequirements) != 0 {
				t.Fatal("runtime requirements must stay on the execution, outside action readiness")
			}
		})
	}
}

func TestInvalidSecretAnnouncementsDoNotChangeInventory(t *testing.T) {
	for _, test := range []struct {
		name    string
		secrets []string
	}{
		{name: "invalid name", secrets: []string{"../TOKEN"}},
		{name: "duplicate name", secrets: []string{"API_TOKEN", "API_TOKEN"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			consumer, store, catalog := fixtures.ObservationConsumer(t)
			snapshot := fixtures.ObservationSnapshot()
			snapshot.Actions[0].RequiredSecrets = test.secrets
			err := consumer.Consume(t.Context(), fixtures.ObservationMessage(t, controllerevents.AgentStarted, time.Now().UTC(), snapshot))
			if !errors.Is(err, consumers.ErrInvalidMessage) || store.Snapshot() != nil || catalog.Count() != 0 {
				t.Fatalf("invalid declaration changed inventory: error=%v writes=%d", err, catalog.Count())
			}
		})
	}
}
