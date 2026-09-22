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
	"commander/framework/comms"
	agentevents "commander/framework/comms/clients/agent/events"
	"commander/framework/comms/messages"
	"commander/framework/controller"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/executions"
	artifactproviders "commander/providers/artifact"
	commsproviders "commander/providers/comms"
	controllerproviders "commander/providers/controller"
)

var errUnavailableDatabase = errors.New("fixture database unavailable")

func TestAgentObservationRequiresDurableInventoryBeforeAcknowledgement(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	f.markAgent(t, agents.AgentIdle)
	f.inventory.failUpsert.Store(true)
	message, err := messages.NewMessage(messages.AgentHeartbeat, recoveryAgentID, agents.AgentSnapshot{
		ID: recoveryAgentID, Type: "fixture", Status: agents.AgentReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.controller.WaitForAgent(t.Context(), recoveryAgentID)
		done <- err
	}()

	// Act
	f.publish(t, message)
	err = deliveryWait(t, done)
	record, readErr := f.database.AgentStore.Get(t.Context(), recoveryAgentID)

	// Assert
	if !errors.Is(err, errUnavailableDatabase) {
		t.Fatalf("unsaved observation must fail waiting callers: %v", err)
	}
	if readErr != nil || record.Status != agents.AgentIdle {
		t.Fatalf("failed observation changed inventory: %+v, %v", record, readErr)
	}
	if f.bus.wasAcked(message.ID) {
		t.Fatal("unsaved observation was acknowledged")
	}
}

func TestMalformedAgentObservationDoesNotBlockValidMessages(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	invalid, err := messages.NewMessage(messages.AgentHeartbeat, recoveryAgentID, agents.AgentSnapshot{
		ID: recoveryAgentID, Type: "", Status: agents.AgentReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := messages.NewMessage(messages.AgentHeartbeat, recoveryAgentID, agents.AgentSnapshot{
		ID: recoveryAgentID, Type: "fixture", Status: agents.AgentReady,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	f.publish(t, invalid)
	f.publish(t, valid)
	f.waitAck(t, valid.ID)
	record, err := f.controller.Agent(t.Context(), recoveryAgentID)

	// Assert
	if f.bus.wasAcked(invalid.ID) {
		t.Fatal("malformed observation was acknowledged")
	}
	if err != nil || record.Type != "fixture" || record.Status != agents.AgentReady {
		t.Fatalf("valid observation was not applied: %+v, %v", record, err)
	}
}

func TestLifecyclePreservesIdentityAndPayloadValidation(t *testing.T) {
	for _, scenario := range []string{"mismatched current type", "unknown field", "removed name field"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			f := newDeliveryFixture(t)
			const agentID = "web-quality-agent-1234abcd"
			payload := map[string]any{"id": agentID, "type": "web-quality", "status": agents.AgentReady}
			if scenario == "mismatched current type" {
				payload["type"] = "other"
			} else if scenario == "removed name field" {
				payload["name"] = "retired-name"
			} else {
				payload["unrecognized_field"] = true
			}
			invalid, err := messages.NewMessage(messages.AgentHeartbeat, agentID, payload)
			if err != nil {
				t.Fatal(err)
			}
			barrier, err := messages.NewMessage(messages.AgentHeartbeat, recoveryAgentID, agents.AgentSnapshot{
				ID: recoveryAgentID, Type: "fixture", Status: agents.AgentReady,
			})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			f.publish(t, invalid)
			f.publish(t, barrier)
			f.waitAck(t, barrier.ID)
			_, readErr := f.controller.Agent(t.Context(), agentID)

			// Assert
			if f.bus.wasAcked(invalid.ID) || !errors.Is(readErr, agentmodel.ErrUnknownAgent) {
				t.Fatalf("invalid snapshot was accepted: ack=%v read=%v", f.bus.wasAcked(invalid.ID), readErr)
			}
		})
	}
}

func TestActionCommandRequiresDurableClaimBeforeDispatch(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	f.markAgent(t, agents.AgentReady)
	f.journal.failClaim.Store(true)
	call, _ := recoveryCommand(t)

	// Act
	_, err := f.controller.RunAction(t.Context(), recoveryAgentID, call)

	// Assert
	if !errors.Is(err, errUnavailableDatabase) {
		t.Fatalf("claim failure = %v", err)
	}
	if f.bus.published.Load() != 0 {
		t.Fatal("command was dispatched without a durable claim")
	}
}

func TestActionResultRequiresDurableCompletionBeforeSuccessOrAcknowledgement(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	f.markAgent(t, agents.AgentReady)
	f.journal.failComplete.Store(true)
	call, _ := recoveryCommand(t)
	done := make(chan error, 1)
	go func() {
		_, err := f.controller.RunAction(t.Context(), recoveryAgentID, call)
		done <- err
	}()
	command := f.nextCommand(t)
	result := recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"ok":true}`)})

	// Act
	f.publish(t, result)
	err := deliveryWait(t, done)
	record, readErr := f.journal.Get(t.Context(), recoveryAgentID, command.ID)

	// Assert
	if !errors.Is(err, errUnavailableDatabase) || !errors.Is(err, executions.ErrIndeterminate) {
		t.Fatalf("unsaved result must not report success: %v", err)
	}
	if readErr != nil || record.Result != nil {
		t.Fatalf("failed completion changed database: %+v, %v", record, readErr)
	}
	if f.bus.receipts.Load() != 0 || f.bus.wasAcked(result.ID) {
		t.Fatal("unsaved result was acknowledged")
	}
}

func TestActionFailureIsPersistedAndAcknowledgedAsHandled(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	f.markAgent(t, agents.AgentReady)
	call, _ := recoveryCommand(t)
	done := make(chan error, 1)
	go func() {
		_, err := f.controller.RunAction(t.Context(), recoveryAgentID, call)
		done <- err
	}()
	command := f.nextCommand(t)
	result := recoveryReply(t, command, messages.CommandFailed, agents.CommandError{
		ErrorType: "permission_denied", Message: "missing permission: network-access",
	})

	// Act
	f.publish(t, result)
	err := deliveryWait(t, done)
	f.waitAck(t, result.ID)
	record, readErr := f.journal.Get(t.Context(), recoveryAgentID, command.ID)

	// Assert
	var failure *controller.CommandFailure
	if !errors.As(err, &failure) || failure.CommandID != command.ID || failure.Detail.ErrorType != "permission_denied" {
		t.Fatalf("action failure was not reported to its caller: %v", err)
	}
	if readErr != nil || record.Result == nil || record.Result.ID != result.ID {
		t.Fatalf("acknowledged failure was not persisted: %+v, %v", record, readErr)
	}
	if f.bus.receipts.Load() != 1 {
		t.Fatal("persisted failure did not receive an acceptance receipt")
	}
}

func TestDiscoveryFailureIsAcknowledgedWithoutStoppingController(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	f.markAgent(t, agents.AgentReady)
	done := make(chan error, 1)
	go func() {
		_, err := f.controller.DiscoverActions(t.Context(), recoveryAgentID, nil)
		done <- err
	}()
	command := f.nextCommand(t)
	result := recoveryReply(t, command, messages.CommandFailed, agents.CommandError{
		ErrorType: "discovery_failed", Message: "action discovery is unavailable",
	})
	heartbeat, err := messages.NewMessage(messages.AgentHeartbeat, recoveryAgentID, agents.AgentSnapshot{
		ID: recoveryAgentID, Type: "fixture", Status: agents.AgentReady,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	f.publish(t, result)
	err = deliveryWait(t, done)
	f.waitAck(t, result.ID)
	f.publish(t, heartbeat)
	f.waitAck(t, heartbeat.ID)

	// Assert
	var failure *controller.CommandFailure
	if !errors.As(err, &failure) || failure.CommandID != command.ID || failure.Detail.ErrorType != "discovery_failed" {
		t.Fatalf("discovery failure was not reported to its caller: %v", err)
	}
	if !f.bus.wasAcked(result.ID) || !f.bus.wasAcked(heartbeat.ID) {
		t.Fatal("handled command failure interrupted event consumption")
	}
}

func TestRestartAcceptsClaimedResultWithoutInventoryAndReplaysReceipt(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	call, command := recoveryCommand(t)
	if _, claimed, err := f.journal.Claim(t.Context(), command); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	f.restart(t)
	result := recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"ok":true}`)})

	// Act
	f.publish(t, result)
	firstReceipt := f.nextReceipt(t)
	output, err := f.controller.RecoverAction(t.Context(), recoveryAgentID, call)
	f.markAgent(t, agents.AgentWorking)
	before, _ := f.controller.Agent(t.Context(), recoveryAgentID)
	f.publish(t, result)
	secondReceipt := f.nextReceipt(t)
	after, inventoryErr := f.controller.Agent(t.Context(), recoveryAgentID)

	// Assert
	if err != nil || output.ActionName != call.ActionName {
		t.Fatalf("recover new result without inventory = %+v, %v", output, err)
	}
	for _, receipt := range []messages.Message{firstReceipt, secondReceipt} {
		var detail agentevents.ResultReceipt
		if err := agents.Decode(receipt.Payload, &detail); err != nil || receipt.Type != messages.ActionResultAccepted || receipt.CorrelationID != command.ID || receipt.Work != command.Work || detail.ResultID != result.ID {
			t.Fatalf("receipt = %+v, %+v, %v", receipt, detail, err)
		}
	}
	if inventoryErr != nil || after.Status != agents.AgentWorking || !after.LastSeen.Equal(before.LastSeen) {
		t.Fatalf("duplicate result changed a newer agent observation: before=%+v after=%+v err=%v", before, after, inventoryErr)
	}
}

func TestUntrackedAndMismatchedActionResultsAreNotAcknowledged(t *testing.T) {
	for _, scenario := range []string{"untracked", "wrong agent", "wrong action", "wrong work", "malformed artifact", "conflicting completion"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			f := newDeliveryFixture(t)
			call, command := recoveryCommand(t)
			if scenario != "untracked" {
				if _, _, err := f.journal.Claim(t.Context(), command); err != nil {
					t.Fatal(err)
				}
			}
			output := agents.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"ok":true}`)}
			if scenario == "wrong action" {
				output.ActionName = "other_action"
			}
			result := recoveryReply(t, command, messages.ActionSucceeded, output)
			switch scenario {
			case "wrong agent":
				result.AgentID = "other-agent-a9107c60"
			case "wrong work":
				result.Work.StepID = "other_step"
			case "malformed artifact":
				result.Payload = []byte(`{"action_name":"fixture","output":{},"artifacts":[{"id":"invalid"}]}`)
			case "conflicting completion":
				first := recoveryReply(t, command, messages.CommandFailed, agents.CommandError{ErrorType: "action_error", Message: "original failure"})
				if err := f.journal.Complete(t.Context(), first); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			f.publish(t, result)
			// A heartbeat after the rejected result provides a FIFO processing
			// barrier without asserting private event-handler structure.
			barrier, err := messages.NewMessage(messages.AgentHeartbeat, recoveryAgentID, agents.AgentSnapshot{
				ID: recoveryAgentID, Type: "fixture", Status: agents.AgentWorking,
			})
			if err != nil {
				t.Fatal(err)
			}
			f.publish(t, barrier)
			f.waitAck(t, barrier.ID)
			record, readErr := f.journal.Get(t.Context(), recoveryAgentID, command.ID)

			// Assert
			if f.bus.wasAcked(result.ID) || f.bus.receipts.Load() != 0 {
				t.Fatal("invalid result was acknowledged")
			}
			if scenario == "untracked" {
				if !errors.Is(readErr, executions.ErrUnknownExecution) {
					t.Fatalf("untracked result created execution: %+v, %v", record, readErr)
				}
			} else if readErr != nil || (scenario != "conflicting completion" && record.Result != nil) || (scenario == "conflicting completion" && record.Result.Type != messages.CommandFailed) {
				t.Fatalf("invalid result changed outcome: %+v, %v", record, readErr)
			}
		})
	}
}

func TestRepeatedTaskReusesDurableOutcomeWithoutDispatch(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	call, command := recoveryCommand(t)
	if _, _, err := f.journal.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	result := recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"ok":true}`)})
	if err := f.journal.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}

	// Act
	output, err := f.controller.RunAction(t.Context(), recoveryAgentID, call)
	call.Input = []byte(`{"value":2}`)
	_, conflictErr := f.controller.RunAction(t.Context(), recoveryAgentID, call)

	// Assert
	if err != nil || output.ActionName != call.ActionName || f.bus.published.Load() != 0 {
		t.Fatalf("repeat must reuse saved outcome: %+v, %v, publishes=%d", output, err, f.bus.published.Load())
	}
	if !errors.Is(conflictErr, executions.ErrExecutionConflict) {
		t.Fatalf("changed task request = %v", conflictErr)
	}
}

func TestRepeatedTaskWaitsForOriginalExecutionWithoutDispatch(t *testing.T) {
	// Arrange
	f := newDeliveryFixture(t)
	f.markAgent(t, agents.AgentWorking)
	call, command := recoveryCommand(t)
	if _, _, err := f.journal.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)

	// Act
	go func() {
		_, err := f.controller.RunAction(ctx, recoveryAgentID, call)
		done <- err
	}()
	result := recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"ok":true}`)})
	f.publish(t, result)
	receipt := f.nextReceipt(t)
	err := deliveryWait(t, done)

	// Assert
	if err != nil {
		t.Fatalf("observe original execution: %v", err)
	}
	if receipt.Type != messages.ActionResultAccepted || f.bus.published.Load() != 1 {
		t.Fatalf("expected only a result receipt, got %+v (publishes=%d)", receipt, f.bus.published.Load())
	}
}

type deliveryFixture struct {
	controller *controller.Controller
	bus        *deliveryBus
	journal    *faultExecutionStore
	inventory  *faultInventoryStore
	actions    *faultActionStore
	database   *controllerproviders.Database
	storage    *artifactproviders.Storage
	commands   comms.Subscription
	receipts   comms.Subscription
}

type faultExecutionStore struct {
	executions.ExecutionStore
	failClaim    atomic.Bool
	failComplete atomic.Bool
}

type faultInventoryStore struct {
	agentmodel.AgentStore
	failUpsert atomic.Bool
}

type faultActionStore struct {
	actionmodel.ActionStore
	failUpsert atomic.Bool
}

func (s *faultActionStore) Upsert(ctx context.Context, entry actionmodel.Action) error {
	if s.failUpsert.Load() {
		return errUnavailableDatabase
	}
	return s.ActionStore.Upsert(ctx, entry)
}

func (s *faultInventoryStore) Upsert(ctx context.Context, record agentmodel.Agent) error {
	if s.failUpsert.Load() {
		return errUnavailableDatabase
	}
	return s.AgentStore.Upsert(ctx, record)
}

func (s *faultExecutionStore) Claim(ctx context.Context, command messages.Message) (executions.Execution, bool, error) {
	if s.failClaim.Load() {
		return executions.Execution{}, false, errUnavailableDatabase
	}
	return s.ExecutionStore.Claim(ctx, command)
}

func (s *faultExecutionStore) Complete(ctx context.Context, result messages.Message) error {
	if s.failComplete.Load() {
		return errUnavailableDatabase
	}
	return s.ExecutionStore.Complete(ctx, result)
}

type deliveryBus struct {
	comms.Bus
	failReceipts atomic.Bool
	published    atomic.Int64
	receipts     atomic.Int64
	mu           sync.Mutex
	acked        map[string]bool
	ack          chan string
}

func (b *deliveryBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	b.published.Add(1)
	if message.Type == messages.ActionResultAccepted {
		if b.failReceipts.Load() {
			return errors.New("fixture receipt publication failed")
		}
		b.receipts.Add(1)
	}
	return b.Bus.Publish(ctx, topic, message)
}

func (b *deliveryBus) Subscribe(ctx context.Context, topic string) (comms.Subscription, error) {
	subscription, err := b.Bus.Subscribe(ctx, topic)
	if err != nil {
		return nil, err
	}
	return &deliverySubscription{Subscription: subscription, bus: b}, nil
}

func (b *deliveryBus) wasAcked(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acked[id]
}

type deliverySubscription struct {
	comms.Subscription
	bus *deliveryBus
}

func (s *deliverySubscription) Ack(ctx context.Context, id string) error {
	if err := s.Subscription.Ack(ctx, id); err != nil {
		return err
	}
	s.bus.mu.Lock()
	s.bus.acked[id] = true
	s.bus.mu.Unlock()
	s.bus.ack <- id
	return nil
}

func newDeliveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	root := t.TempDir()
	bus, err := commsproviders.OpenBus(t.Context(), filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
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
	commands, err := bus.Subscribe(t.Context(), messages.AgentCommands(recoveryAgentID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = commands.Close() })
	receipts, err := bus.Subscribe(t.Context(), messages.AgentReceipts(recoveryAgentID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receipts.Close() })
	f := &deliveryFixture{
		bus:     &deliveryBus{Bus: bus, acked: make(map[string]bool), ack: make(chan string, 32)},
		journal: &faultExecutionStore{ExecutionStore: database.ExecutionStore}, database: database, storage: storage, commands: commands, receipts: receipts,
		inventory: &faultInventoryStore{AgentStore: database.AgentStore},
		actions:   &faultActionStore{ActionStore: database.ActionStore},
	}
	f.start(t)
	t.Cleanup(func() { f.close(t) })
	return f
}

func (f *deliveryFixture) start(t *testing.T) {
	t.Helper()
	c, err := controller.NewController(t.Context(), f.bus, controller.ControllerConfig{
		ArtifactStore: f.storage.ArtifactStore, AgentStore: f.inventory, ActionStore: f.actions, ExecutionStore: f.journal,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.controller = c
}

func (f *deliveryFixture) close(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.controller.Close(ctx); err != nil {
		t.Error(err)
	}
}

func (f *deliveryFixture) restart(t *testing.T) {
	t.Helper()
	f.close(t)
	f.start(t)
}

func (f *deliveryFixture) markAgent(t *testing.T, status agents.AgentStatus) {
	t.Helper()
	now := time.Now().UTC()
	if err := f.database.AgentStore.Upsert(t.Context(), agentmodel.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: recoveryAgentID, Type: "fixture", Status: status},
		LastSeen:      now, LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *deliveryFixture) publish(t *testing.T, message messages.Message) {
	t.Helper()
	if err := f.bus.Bus.Publish(t.Context(), messages.ControllerEvents(f.controller.Type()), message); err != nil {
		t.Fatal(err)
	}
}

func (f *deliveryFixture) nextCommand(t *testing.T) messages.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	message, err := f.commands.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func (f *deliveryFixture) nextReceipt(t *testing.T) messages.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	message, err := f.receipts.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func (f *deliveryFixture) waitAck(t *testing.T, id string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for !f.bus.wasAcked(id) {
		select {
		case <-f.bus.ack:
		case <-deadline.C:
			t.Fatal("controller did not acknowledge barrier")
		}
	}
}

func deliveryWait(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("command did not finish")
		return nil
	}
}

func TestQueuedDispatchPublishesOriginalReservationAfterRestart(t *testing.T) {
	// Arrange: the database reservation survived a crash before broker publication.
	f := newDeliveryFixture(t)
	f.markAgent(t, agents.AgentReady)
	call, command := recoveryCommand(t)
	if _, _, err := f.journal.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)

	// Act: queued dispatch redelivers only to the original agent process.
	go func() { _, err := f.controller.DispatchAction(ctx, recoveryAgentID, call); done <- err }()
	actual := f.nextCommand(t)
	f.publish(t, recoveryReply(t, actual, messages.ActionSucceeded, agents.ActionOutput{ActionName: call.ActionName, Output: []byte(`{}`)}))
	if err := deliveryWait(t, done); err != nil {
		t.Fatal(err)
	}

	// Assert: identity, payload and creation time are identical, including across restart.
	if actual.ID != command.ID || actual.AgentID != command.AgentID || !actual.CreatedAt.Equal(command.CreatedAt) || string(actual.Payload) != string(command.Payload) {
		t.Fatalf("reservation changed: before=%+v after=%+v", command, actual)
	}
}
