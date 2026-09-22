package controller_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
)

func TestAgentAnnouncementsPopulateAndRefreshActionCatalog(t *testing.T) {
	// Arrange: the catalog starts empty and two agents report the same action.
	f := newDeliveryFixture(t)
	entry := announcedAction()
	const otherAgentID = "fixture-agent-22223333"

	// Act: learn metadata from startup, heartbeat, and discovery updates, then restart.
	announceAction(t, f, messages.AgentStarted, recoveryAgentID, entry)
	entry.Status, entry.StatusReason = actionmodel.Unavailable, "missing executable"
	announceAction(t, f, messages.AgentHeartbeat, otherAgentID, entry)
	entry.Status, entry.StatusReason = actionmodel.Ready, ""
	entry.Description = "Updated observation schema"
	entry.InputSchema = json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"}}}`)
	announceAction(t, f, messages.AgentUpdated, recoveryAgentID, entry)
	f.restart(t)

	// Assert: one durable definition is shared; each agent retains its own readiness.
	catalog, err := f.database.ActionStore.List(t.Context())
	if err != nil || len(catalog) != 1 {
		t.Fatalf("announcements did not create one catalog entry: %+v, %v", catalog, err)
	}
	saved := catalog[0]
	var inputSchema bytes.Buffer
	if err := json.Compact(&inputSchema, saved.InputSchema); err != nil {
		t.Fatal(err)
	}
	if saved.Name != entry.Name || saved.Description != entry.Description ||
		saved.Status != actionmodel.Unknown || saved.StatusReason != "" ||
		inputSchema.String() != string(entry.InputSchema) || len(saved.RequiredPermissions) != 1 {
		t.Fatalf("catalog did not preserve shared metadata: %+v", saved)
	}
	for agentID, status := range map[string]actionmodel.Status{recoveryAgentID: actionmodel.Ready, otherAgentID: actionmodel.Unavailable} {
		record, err := f.database.AgentStore.Get(t.Context(), agentID)
		if err != nil || len(record.Actions) != 1 || record.Actions[0].Status != status {
			t.Fatalf("agent readiness was lost: %+v, %v", record, err)
		}
	}
}

func TestCatalogWriteFailureLeavesAnnouncementUnacknowledged(t *testing.T) {
	// Arrange: the catalog database fails while receiving an agent's first announcement.
	f := newDeliveryFixture(t)
	f.actions.failUpsert.Store(true)
	message := actionAnnouncement(t, messages.AgentStarted, recoveryAgentID, announcedAction())

	// Act.
	f.publish(t, message)
	select {
	case <-f.controller.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("controller did not surface the catalog write failure")
	}

	// Assert: failed persistence cannot acknowledge or expose an accepted agent.
	if !errors.Is(f.controller.Err(), errUnavailableDatabase) || f.bus.wasAcked(message.ID) {
		t.Fatalf("catalog failure was hidden or acknowledged: %v", f.controller.Err())
	}
	if _, err := f.database.AgentStore.Get(t.Context(), recoveryAgentID); !errors.Is(err, agentmodel.ErrUnknownAgent) {
		t.Fatalf("agent was accepted before its catalog was saved: %v", err)
	}
	f.actions.failUpsert.Store(false)
	f.restart(t)
	f.publish(t, message)
	f.waitAck(t, message.ID)
	if _, err := f.database.ActionStore.Get(t.Context(), announcedAction().Name); err != nil {
		t.Fatalf("replayed announcement did not restore the catalog: %v", err)
	}
}

func TestMalformedActionMetadataDoesNotStopAgentAnnouncements(t *testing.T) {
	// Arrange: an invalid schema arrives before a valid announcement.
	f := newDeliveryFixture(t)
	entry := announcedAction()
	entry.InputSchema = json.RawMessage(`[]`)
	invalid := actionAnnouncement(t, messages.AgentStarted, recoveryAgentID, entry)

	// Act.
	f.publish(t, invalid)
	announceAction(t, f, messages.AgentHeartbeat, recoveryAgentID, announcedAction())

	// Assert: invalid metadata is rejected; a later valid agent can still register.
	if f.bus.wasAcked(invalid.ID) {
		t.Fatal("invalid action schema was acknowledged")
	}
	if _, err := f.database.ActionStore.Get(t.Context(), announcedAction().Name); err != nil {
		t.Fatalf("valid action was not registered: %v", err)
	}
}

func announcedAction() actionmodel.Action {
	return actionmodel.Action{
		Name: "inspect-fixture", Description: "Inspect a fixture", Status: actionmodel.Ready,
		RequiredPermissions: []string{"network-access"},
		InputSchema:         json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func actionAnnouncement(t *testing.T, kind messages.MessageType, agentID string, entry actionmodel.Action) messages.Message {
	t.Helper()
	message, err := messages.NewMessage(kind, agentID, agentmodel.AgentSnapshot{
		ID: agentID, Type: "fixture", Status: agentmodel.AgentReady, Actions: []actionmodel.Action{entry},
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func announceAction(t *testing.T, f *deliveryFixture, kind messages.MessageType, agentID string, entry actionmodel.Action) {
	t.Helper()
	message := actionAnnouncement(t, kind, agentID, entry)
	f.publish(t, message)
	f.waitAck(t, message.ID)
}
