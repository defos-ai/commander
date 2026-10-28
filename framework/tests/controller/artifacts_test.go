package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestArtifactsRecoveredBeforeResultAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command, result, output := fixtures.ArtifactResult(t)
	durable := fakes.NewArtifactExecutions(executions.Execution{Command: command, StartedAt: time.Now().UTC()})
	unavailable := errors.New("artifact database unavailable")
	catalog := fakes.NewArtifactCatalog()
	catalog.FailOn(output.Artifacts[1].ID, unavailable)
	bus := fakes.NewBus()
	consumer := consumers.CommandResultConsumer{Inventory: &fakes.Inventory{}, Executions: durable, Artifacts: catalog, Bus: bus, State: state.New(ctx, time.Hour)}

	err := consumer.Consume(ctx, result)

	if !errors.Is(err, unavailable) || errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("metadata failure must remain retryable: %v", err)
	}
	stored, err := durable.Get(ctx, fixtures.AgentID, command.ID)
	if err != nil || stored.Result == nil || stored.Result.ID != result.ID || !bytes.Equal(stored.Result.Payload, result.Payload) {
		t.Fatalf("metadata failure lost durable execution: %+v, %v", stored, err)
	}
	if len(catalog.Snapshot()) != 1 || !catalog.Snapshot()[output.Artifacts[0].ID].Equal(output.Artifacts[0]) {
		t.Fatalf("expected partially saved metadata: %+v", catalog.Snapshot())
	}
	assertNoArtifactReceipt(t, bus)
	catalog.Fail(nil)

	replacement, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: &fakes.Inventory{}, ActionStore: fakes.UnusedActions{}, ExecutionStore: durable, ArtifactStore: catalog,
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

	recovered, err := replacement.RecoverAction(ctx, fixtures.AgentID, call)

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
	if len(catalog.Snapshot()) != 2 {
		t.Fatalf("recovery created duplicate metadata: %+v", catalog.Snapshot())
	}
	assertNoArtifactReceipt(t, bus)

	if err := bus.Send(ctx, result); err != nil {
		t.Fatal(err)
	}
	if ack := fixtures.Receive(t, ctx, bus.Acknowledged()); ack != result.ID {
		t.Fatalf("unexpected result acknowledgement: %s", ack)
	}
	receipt := fixtures.Receive(t, ctx, bus.Commands())
	if receipt.Type != messages.ActionResultAccepted || receipt.CorrelationID != command.ID || receipt.Work != command.Work {
		t.Fatalf("unexpected receipt after metadata repair: %+v", receipt)
	}
	var accepted struct {
		ResultID string `json:"result_id"`
	}
	if err := json.Unmarshal(receipt.Payload, &accepted); err != nil || accepted.ResultID != result.ID {
		t.Fatalf("receipt acknowledged a different result: %+v, %v", accepted, err)
	}
	if durable.Snapshot().Result.ID != result.ID || len(catalog.Snapshot()) != 2 {
		t.Fatal("replay changed durable result or duplicated metadata")
	}
}

func TestRejectedResultsDoNotCreateArtifacts(t *testing.T) {
	for _, mismatch := range []string{"agent", "command", "action", "duplicate metadata"} {
		t.Run(mismatch, func(t *testing.T) {
			command, result, output := fixtures.ArtifactResult(t)
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
			result.Payload = fixtures.JSON(t, output)
			durable := fakes.NewArtifactExecutions(executions.Execution{Command: command, StartedAt: time.Now().UTC()})
			catalog := fakes.NewArtifactCatalog()
			bus := fakes.NewBus()
			consumer := consumers.CommandResultConsumer{Inventory: &fakes.Inventory{}, Executions: durable, Artifacts: catalog, Bus: bus, State: state.New(t.Context(), time.Hour)}

			err := consumer.Consume(t.Context(), result)

			if !errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("invalid artifact ownership or metadata accepted: %v", err)
			}
			if durable.Completions() != 0 || durable.Snapshot().Result != nil || catalog.Writes() != 0 {
				t.Fatalf("invalid result reached persistence: completions=%d artifact writes=%d", durable.Completions(), catalog.Writes())
			}
			assertNoArtifactReceipt(t, bus)
		})
	}

	t.Run("conflicting result replay", func(t *testing.T) {
		command, accepted, output := fixtures.ArtifactResult(t)
		durable := fakes.NewArtifactExecutions(executions.Execution{Command: command, StartedAt: time.Now().UTC()})
		catalog := fakes.NewArtifactCatalog()
		bus := fakes.NewBus()
		consumer := consumers.CommandResultConsumer{Inventory: &fakes.Inventory{}, Executions: durable, Artifacts: catalog, Bus: bus, State: state.New(t.Context(), time.Hour)}
		if err := consumer.Consume(t.Context(), accepted); err != nil {
			t.Fatal(err)
		}
		<-bus.Commands()
		writes := catalog.Writes()
		output.Artifacts[0].ID = id.New()
		output.Artifacts[0].Files[0].ID = id.New()
		conflict := accepted
		conflict.Payload = fixtures.JSON(t, output)

		err := consumer.Consume(t.Context(), conflict)

		if !errors.Is(err, consumers.ErrInvalidMessage) || !errors.Is(err, executions.ErrExecutionConflict) {
			t.Fatalf("conflicting result was not rejected: %v", err)
		}
		if catalog.Writes() != writes || !bytes.Equal(durable.Snapshot().Result.Payload, accepted.Payload) {
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
			command, result, _ := fixtures.ArtifactResult(t)
			current := &agents.Work{OperationID: command.Work.OperationID, TaskID: command.Work.TaskID, ActionName: "capture", CommandID: command.ID}
			if newerWork {
				current.CommandID, current.TaskID = id.New(), id.New()
			}
			store := &fakes.Inventory{}
			if err := store.Upsert(t.Context(), agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: fixtures.AgentID,
				Status: agents.AgentWorking, Work: current}, LastSeen: command.CreatedAt, ObservedAt: command.CreatedAt}); err != nil {
				t.Fatal(err)
			}
			consumer := consumers.CommandResultConsumer{
				Inventory: store, Executions: fakes.NewArtifactExecutions(executions.Execution{Command: command, StartedAt: command.CreatedAt}),
				Artifacts: fakes.NewArtifactCatalog(), Bus: fakes.NewBus(), State: state.New(t.Context(), time.Hour),
			}
			if err := consumer.Consume(t.Context(), result); err != nil {
				t.Fatal(err)
			}
			record, err := store.Get(t.Context(), fixtures.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			if newerWork {
				if record.Status != agents.AgentWorking || !reflect.DeepEqual(record.Work, current) {
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
			command, result, _ := fixtures.ArtifactResult(t)
			if scenario == "expired result" {
				command.CreatedAt = command.CreatedAt.Add(-2 * time.Hour)
				result.CreatedAt = result.CreatedAt.Add(-2 * time.Hour)
			}
			store := &fakes.Inventory{}
			if scenario != "missing agent" {
				record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: fixtures.AgentID, Status: agents.AgentUnavailable,
					Work: &agents.Work{CommandID: command.ID, ActionName: "capture"}},
					ObservedAt: result.CreatedAt.Add(time.Second), LastSeen: time.Now().UTC()}
				if scenario == "expired result" {
					record.ObservedAt = command.CreatedAt
				}
				if err := store.Upsert(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			}
			before := store.Snapshot()
			durable := fakes.NewArtifactExecutions(executions.Execution{Command: command, StartedAt: command.CreatedAt})
			catalog := fakes.NewArtifactCatalog()
			bus := fakes.NewBus()
			consumer := consumers.CommandResultConsumer{Inventory: store, Executions: durable,
				Artifacts: catalog, Bus: bus, State: state.New(t.Context(), time.Minute)}

			if err := consumer.Consume(t.Context(), result); err != nil {
				t.Fatal(err)
			}

			if durable.Snapshot().Result == nil || durable.Snapshot().Result.ID != result.ID || len(catalog.Snapshot()) != 2 {
				t.Fatal("late result or artifacts were lost")
			}
			if !reflect.DeepEqual(before, store.Snapshot()) {
				t.Fatalf("late result changed inventory: %+v", store.Snapshot())
			}
			select {
			case receipt := <-bus.Commands():
				if receipt.Type != messages.ActionResultAccepted || receipt.CorrelationID != command.ID {
					t.Fatalf("unexpected receipt: %+v", receipt)
				}
			default:
				t.Fatal("late durable result did not receive acceptance")
			}
		})
	}
}

func assertNoArtifactReceipt(t *testing.T, bus *fakes.Bus) {
	t.Helper()
	select {
	case message := <-bus.Commands():
		t.Fatalf("unexpected command or result acceptance: %+v", message)
	default:
	}
}
