package controller_test

import (
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
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestAgentObservationMatchesTypeWithOrWithoutRoleSuffix(t *testing.T) {
	for _, test := range []struct {
		name      string
		agentType agents.AgentType
		invalid   bool
	}{
		{name: "plain type", agentType: "browser"},
		{name: "role suffix", agentType: "browser-agent"},
		{name: "wrong type", agentType: "other-agent", invalid: true},
		{name: "duplicated role", agentType: "browser-agent-agent", invalid: true},
		{name: "malformed type", agentType: "browser--agent", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			consumer, store, catalog := fixtures.ObservationConsumer(t)
			snapshot := fixtures.ObservationSnapshot()
			snapshot.ID, snapshot.Type = "browser-agent-12345678", test.agentType
			message, err := messages.NewMessage(controllerevents.AgentStarted, snapshot.ID, snapshot)
			if err != nil {
				t.Fatal(err)
			}

			err = consumer.Consume(t.Context(), message)

			if test.invalid {
				if !errors.Is(err, consumers.ErrInvalidMessage) || store.Snapshot() != nil || catalog.Count() != 0 {
					t.Fatalf("invalid observation changed inventory: error=%v record=%+v writes=%d", err, store.Snapshot(), catalog.Count())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			record := store.Snapshot()
			if record == nil || record.ID != snapshot.ID || record.Type != test.agentType || catalog.Count() != 1 {
				t.Fatalf("observation changed public identity or missed catalog: record=%+v writes=%d", record, catalog.Count())
			}
		})
	}
}

func TestDelayedAgentEventsKeepLatestSnapshot(t *testing.T) {
	consumer, store, catalog := fixtures.ObservationConsumer(t)
	newest := time.Now().UTC().Add(-time.Second)
	current := fixtures.ObservationSnapshot()
	current.Status = agents.AgentWorking
	current.Work = &agents.Work{CommandID: "current", ActionName: "inspect"}
	current.Actions[0].Description = "current definition"
	if err := consumer.Consume(t.Context(), fixtures.ObservationMessage(t, controllerevents.AgentHeartbeat, newest, current)); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot().Clone()
	changed := consumer.State.Changed()
	waiting := &state.PendingCommand{AgentID: fixtures.AgentID, Reply: make(chan state.CommandReply, 1)}
	consumer.State.Pending["current"] = waiting
	older := fixtures.ObservationSnapshot()
	older.Actions[0].Description = "outdated definition"

	for _, kind := range []messages.MessageType{controllerevents.AgentHeartbeat, controllerevents.AgentUpdated, controllerevents.AgentStopped} {
		if kind == controllerevents.AgentStopped {
			older.Status = agents.AgentStopped
		}
		if err := consumer.Consume(t.Context(), fixtures.ObservationMessage(t, kind, newest.Add(-time.Second), older)); err != nil {
			t.Fatal(err)
		}
	}

	if after := store.Snapshot(); !reflect.DeepEqual(before, *after) {
		t.Fatalf("older events replaced current inventory: %+v", after)
	}
	if catalog.Count() != 1 || catalog.Last().Description != "current definition" {
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
	consumer, store, _ := fixtures.ObservationConsumer(t)
	observed := time.Now().UTC().Add(-20 * time.Second)
	message := fixtures.ObservationMessage(t, controllerevents.AgentHeartbeat, observed, fixtures.ObservationSnapshot())

	if err := consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot().Clone()
	if err := consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}

	if !before.LastHeartbeat.Equal(observed) || !before.ObservedAt.Equal(observed) || !before.LeaseExpiresAt.Equal(observed.Add(time.Minute)) {
		t.Fatalf("late heartbeat received a fresh lease: %+v", before)
	}
	if after := store.Snapshot(); !reflect.DeepEqual(before, *after) {
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
			consumer, store, catalog := fixtures.ObservationConsumer(t)
			message := fixtures.ObservationMessage(t, controllerevents.AgentHeartbeat, test.timestamp, fixtures.ObservationSnapshot())

			err := consumer.Consume(t.Context(), message)

			if errors.Is(err, consumers.ErrInvalidMessage) != test.invalid || (!test.invalid && err != nil) {
				t.Fatalf("unexpected handling result: %v", err)
			}
			if store.Snapshot() != nil || catalog.Count() != 0 {
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
			consumer, store, _ := fixtures.ObservationConsumer(t)
			newest := time.Now().UTC().Add(-time.Second)
			current := fixtures.ObservationSnapshot()
			current.Actions[0].Status = actions.Unavailable
			current.Actions[0].StatusReason = "dependency removed"
			if err := consumer.Consume(t.Context(), fixtures.ObservationMessage(t, controllerevents.AgentHeartbeat, newest, current)); err != nil {
				t.Fatal(err)
			}
			before := store.Snapshot().Clone()
			reply := make(chan state.CommandReply, 1)
			consumer.State.Pending["discovery"] = &state.PendingCommand{AgentID: fixtures.AgentID, Expected: controllerevents.AgentUpdated, Reply: reply}
			observed := newest.Add(-time.Second)
			if expired {
				observed = newest.Add(-time.Hour)
			}
			message := fixtures.ObservationMessage(t, controllerevents.AgentUpdated, observed, fixtures.ObservationSnapshot())
			message.CorrelationID = "discovery"

			if err := consumer.Consume(t.Context(), message); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(before, *store.Snapshot()) {
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
	consumer, store, catalog := fixtures.ObservationConsumer(t)
	observed := time.Now().UTC().Add(-time.Second)
	if err := consumer.Consume(t.Context(), fixtures.ObservationMessage(t, controllerevents.AgentStarted, observed.Add(-time.Second), fixtures.ObservationSnapshot())); err != nil {
		t.Fatal(err)
	}
	stopped := fixtures.ObservationSnapshot()
	stopped.Status = agents.AgentStopped
	message := fixtures.ObservationMessage(t, controllerevents.AgentStopped, observed, stopped)
	reply := make(chan state.CommandReply, 1)
	consumer.State.Pending["running"] = &state.PendingCommand{AgentID: fixtures.AgentID, Reply: reply}
	failure := errors.New("catalog unavailable")
	catalog.Fail(failure)
	if err := consumer.Consume(t.Context(), message); !errors.Is(err, failure) {
		t.Fatalf("catalog failure was not reported: %v", err)
	}
	before := store.Snapshot().Clone()
	catalog.Fail(nil)

	if err := consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(before, *store.Snapshot()) || catalog.Last().Name != "inspect" {
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
