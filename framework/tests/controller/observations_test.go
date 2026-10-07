package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/internal/state"
)

func TestDelayedAgentEventsKeepLatestSnapshot(t *testing.T) {
	consumer, store, catalog := observationFixture(t)
	newest := time.Now().UTC().Add(-time.Second)
	current := observationSnapshot()
	current.Status = agents.AgentWorking
	current.Work = &agents.Work{CommandID: "current", ActionName: "inspect"}
	current.Actions[0].Description = "current definition"
	if err := consumer.Consume(t.Context(), observationMessage(t, controllerevents.AgentHeartbeat, newest, current)); err != nil {
		t.Fatal(err)
	}
	before := store.record.Load().Clone()
	changed := consumer.State.Changed()
	waiting := &state.PendingCommand{AgentID: agentID, Reply: make(chan state.CommandReply, 1)}
	consumer.State.Pending["current"] = waiting
	older := observationSnapshot()
	older.Actions[0].Description = "outdated definition"

	for _, kind := range []messages.MessageType{controllerevents.AgentHeartbeat, controllerevents.AgentUpdated, controllerevents.AgentStopped} {
		if kind == controllerevents.AgentStopped {
			older.Status = agents.AgentStopped
		}
		if err := consumer.Consume(t.Context(), observationMessage(t, kind, newest.Add(-time.Second), older)); err != nil {
			t.Fatal(err)
		}
	}

	if after := store.record.Load(); !reflect.DeepEqual(before, *after) {
		t.Fatalf("older events replaced current inventory: %+v", after)
	}
	if catalog.writes != 1 || catalog.last.Description != "current definition" {
		t.Fatalf("older events changed action catalog: %+v", catalog)
	}
	select {
	case <-changed:
		t.Fatal("older events notified inventory watchers")
	default:
	}
	if consumer.State.Pending["current"] != waiting {
		t.Fatal("older stop cancelled current work")
	}
}

func TestAgentLeaseUsesSourceHeartbeatTime(t *testing.T) {
	consumer, store, _ := observationFixture(t)
	observed := time.Now().UTC().Add(-20 * time.Second)
	message := observationMessage(t, controllerevents.AgentHeartbeat, observed, observationSnapshot())

	if err := consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	before := store.record.Load().Clone()
	if err := consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}

	if !before.LastHeartbeat.Equal(observed) || !before.ObservedAt.Equal(observed) || !before.LeaseExpiresAt.Equal(observed.Add(time.Minute)) {
		t.Fatalf("late heartbeat received a fresh lease: %+v", before)
	}
	if after := store.record.Load(); !reflect.DeepEqual(before, *after) {
		t.Fatalf("duplicate heartbeat renewed inventory: %+v", after)
	}
}

func TestUnusableAgentObservationsCannotCreateInventory(t *testing.T) {
	for _, test := range []struct {
		name      string
		timestamp time.Time
		invalid   bool
	}{
		{"expired", time.Now().Add(-time.Hour), false},
		{"missing time", time.Time{}, true},
		{"future time", time.Now().Add(time.Hour), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			consumer, store, catalog := observationFixture(t)
			message := observationMessage(t, controllerevents.AgentHeartbeat, test.timestamp, observationSnapshot())

			err := consumer.Consume(t.Context(), message)

			if errors.Is(err, consumers.ErrInvalidMessage) != test.invalid || (!test.invalid && err != nil) {
				t.Fatalf("unexpected handling result: %v", err)
			}
			if store.record.Load() != nil || catalog.writes != 0 {
				t.Fatal("unusable observation created inventory or action definitions")
			}
		})
	}
}

func TestDelayedDiscoveryReplyReleasesCaller(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "newer snapshot available"
		if expired {
			name = "reply expired"
		}
		t.Run(name, func(t *testing.T) {
			consumer, store, _ := observationFixture(t)
			newest := time.Now().UTC().Add(-time.Second)
			current := observationSnapshot()
			current.Actions[0].Status = actions.Unavailable
			current.Actions[0].StatusReason = "dependency removed"
			if err := consumer.Consume(t.Context(), observationMessage(t, controllerevents.AgentHeartbeat, newest, current)); err != nil {
				t.Fatal(err)
			}
			before := store.record.Load().Clone()
			reply := make(chan state.CommandReply, 1)
			consumer.State.Pending["discovery"] = &state.PendingCommand{AgentID: agentID, Expected: controllerevents.AgentUpdated, Reply: reply}
			observed := newest.Add(-time.Second)
			if expired {
				observed = newest.Add(-time.Hour)
			}
			message := observationMessage(t, controllerevents.AgentUpdated, observed, observationSnapshot())
			message.CorrelationID = "discovery"

			if err := consumer.Consume(t.Context(), message); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(before, *store.record.Load()) {
				t.Fatal("delayed discovery changed current inventory")
			}
			select {
			case received := <-reply:
				if expired {
					if received.Err == nil {
						t.Fatal("expired reply completed command successfully")
					}
				} else if received.Err != nil || !reflect.DeepEqual(received.Record, before) {
					t.Fatalf("late discovery returned outdated readiness: %+v", received)
				}
			default:
				t.Fatal("late discovery did not release its caller")
			}
		})
	}
}

func TestObservationReplayRepairsPartialSave(t *testing.T) {
	consumer, store, catalog := observationFixture(t)
	observed := time.Now().UTC().Add(-time.Second)
	if err := consumer.Consume(t.Context(), observationMessage(t, controllerevents.AgentStarted, observed.Add(-time.Second), observationSnapshot())); err != nil {
		t.Fatal(err)
	}
	stopped := observationSnapshot()
	stopped.Status = agents.AgentStopped
	message := observationMessage(t, controllerevents.AgentStopped, observed, stopped)
	reply := make(chan state.CommandReply, 1)
	consumer.State.Pending["running"] = &state.PendingCommand{AgentID: agentID, Reply: reply}
	failure := errors.New("catalog unavailable")
	catalog.failure = failure
	if err := consumer.Consume(t.Context(), message); !errors.Is(err, failure) {
		t.Fatalf("catalog failure was not reported: %v", err)
	}
	before := store.record.Load().Clone()
	catalog.failure = nil

	if err := consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(before, *store.record.Load()) || catalog.last.Name != "inspect" {
		t.Fatal("replay did not preserve inventory while repairing catalog")
	}
	select {
	case received := <-reply:
		if !errors.Is(received.Err, agents.ErrAgentNotRunning) {
			t.Fatalf("replay did not fail stopped agent's pending work: %+v", received)
		}
	default:
		t.Fatal("partial save left pending work waiting after replay")
	}
}

func observationFixture(t *testing.T) (consumers.AgentEventConsumer, *inventory, *observationCatalog) {
	t.Helper()
	store, catalog := &inventory{}, &observationCatalog{}
	return consumers.AgentEventConsumer{Inventory: store, Actions: catalog, State: state.New(t.Context(), time.Minute)}, store, catalog
}

func observationSnapshot() agents.AgentSnapshot {
	return agents.AgentSnapshot{ID: agentID, Type: "fixture", Status: agents.AgentReady,
		Server:  agents.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
		Actions: []actions.Action{{Name: "inspect", Status: actions.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
	}
}

func observationMessage(t *testing.T, kind messages.MessageType, at time.Time, snapshot agents.AgentSnapshot) messages.Message {
	t.Helper()
	message, err := messages.NewMessage(kind, agentID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	message.CreatedAt = at
	return message
}

type observationCatalog struct {
	actions.ActionStore
	last    actions.Action
	writes  int
	failure error
}

func (s *observationCatalog) Upsert(_ context.Context, entry actions.Action) error {
	if s.failure != nil {
		return s.failure
	}
	s.last, s.writes = entry, s.writes+1
	return nil
}
