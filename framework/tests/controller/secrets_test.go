package controller_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/actions"
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
