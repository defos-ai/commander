package controller_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/internal/state"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestControllerCheckpointsPersistBeforeReplyAndLoadIndependently(t *testing.T) {
	consumer, store, bus := fixtures.CheckpointConsumer(t)
	request := fixtures.CheckpointRequest(t, store.Snapshot().Command, "package", controllerevents.CheckpointSave, `{"version":"v0.2.0"}`)
	bus.BeforePublish(func(message messages.Message) {
		saved, err := store.Get(t.Context(), request.AgentID, request.CorrelationID)
		if err != nil || string(saved.Checkpoints["package"]) != `{"version":"v0.2.0"}` {
			t.Fatal("checkpoint response preceded durable persistence")
		}
		if message.CorrelationID != request.ID || message.Work != request.Work {
			t.Fatal("checkpoint response lost its request or work identity")
		}
	})
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if bus.Topics()[0] != messages.AgentEvents(request.AgentID) {
		t.Fatal("checkpoint response used the action command channel")
	}
	response := checkpointResponse(t, bus)
	if response.Error != "" || string(response.Data) != `{"version":"v0.2.0"}` {
		t.Fatalf("unexpected save response: %+v", response)
	}
	response.Data[0] = '['
	bus.BeforePublish(nil)
	load := fixtures.CheckpointRequest(t, store.Snapshot().Command, "package", controllerevents.CheckpointLoad, "")
	if err := consumer.Consume(t.Context(), load); err != nil {
		t.Fatal(err)
	}
	response = checkpointResponse(t, bus)
	if string(response.Data) != `{"version":"v0.2.0"}` || store.Saves() != 1 {
		t.Fatal("reading mutated checkpoint data or wrote another record")
	}
	missing := fixtures.CheckpointRequest(t, store.Snapshot().Command, "missing", controllerevents.CheckpointLoad, "")
	if err := consumer.Consume(t.Context(), missing); err != nil {
		t.Fatal(err)
	}
	if response := checkpointResponse(t, bus); response.Error != agentevents.CheckpointNotFound || len(response.Data) != 0 {
		t.Fatal("missing checkpoint returned data or an ambiguous response")
	}
}

func TestControllerCheckpointsRequireSavedExecutionOwnership(t *testing.T) {
	for _, scenario := range []string{"unknown agent", "unknown command", "wrong work", "wrong saved owner"} {
		t.Run(scenario, func(t *testing.T) {
			consumer, store, bus := fixtures.CheckpointConsumer(t)
			store.Change(func(record *executions.Execution) {
				record.Checkpoints = map[string]json.RawMessage{"package": json.RawMessage(`{"private":"execution-data"}`)}
			})
			command := store.Snapshot().Command
			switch scenario {
			case "unknown agent":
				command.AgentID = "fixture-agent-ffffffff"
			case "unknown command":
				command.ID = id.MessageID()
			case "wrong work":
				command.Work.TaskID = id.New()
			case "wrong saved owner":
				store.IgnoreLookup()
				command.AgentID = "fixture-agent-ffffffff"
			}
			request := fixtures.CheckpointRequest(t, command, "package", controllerevents.CheckpointLoad, "")
			err := consumer.Consume(t.Context(), request)
			if scenario == "wrong work" || scenario == "wrong saved owner" {
				if !errors.Is(err, consumers.ErrInvalidMessage) || len(bus.Messages()) != 0 {
					t.Fatalf("ownership mismatch was not rejected: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				response := checkpointResponse(t, bus)
				if response.Error != agentevents.CheckpointUnknownExecution || len(response.Data) != 0 {
					t.Fatal("unknown execution exposed another command's checkpoint")
				}
			}
			if store.Saves() != 0 {
				t.Fatal("invalid ownership reached checkpoint persistence")
			}
		})
	}
}

func TestControllerCheckpointReplayRecoversUncertainSaveAndLostReply(t *testing.T) {
	for _, scenario := range []string{"uncertain save", "lost reply"} {
		t.Run(scenario, func(t *testing.T) {
			consumer, store, bus := fixtures.CheckpointConsumer(t)
			failure := errors.New("infrastructure unavailable")
			if scenario == "uncertain save" {
				store.FailSave(failure)
			} else {
				bus.FailPublish(failure)
			}
			request := fixtures.CheckpointRequest(t, store.Snapshot().Command, "package", controllerevents.CheckpointSave, `{"version":"v0.2.0","name":"agent"}`)
			if err := consumer.Consume(t.Context(), request); !errors.Is(err, failure) || errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("infrastructure failure was hidden or treated as bad input: %v", err)
			}
			if len(bus.Messages()) != 0 || len(store.Snapshot().Checkpoints) != 1 {
				t.Fatal("failed save or reply lost recoverable data or acknowledged success")
			}
			store.FailSave(nil)
			bus.FailPublish(nil)
			restarted := consumers.EventConsumer{Executions: store, Bus: bus, State: state.New(t.Context(), time.Hour)}
			// Equivalent JSON with different ordering must recover the first saved fact.
			retry := fixtures.CheckpointRequest(t, store.Snapshot().Command, "package", controllerevents.CheckpointSave, `{"name":"agent","version":"v0.2.0"}`)
			if err := restarted.Consume(t.Context(), retry); err != nil {
				t.Fatal(err)
			}
			response := checkpointResponse(t, bus)
			if response.Error != "" || string(response.Data) != `{"version":"v0.2.0","name":"agent"}` || len(store.Snapshot().Checkpoints) != 1 {
				t.Fatal("replay changed the immutable checkpoint or failed to confirm it")
			}
			if bus.Messages()[0].CorrelationID != retry.ID {
				t.Fatal("response correlated to an earlier request rather than the replay")
			}
		})
	}
}

func TestControllerCheckpointCompletionPreservesExactReplays(t *testing.T) {
	consumer, store, bus := fixtures.CheckpointConsumer(t)
	save := fixtures.CheckpointRequest(t, store.Snapshot().Command, "launch", controllerevents.CheckpointSave, `{"id":"assigned-id"}`)
	if err := consumer.Consume(t.Context(), save); err != nil {
		t.Fatal(err)
	}
	_, result, _ := fixtures.ArtifactResult(t)
	result.AgentID, result.CorrelationID, result.Work = store.Snapshot().Command.AgentID, store.Snapshot().Command.ID, store.Snapshot().Command.Work
	completed := time.Now().UTC()
	store.Change(func(record *executions.Execution) { record.Result, record.CompletedAt = &result, &completed })
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{"launch", `{"id":"assigned-id"}`, ""},
		{"launch", `{"id":"replacement-id"}`, agentevents.CheckpointConflict},
		{"new-fact", `{}`, agentevents.CheckpointCompleted},
	} {
		request := fixtures.CheckpointRequest(t, store.Snapshot().Command, test.name, controllerevents.CheckpointSave, test.data)
		if err := consumer.Consume(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		response := checkpointResponse(t, bus)
		if response.Error != test.want || test.want != "" && len(response.Data) != 0 {
			t.Fatalf("completion replay response=%+v want=%s", response, test.want)
		}
	}
	if len(store.Snapshot().Checkpoints) != 1 || string(store.Snapshot().Checkpoints["launch"]) != `{"id":"assigned-id"}` {
		t.Fatal("completed execution checkpoints changed")
	}
}

func TestControllerCheckpointLimitAndReadFailure(t *testing.T) {
	consumer, store, bus := fixtures.CheckpointConsumer(t)
	for n := range executions.MaxCheckpoints {
		name := "checkpoint-" + string(rune('a'+n/26)) + string(rune('a'+n%26))
		if _, err := store.AddCheckpoint(name, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	request := fixtures.CheckpointRequest(t, store.Snapshot().Command, "extra", controllerevents.CheckpointSave, `{}`)
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if response := checkpointResponse(t, bus); response.Error != agentevents.CheckpointInvalid || store.Saves() != 0 {
		t.Fatal("checkpoint limit was ignored or exposed as an infrastructure failure")
	}
	bus.Reset()
	readError := errors.New("storage unavailable")
	store.FailRead(readError)
	if err := consumer.Consume(t.Context(), request); !errors.Is(err, readError) || errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("read failure was not propagated: %v", err)
	}
	if len(bus.Messages()) != 0 {
		t.Fatal("unreadable execution produced an acknowledgment")
	}
}

func checkpointResponse(t *testing.T, bus *fakes.SecretPublishBus) agentevents.CheckpointResponse {
	t.Helper()
	if len(bus.Messages()) == 0 {
		t.Fatal("no checkpoint response")
	}
	response, err := agentevents.DecodeCheckpointResponse(bus.Messages()[len(bus.Messages())-1])
	if err != nil {
		t.Fatal(err)
	}
	return response
}
