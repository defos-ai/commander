package controller_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agents "commander/framework/agent"
	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/comms"
	"commander/framework/comms/messages"
	"commander/framework/controller"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/executions"
	artifactproviders "commander/providers/artifact"
	commsproviders "commander/providers/comms"
	controllerproviders "commander/providers/controller"
)

const recoveryAgentID = "fixture-agent-a5452d64"

func TestRecoveryReadsCompletedResultAndArtifactsAfterAgentDisappears(t *testing.T) {
	// Arrange
	f := newRecoveryFixture(t)
	call, command := recoveryCommand(t)
	f.claim(t, command)
	want := []byte("saved evidence")
	ref, err := f.artifacts.Put(t.Context(), artifact.Artifact{Name: "evidence.txt", Data: want}, artifact.Owner{
		AgentID: recoveryAgentID, ActionName: call.ActionName, CommandID: command.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{
		ActionName: call.ActionName, Output: []byte(`{"ok":true}`), Artifacts: []artifact.Ref{ref},
	})
	f.complete(t, result)

	// Act
	output, err := f.controller.RecoverAction(t.Context(), recoveryAgentID, call)
	_, content, readErr := f.controller.ReadArtifact(t.Context(), ref.ID)

	// Assert
	if err != nil || output.ActionName != call.ActionName || len(output.Artifacts) != 1 {
		t.Fatalf("recover completed output = %+v, %v", output, err)
	}
	if readErr != nil || string(content) != string(want) {
		t.Fatalf("read recovered evidence = %q, %v", content, readErr)
	}
}

func TestRecoveryDistinguishesSavedFailureFromUnknownExecution(t *testing.T) {
	for _, scenario := range []string{"saved failure", "unfinished claim", "missing claim"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			f := newRecoveryFixture(t)
			call, command := recoveryCommand(t)
			if scenario != "missing claim" {
				f.claim(t, command)
			}
			if scenario == "saved failure" {
				f.complete(t, recoveryReply(t, command, messages.CommandFailed, agents.CommandError{
					ErrorType: "action_error", Message: "action returned a failure",
				}))
			}

			// Act
			_, err := f.controller.RecoverAction(t.Context(), recoveryAgentID, call)

			// Assert
			var failure *controller.CommandFailure
			if scenario == "saved failure" {
				if !errors.As(err, &failure) || failure.CommandID != command.ID || errors.Is(err, executions.ErrIndeterminate) {
					t.Fatalf("saved failure must remain definitive: %v", err)
				}
			} else if !errors.Is(err, executions.ErrIndeterminate) || errors.As(err, &failure) {
				t.Fatalf("unknown outcome must remain indeterminate: %v", err)
			}
		})
	}
}

func TestRecoveryRejectsDifferentStoredRequest(t *testing.T) {
	for _, scenario := range []string{"action", "input", "operation"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			f := newRecoveryFixture(t)
			call, command := recoveryCommand(t)
			f.claim(t, command)
			f.complete(t, recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{
				ActionName: call.ActionName, Output: []byte(`{"ok":true}`),
			}))
			switch scenario {
			case "action":
				call.ActionName = "different_action"
			case "input":
				// These integers collapse to the same float64; identity checks
				// must retain their exact JSON numeric values.
				call.Input = []byte(`{"value":9007199254740993}`)
			case "operation":
				call.Work.OperationID = "1b96d60a-eb42-439b-89cc-77e3f2c7c2c9"
			}

			// Act
			_, err := f.controller.RecoverAction(t.Context(), recoveryAgentID, call)

			// Assert
			if !errors.Is(err, executions.ErrIndeterminate) {
				t.Fatalf("different request must not restore a result: %v", err)
			}
		})
	}
}

func TestRecoveryWaitsForDurableCompletionWithoutDispatch(t *testing.T) {
	// Arrange
	f := newRecoveryFixture(t)
	call, command := recoveryCommand(t)
	f.claim(t, command)
	f.markAgentRunning(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)

	// Act
	go func() {
		_, err := f.controller.RecoverAction(ctx, recoveryAgentID, call)
		done <- err
	}()
	select {
	case <-f.journal.firstRead:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	f.complete(t, recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{
		ActionName: call.ActionName, Output: []byte(`{"ok":true}`),
	}))
	err := <-done

	// Assert
	if err != nil {
		t.Fatalf("recover completed running execution: %v", err)
	}
}

func TestRecoveryAllowsSurvivingAgentToAnnounceAfterControllerOutage(t *testing.T) {
	// Arrange
	f := newRecoveryFixture(t)
	call, command := recoveryCommand(t)
	f.claim(t, command)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)

	// Act
	go func() {
		_, err := f.controller.RecoverAction(ctx, recoveryAgentID, call)
		done <- err
	}()
	select {
	case <-f.inventory.firstMissing:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The controller initially has no agent inventory after the outage. The
	// same agent reappears at its next heartbeat and completes the old command.
	f.markAgentRunning(t)
	select {
	case <-f.journal.secondRead:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	f.complete(t, recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{
		ActionName: call.ActionName, Output: []byte(`{"ok":true}`),
	}))
	err := <-done

	// Assert
	if err != nil {
		t.Fatalf("surviving execution must recover during rediscovery: %v", err)
	}
}

func TestRecoveryCancellationSuspendsObservationAndDeadlineIsIndeterminate(t *testing.T) {
	for _, scenario := range []string{"cancellation", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			f := newRecoveryFixture(t)
			call, command := recoveryCommand(t)
			f.claim(t, command)
			f.markAgentRunning(t)
			ctx, cancel := context.WithCancel(t.Context())
			if scenario == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)

			// Act
			go func() {
				_, err := f.controller.RecoverAction(ctx, recoveryAgentID, call)
				done <- err
			}()
			if scenario == "cancellation" {
				select {
				case <-f.journal.firstRead:
				case <-time.After(2 * time.Second):
					t.Fatal("recovery did not read the execution")
				}
				cancel()
			}
			err := <-done

			// Assert
			if scenario == "cancellation" {
				if !errors.Is(err, context.Canceled) || errors.Is(err, executions.ErrIndeterminate) {
					t.Fatalf("shutdown cancellation should suspend recovery: %v", err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, executions.ErrIndeterminate) {
				t.Fatalf("deadline should leave the execution outcome unknown: %v", err)
			}
		})
	}
}

type recoveryFixture struct {
	controller *controller.Controller
	journal    *observedExecutionStore
	artifacts  artifactstore.ArtifactStore
	inventory  *observedInventoryStore
}

type observedBus struct {
	comms.Bus
	published atomic.Int64
}

func (b *observedBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	b.published.Add(1)
	return b.Bus.Publish(ctx, topic, message)
}

type observedExecutionStore struct {
	executions.ExecutionStore
	firstRead  chan struct{}
	secondRead chan struct{}
	readCount  atomic.Int64
}

func (s *observedExecutionStore) Get(ctx context.Context, agentID, commandID string) (executions.Execution, error) {
	record, err := s.ExecutionStore.Get(ctx, agentID, commandID)
	switch s.readCount.Add(1) {
	case 1:
		close(s.firstRead)
	case 2:
		close(s.secondRead)
	}
	return record, err
}

type observedInventoryStore struct {
	agentmodel.AgentStore
	firstMissing chan struct{}
	once         sync.Once
}

func (s *observedInventoryStore) Get(ctx context.Context, agentID string) (agentmodel.Agent, error) {
	record, err := s.AgentStore.Get(ctx, agentID)
	if errors.Is(err, agentmodel.ErrUnknownAgent) {
		s.once.Do(func() { close(s.firstMissing) })
	}
	return record, err
}

func newRecoveryFixture(t *testing.T) recoveryFixture {
	t.Helper()
	root := t.TempDir()
	bus, err := commsproviders.OpenBus(t.Context(), filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	observed := &observedBus{Bus: bus}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
		if count := observed.published.Load(); count != 0 {
			t.Errorf("recovery must never publish commands; published %d", count)
		}
	})
	storage, err := artifactproviders.OpenStorage(t.Context(), filepath.Join(root, "storage"), artifactproviders.StorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	database, err := controllerproviders.OpenDatabase(t.Context(), filepath.Join(root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	journal := &observedExecutionStore{ExecutionStore: database.ExecutionStore, firstRead: make(chan struct{}), secondRead: make(chan struct{})}
	inventoryStore := &observedInventoryStore{AgentStore: database.AgentStore, firstMissing: make(chan struct{})}
	c, err := controller.NewController(t.Context(), observed, controller.ControllerConfig{
		ArtifactStore: storage.ArtifactStore, AgentStore: inventoryStore, ActionStore: database.ActionStore, ExecutionStore: journal,
		HeartbeatTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return recoveryFixture{controller: c, journal: journal, artifacts: storage.ArtifactStore, inventory: inventoryStore}
}

func recoveryCommand(t *testing.T) (agents.ActionCall, messages.Message) {
	t.Helper()
	call := agents.ActionCall{ActionName: "fixture", Input: []byte(`{"value":9007199254740992}`), Work: messages.WorkContext{
		OperationID: "9b87c40f-35e2-4980-a116-3f5970d06988", TaskID: "a7ba3bf2-270f-4050-a49e-d6ba347da9ef", StepID: "inspect",
	}}
	command, err := messages.NewMessage(messages.RunAction, recoveryAgentID, call)
	if err != nil {
		t.Fatal(err)
	}
	command.ID, command.Work = call.Work.TaskID, call.Work
	return call, command
}

func recoveryReply(t *testing.T, command messages.Message, kind messages.MessageType, payload any) messages.Message {
	t.Helper()
	result, err := messages.NewMessage(kind, command.AgentID, payload)
	if err != nil {
		t.Fatal(err)
	}
	result.CorrelationID, result.Work = command.ID, command.Work
	return result
}

func (f recoveryFixture) claim(t *testing.T, command messages.Message) {
	t.Helper()
	if _, claimed, err := f.journal.Claim(t.Context(), command); err != nil || !claimed {
		t.Fatalf("claim fixture execution = %v, %v", claimed, err)
	}
}

func (f recoveryFixture) complete(t *testing.T, result messages.Message) {
	t.Helper()
	if err := f.journal.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
}

func (f recoveryFixture) markAgentRunning(t *testing.T) {
	t.Helper()
	now := time.Now().UTC()
	if err := f.inventory.Upsert(t.Context(), agentmodel.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: recoveryAgentID, Status: agents.AgentWorking},
		LastSeen:      now, LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}
}
