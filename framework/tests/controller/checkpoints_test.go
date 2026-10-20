package controller_test

import (
	"bytes"
	"context"
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
)

func TestControllerCheckpointsPersistBeforeReplyAndLoadIndependently(t *testing.T) {
	consumer, store, bus := checkpointConsumerFixture(t)
	request := checkpointRequest(t, store.record.Command, "package", controllerevents.CheckpointSave, `{"version":"v0.2.0"}`)
	bus.before = func(message messages.Message) {
		saved, err := store.Get(t.Context(), request.AgentID, request.CorrelationID)
		if err != nil || string(saved.Checkpoints["package"]) != `{"version":"v0.2.0"}` {
			t.Fatal("checkpoint response preceded durable persistence")
		}
		if message.CorrelationID != request.ID || message.Work != request.Work {
			t.Fatal("checkpoint response lost its request or work identity")
		}
	}
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if bus.topics[0] != messages.AgentEvents(request.AgentID) {
		t.Fatal("checkpoint response used the action command channel")
	}
	response := checkpointResponse(t, bus)
	if response.Error != "" || string(response.Data) != `{"version":"v0.2.0"}` {
		t.Fatalf("unexpected save response: %+v", response)
	}
	response.Data[0] = '['
	bus.before = nil
	load := checkpointRequest(t, store.record.Command, "package", controllerevents.CheckpointLoad, "")
	if err := consumer.Consume(t.Context(), load); err != nil {
		t.Fatal(err)
	}
	response = checkpointResponse(t, bus)
	if string(response.Data) != `{"version":"v0.2.0"}` || store.saves != 1 {
		t.Fatal("reading mutated checkpoint data or wrote another record")
	}
	missing := checkpointRequest(t, store.record.Command, "missing", controllerevents.CheckpointLoad, "")
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
			consumer, store, bus := checkpointConsumerFixture(t)
			store.record.Checkpoints = map[string]json.RawMessage{"package": json.RawMessage(`{"private":"execution-data"}`)}
			command := store.record.Command
			switch scenario {
			case "unknown agent":
				command.AgentID = "fixture-agent-ffffffff"
			case "unknown command":
				command.ID = id.MessageID()
			case "wrong work":
				command.Work.TaskID = id.New()
			case "wrong saved owner":
				store.ignoreLookup = true
				command.AgentID = "fixture-agent-ffffffff"
			}
			request := checkpointRequest(t, command, "package", controllerevents.CheckpointLoad, "")
			err := consumer.Consume(t.Context(), request)
			if scenario == "wrong work" || scenario == "wrong saved owner" {
				if !errors.Is(err, consumers.ErrInvalidMessage) || len(bus.messages) != 0 {
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
			if store.saves != 0 {
				t.Fatal("invalid ownership reached checkpoint persistence")
			}
		})
	}
}

func TestControllerCheckpointReplayRecoversUncertainSaveAndLostReply(t *testing.T) {
	for _, scenario := range []string{"uncertain save", "lost reply"} {
		t.Run(scenario, func(t *testing.T) {
			consumer, store, bus := checkpointConsumerFixture(t)
			failure := errors.New("infrastructure unavailable")
			if scenario == "uncertain save" {
				store.saveError = failure
			} else {
				bus.failure = failure
			}
			request := checkpointRequest(t, store.record.Command, "package", controllerevents.CheckpointSave, `{"version":"v0.2.0","name":"agent"}`)
			if err := consumer.Consume(t.Context(), request); !errors.Is(err, failure) || errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("infrastructure failure was hidden or treated as bad input: %v", err)
			}
			if len(bus.messages) != 0 || len(store.record.Checkpoints) != 1 {
				t.Fatal("failed save or reply lost recoverable data or acknowledged success")
			}
			store.saveError, bus.failure = nil, nil
			restarted := consumers.EventConsumer{Executions: store, Bus: bus, State: state.New(t.Context(), time.Hour)}
			// Equivalent JSON with different ordering must recover the first saved fact.
			retry := checkpointRequest(t, store.record.Command, "package", controllerevents.CheckpointSave, `{"name":"agent","version":"v0.2.0"}`)
			if err := restarted.Consume(t.Context(), retry); err != nil {
				t.Fatal(err)
			}
			response := checkpointResponse(t, bus)
			if response.Error != "" || string(response.Data) != `{"version":"v0.2.0","name":"agent"}` || len(store.record.Checkpoints) != 1 {
				t.Fatal("replay changed the immutable checkpoint or failed to confirm it")
			}
			if bus.messages[0].CorrelationID != retry.ID {
				t.Fatal("response correlated to an earlier request rather than the replay")
			}
		})
	}
}

func TestControllerCheckpointCompletionPreservesExactReplays(t *testing.T) {
	consumer, store, bus := checkpointConsumerFixture(t)
	save := checkpointRequest(t, store.record.Command, "launch", controllerevents.CheckpointSave, `{"id":"assigned-id"}`)
	if err := consumer.Consume(t.Context(), save); err != nil {
		t.Fatal(err)
	}
	_, result, _ := artifactResult(t)
	result.AgentID, result.CorrelationID, result.Work = store.record.Command.AgentID, store.record.Command.ID, store.record.Command.Work
	completed := time.Now().UTC()
	store.record.Result, store.record.CompletedAt = &result, &completed
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{"launch", `{"id":"assigned-id"}`, ""},
		{"launch", `{"id":"replacement-id"}`, agentevents.CheckpointConflict},
		{"new-fact", `{}`, agentevents.CheckpointCompleted},
	} {
		request := checkpointRequest(t, store.record.Command, test.name, controllerevents.CheckpointSave, test.data)
		if err := consumer.Consume(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		response := checkpointResponse(t, bus)
		if response.Error != test.want || test.want != "" && len(response.Data) != 0 {
			t.Fatalf("completion replay response=%+v want=%s", response, test.want)
		}
	}
	if len(store.record.Checkpoints) != 1 || string(store.record.Checkpoints["launch"]) != `{"id":"assigned-id"}` {
		t.Fatal("completed execution checkpoints changed")
	}
}

func TestControllerCheckpointLimitAndReadFailure(t *testing.T) {
	consumer, store, bus := checkpointConsumerFixture(t)
	for n := range executions.MaxCheckpoints {
		name := "checkpoint-" + string(rune('a'+n/26)) + string(rune('a'+n%26))
		if _, err := store.record.RecordCheckpoint(name, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	request := checkpointRequest(t, store.record.Command, "extra", controllerevents.CheckpointSave, `{}`)
	if err := consumer.Consume(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if response := checkpointResponse(t, bus); response.Error != agentevents.CheckpointInvalid || store.saves != 0 {
		t.Fatal("checkpoint limit was ignored or exposed as an infrastructure failure")
	}
	bus.messages = nil
	store.readError = errors.New("storage unavailable")
	if err := consumer.Consume(t.Context(), request); !errors.Is(err, store.readError) || errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("read failure was not propagated: %v", err)
	}
	if len(bus.messages) != 0 {
		t.Fatal("unreadable execution produced an acknowledgment")
	}
}

func checkpointConsumerFixture(t *testing.T) (consumers.EventConsumer, *checkpointExecutions, *secretPublishBus) {
	t.Helper()
	command, _, _ := artifactResult(t)
	store := &checkpointExecutions{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}}
	bus := &secretPublishBus{}
	return consumers.EventConsumer{Executions: store, Bus: bus, State: state.New(t.Context(), time.Hour)}, store, bus
}

func checkpointRequest(t *testing.T, command messages.Message, name, operation, data string) messages.Message {
	t.Helper()
	request, err := controllerevents.NewCheckpointRequest(command.AgentID, command.Work,
		controllerevents.CheckpointRequest{CommandID: command.ID, Name: name, Operation: operation, Data: json.RawMessage(data)})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func checkpointResponse(t *testing.T, bus *secretPublishBus) agentevents.CheckpointResponse {
	t.Helper()
	if len(bus.messages) == 0 {
		t.Fatal("no checkpoint response")
	}
	response, err := agentevents.DecodeCheckpointResponse(bus.messages[len(bus.messages)-1])
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type checkpointExecutions struct {
	executions.ExecutionStore
	record       executions.Execution
	saveError    error
	readError    error
	ignoreLookup bool
	saves        int
}

func (store *checkpointExecutions) Get(ctx context.Context, agentID, commandID string) (executions.Execution, error) {
	if err := ctx.Err(); err != nil {
		return executions.Execution{}, err
	}
	if store.readError != nil {
		return executions.Execution{}, store.readError
	}
	if !store.ignoreLookup && (agentID != store.record.Command.AgentID || commandID != store.record.Command.ID) {
		return executions.Execution{}, executions.ErrUnknownExecution
	}
	record := store.record
	record.Command.Payload = bytes.Clone(record.Command.Payload)
	record.Checkpoints = make(map[string]json.RawMessage, len(store.record.Checkpoints))
	for name, data := range store.record.Checkpoints {
		record.Checkpoints[name] = bytes.Clone(data)
	}
	if record.Result != nil {
		result := *record.Result
		result.Payload = bytes.Clone(result.Payload)
		record.Result = &result
		completed := *record.CompletedAt
		record.CompletedAt = &completed
	}
	return record, nil
}

func (store *checkpointExecutions) SaveCheckpoint(ctx context.Context, agentID, commandID, name string, data json.RawMessage) error {
	record, err := store.Get(ctx, agentID, commandID)
	if err != nil {
		return err
	}
	store.saves++
	if _, err := record.RecordCheckpoint(name, data); err != nil {
		return err
	}
	store.record = record
	return store.saveError
}
