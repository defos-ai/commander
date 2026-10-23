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
)

func TestAgentAnnouncementsPreserveSecretDeclarations(t *testing.T) {
	consumer, store, catalog := observationFixture(t)
	snapshot := observationSnapshot()
	snapshot.Actions[0].RequiredSecrets = []string{"API_TOKEN"}
	if err := consumer.Consume(t.Context(), observationMessage(t, controllerevents.AgentStarted, time.Now().UTC(), snapshot)); err != nil {
		t.Fatal(err)
	}
	record := store.record.Load()
	if record.Type != snapshot.Type || !slices.Equal(record.Actions[0].RequiredSecrets, []string{"API_TOKEN"}) {
		t.Fatalf("inventory lost secret type/declarations: %+v", record)
	}
	if catalog.last.Status != actions.Unknown || !slices.Equal(catalog.last.RequiredSecrets, []string{"API_TOKEN"}) {
		t.Fatalf("catalog projection lost stable declarations: %+v", catalog.last)
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
			consumer, store, catalog := observationFixture(t)
			snapshot := observationSnapshot()
			snapshot.Status = agents.AgentWorking
			snapshot.Work = &agents.Work{CommandID: id.New(), OperationID: id.New(), TaskID: id.New(), ActionName: "inspect",
				MissingRequirements: []actions.Requirement{{Kind: actions.SecretRequirement, Name: "API_TOKEN"}}}
			if test.change != nil {
				test.change(&snapshot)
			}
			err := consumer.Consume(t.Context(), observationMessage(t, controllerevents.AgentHeartbeat, time.Now().UTC(), snapshot))
			if test.change != nil {
				if !errors.Is(err, consumers.ErrInvalidMessage) || store.record.Load() != nil || catalog.writes != 0 {
					t.Fatal("invalid runtime requirement changed inventory")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			record := store.record.Load()
			if record.Work == nil || !slices.Equal(record.Work.MissingRequirements, snapshot.Work.MissingRequirements) ||
				len(record.Actions[0].MissingRequirements) != 0 || len(catalog.last.MissingRequirements) != 0 {
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
			consumer, store, catalog := observationFixture(t)
			snapshot := observationSnapshot()
			snapshot.Actions[0].RequiredSecrets = test.secrets
			err := consumer.Consume(t.Context(), observationMessage(t, controllerevents.AgentStarted, time.Now().UTC(), snapshot))
			if !errors.Is(err, consumers.ErrInvalidMessage) || store.record.Load() != nil || catalog.writes != 0 {
				t.Fatalf("invalid declaration changed inventory: error=%v writes=%d", err, catalog.writes)
			}
		})
	}
}
