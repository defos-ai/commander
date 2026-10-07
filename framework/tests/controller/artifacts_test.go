package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/internal/state"
)

func TestArtifactsRecoveredBeforeResultAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command, result, output := artifactResult(t)
	durable := &artifactExecutions{record: executions.Execution{Command: command, StartedAt: time.Now().UTC()}}
	unavailable := errors.New("artifact database unavailable")
	catalog := &artifactCatalog{records: make(map[string]artifacts.Artifact), failID: output.Artifacts[1].ID, failure: unavailable}
	bus := artifactBus()
	consumer := consumers.CommandResultConsumer{Inventory: &inventory{}, Executions: durable, Artifacts: catalog, Bus: bus, State: state.New(ctx, time.Hour)}

	err := consumer.Consume(ctx, result)

	if !errors.Is(err, unavailable) || errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("metadata failure must remain retryable: %v", err)
	}
	stored, err := durable.Get(ctx, agentID, command.ID)
	if err != nil || stored.Result == nil || stored.Result.ID != result.ID || !bytes.Equal(stored.Result.Payload, result.Payload) {
		t.Fatalf("metadata failure lost durable execution: %+v, %v", stored, err)
	}
	if len(catalog.records) != 1 || !catalog.records[output.Artifacts[0].ID].Equal(output.Artifacts[0]) {
		t.Fatalf("expected partially saved metadata: %+v", catalog.records)
	}
	assertNoArtifactReceipt(t, bus)
	catalog.failure = nil

	replacement, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: &inventory{}, ActionStore: unusedActions{}, ExecutionStore: durable, ArtifactStore: catalog,
		HeartbeatTimeout: time.Hour, LeaseSweepInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := replacement.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	var call actions.ActionCall
	if err := json.Unmarshal(command.Payload, &call); err != nil {
		t.Fatal(err)
	}

	recovered, err := replacement.RecoverAction(ctx, agentID, call)

	if err != nil || !bytes.Equal(recovered.Output, output.Output) || len(recovered.Artifacts) != len(output.Artifacts) {
		t.Fatalf("replacement controller did not recover durable output: %+v, %v", recovered, err)
	}
	for i, expected := range output.Artifacts {
		if !recovered.Artifacts[i].Equal(expected) {
			t.Fatalf("recovery changed artifact metadata: %+v", recovered.Artifacts[i])
		}
		saved, err := catalog.Get(ctx, expected.ID)
		if err != nil || !saved.Equal(expected) {
			t.Fatalf("recovery changed artifact or file identity: %+v, %v", saved, err)
		}
	}
	if len(catalog.records) != 2 {
		t.Fatalf("recovery created duplicate metadata: %+v", catalog.records)
	}
	assertNoArtifactReceipt(t, bus)

	bus.events <- result
	if ack := receive(t, ctx, bus.acked); ack != result.ID {
		t.Fatalf("unexpected result acknowledgement: %s", ack)
	}
	receipt := receive(t, ctx, bus.commands)
	if receipt.Type != messages.ActionResultAccepted || receipt.CorrelationID != command.ID || receipt.Work != command.Work {
		t.Fatalf("unexpected receipt after metadata repair: %+v", receipt)
	}
	var accepted struct {
		ResultID string `json:"result_id"`
	}
	if err := json.Unmarshal(receipt.Payload, &accepted); err != nil || accepted.ResultID != result.ID {
		t.Fatalf("receipt acknowledged a different result: %+v, %v", accepted, err)
	}
	if durable.record.Result.ID != result.ID || len(catalog.records) != 2 {
		t.Fatal("replay changed durable result or duplicated metadata")
	}
}

func TestRejectedResultsDoNotCreateArtifacts(t *testing.T) {
	for _, mismatch := range []string{"agent", "command", "action", "duplicate metadata"} {
		t.Run(mismatch, func(t *testing.T) {
			command, result, output := artifactResult(t)
			switch mismatch {
			case "agent":
				output.Artifacts[0].AgentID = "fixture-agent-00000002"
			case "command":
				output.Artifacts[0].CommandID = id.New()
			case "action":
				output.Artifacts[0].ActionName = "different"
			case "duplicate metadata":
				conflict := output.Artifacts[0].Clone()
				conflict.Files[0].Size++
				output.Artifacts = append(output.Artifacts, conflict)
			}
			result.Payload = artifactJSON(t, output)
			durable := &artifactExecutions{record: executions.Execution{Command: command, StartedAt: time.Now().UTC()}}
			catalog := &artifactCatalog{records: make(map[string]artifacts.Artifact)}
			bus := artifactBus()
			consumer := consumers.CommandResultConsumer{Inventory: &inventory{}, Executions: durable, Artifacts: catalog, Bus: bus, State: state.New(t.Context(), time.Hour)}

			err := consumer.Consume(t.Context(), result)

			if !errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("invalid artifact ownership or metadata accepted: %v", err)
			}
			if durable.completions != 0 || durable.record.Result != nil || catalog.writes != 0 {
				t.Fatalf("invalid result reached persistence: completions=%d artifact writes=%d", durable.completions, catalog.writes)
			}
			assertNoArtifactReceipt(t, bus)
		})
	}

	t.Run("conflicting result replay", func(t *testing.T) {
		command, accepted, output := artifactResult(t)
		durable := &artifactExecutions{record: executions.Execution{Command: command, StartedAt: time.Now().UTC()}}
		catalog := &artifactCatalog{records: make(map[string]artifacts.Artifact)}
		bus := artifactBus()
		consumer := consumers.CommandResultConsumer{Inventory: &inventory{}, Executions: durable, Artifacts: catalog, Bus: bus, State: state.New(t.Context(), time.Hour)}
		if err := consumer.Consume(t.Context(), accepted); err != nil {
			t.Fatal(err)
		}
		<-bus.commands
		writes := catalog.writes
		output.Artifacts[0].ID = id.New()
		output.Artifacts[0].Files[0].ID = id.New()
		conflict := accepted
		conflict.Payload = artifactJSON(t, output)

		err := consumer.Consume(t.Context(), conflict)

		if !errors.Is(err, consumers.ErrInvalidMessage) || !errors.Is(err, executions.ErrExecutionConflict) {
			t.Fatalf("conflicting result was not rejected: %v", err)
		}
		if catalog.writes != writes || !bytes.Equal(durable.record.Result.Payload, accepted.Payload) {
			t.Fatal("conflicting replay changed the durable result or attempted artifact registration")
		}
		if _, err := catalog.Get(t.Context(), output.Artifacts[0].ID); !errors.Is(err, artifacts.ErrUnknownArtifact) {
			t.Fatalf("conflicting replay registered a new artifact: %v", err)
		}
		assertNoArtifactReceipt(t, bus)
	})
}

func TestCompletionClearsOnlyMatchingWork(t *testing.T) {
	for _, newerWork := range []bool{false, true} {
		name := "completed work"
		if newerWork {
			name = "newer work"
		}
		t.Run(name, func(t *testing.T) {
			command, result, _ := artifactResult(t)
			current := &agents.Work{OperationID: command.Work.OperationID, TaskID: command.Work.TaskID, ActionName: "capture", CommandID: command.ID}
			if newerWork {
				current.CommandID, current.TaskID = id.New(), id.New()
			}
			store := &inventory{}
			if err := store.Upsert(t.Context(), agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: agentID,
				Status: agents.AgentWorking, Work: current}, LastSeen: command.CreatedAt, ObservedAt: command.CreatedAt}); err != nil {
				t.Fatal(err)
			}
			consumer := consumers.CommandResultConsumer{
				Inventory: store, Executions: &artifactExecutions{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}},
				Artifacts: &artifactCatalog{records: make(map[string]artifacts.Artifact)}, Bus: artifactBus(), State: state.New(t.Context(), time.Hour),
			}
			if err := consumer.Consume(t.Context(), result); err != nil {
				t.Fatal(err)
			}
			record, err := store.Get(t.Context(), agentID)
			if err != nil {
				t.Fatal(err)
			}
			if newerWork {
				if record.Status != agents.AgentWorking || record.Work == nil || *record.Work != *current {
					t.Fatalf("completion overwrote newer work: %+v", record)
				}
			} else if record.Status != agents.AgentReady || record.Work != nil {
				t.Fatalf("completion left finished work in inventory: %+v", record)
			}
		})
	}
}

func TestDelayedResultsPersistWithoutChangingInventory(t *testing.T) {
	for _, scenario := range []string{"newer observation", "expired result", "missing agent"} {
		t.Run(scenario, func(t *testing.T) {
			command, result, _ := artifactResult(t)
			if scenario == "expired result" {
				command.CreatedAt = command.CreatedAt.Add(-2 * time.Hour)
				result.CreatedAt = result.CreatedAt.Add(-2 * time.Hour)
			}
			store := &inventory{}
			if scenario != "missing agent" {
				record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: agentID, Status: agents.AgentUnavailable,
					Work: &agents.Work{CommandID: command.ID, ActionName: "capture"}},
					ObservedAt: result.CreatedAt.Add(time.Second), LastSeen: time.Now().UTC()}
				if scenario == "expired result" {
					record.ObservedAt = command.CreatedAt
				}
				if err := store.Upsert(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			}
			before := store.record.Load()
			durable := &artifactExecutions{record: executions.Execution{Command: command, StartedAt: command.CreatedAt}}
			catalog := &artifactCatalog{records: make(map[string]artifacts.Artifact)}
			bus := artifactBus()
			consumer := consumers.CommandResultConsumer{Inventory: store, Executions: durable,
				Artifacts: catalog, Bus: bus, State: state.New(t.Context(), time.Minute)}

			if err := consumer.Consume(t.Context(), result); err != nil {
				t.Fatal(err)
			}

			if durable.record.Result == nil || durable.record.Result.ID != result.ID || len(catalog.records) != 2 {
				t.Fatal("late result or artifacts were lost")
			}
			if !reflect.DeepEqual(before, store.record.Load()) {
				t.Fatalf("late result changed inventory: %+v", store.record.Load())
			}
			select {
			case receipt := <-bus.commands:
				if receipt.Type != messages.ActionResultAccepted || receipt.CorrelationID != command.ID {
					t.Fatalf("unexpected receipt: %+v", receipt)
				}
			default:
				t.Fatal("late durable result did not receive acceptance")
			}
		})
	}
}

func artifactResult(t *testing.T) (messages.Message, messages.Message, actions.ActionOutput) {
	t.Helper()
	work := messages.WorkContext{OperationID: id.New(), TaskID: id.New()}
	call := actions.ActionCall{ActionName: "capture", Work: work, Input: json.RawMessage(`{"url":"https://example.test"}`)}
	command := messages.Message{ID: work.TaskID, Type: messages.RunAction, AgentID: agentID, Work: work, Payload: artifactJSON(t, call), CreatedAt: time.Now().UTC()}
	metadata := func(name, mediaType string) artifacts.Artifact {
		return artifacts.Artifact{ID: id.New(), Name: name, AgentID: agentID, ActionName: call.ActionName, CommandID: command.ID, CreatedAt: time.Now().UTC(),
			Files: []artifacts.ArtifactFile{{ID: id.New(), Name: name, MediaType: mediaType, Size: 2, SHA256: strings.Repeat("a", 64)}}}
	}
	output := actions.ActionOutput{ActionName: call.ActionName, Output: json.RawMessage(`{}`), Artifacts: []artifacts.Artifact{
		metadata("desktop.png", "image/png"), metadata("mobile.png", "image/png"),
	}}
	result := messages.Message{ID: id.New(), Type: messages.ActionSucceeded, AgentID: agentID, CorrelationID: command.ID, Work: work, Payload: artifactJSON(t, output), CreatedAt: time.Now().UTC()}
	return command, result, output
}

func artifactJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func artifactBus() *testBus {
	return &testBus{events: make(chan messages.Message, 8), commands: make(chan messages.Message, 8), acked: make(chan string, 8), closed: make(chan struct{})}
}

func assertNoArtifactReceipt(t *testing.T, bus *testBus) {
	t.Helper()
	select {
	case message := <-bus.commands:
		t.Fatalf("unexpected command or result acceptance: %+v", message)
	default:
	}
}

type artifactCatalog struct {
	records map[string]artifacts.Artifact
	failID  string
	failure error
	writes  int
}

func (s *artifactCatalog) Create(_ context.Context, record artifacts.Artifact) error {
	s.writes++
	if record.ID == s.failID && s.failure != nil {
		return s.failure
	}
	if previous, found := s.records[record.ID]; found && !previous.Equal(record) {
		return artifacts.ErrArtifactConflict
	}
	s.records[record.ID] = record.Clone()
	return nil
}

func (s *artifactCatalog) Get(_ context.Context, artifactID string) (artifacts.Artifact, error) {
	record, found := s.records[artifactID]
	if !found {
		return artifacts.Artifact{}, artifacts.ErrUnknownArtifact
	}
	return record.Clone(), nil
}

type artifactExecutions struct {
	executions.ExecutionStore
	record      executions.Execution
	completions int
}

func (s *artifactExecutions) Get(_ context.Context, owner, commandID string) (executions.Execution, error) {
	if owner != s.record.Command.AgentID || commandID != s.record.Command.ID {
		return executions.Execution{}, executions.ErrUnknownExecution
	}
	cloned := s.record
	cloned.Command.Payload = bytes.Clone(cloned.Command.Payload)
	if cloned.Result != nil {
		result := *cloned.Result
		result.Payload = bytes.Clone(result.Payload)
		cloned.Result = &result
		completed := *cloned.CompletedAt
		cloned.CompletedAt = &completed
	}
	return cloned, nil
}

func (s *artifactExecutions) Complete(_ context.Context, result messages.Message) error {
	s.completions++
	if s.record.Result != nil {
		if s.record.Result.ID != result.ID || !bytes.Equal(s.record.Result.Payload, result.Payload) {
			return executions.ErrExecutionConflict
		}
		return nil
	}
	result.Payload = bytes.Clone(result.Payload)
	completed := time.Now().UTC()
	candidate := s.record
	candidate.Result, candidate.CompletedAt = &result, &completed
	if err := candidate.Validate(); err != nil {
		return err
	}
	s.record = candidate
	return nil
}
