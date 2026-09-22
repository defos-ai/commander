package agents_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agents "commander/framework/agent"
	"commander/framework/agent/action"
	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/comms"
	agentevents "commander/framework/comms/clients/agent/events"
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	artifactproviders "commander/providers/artifact"
	commsproviders "commander/providers/comms"
)

var (
	errResultPublication  = errors.New("fixture temporary result publication failure")
	errCommandAck         = errors.New("fixture command acknowledgement failure")
	errArtifactWrite      = errors.New("fixture artifact write failure")
	errStoppedPublication = errors.New("fixture stopped event publication failure")
)

func TestCommandForAnotherAgentIsIgnoredAndConsumptionContinues(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, false)
	command, err := messages.NewMessage(messages.RunAction, "other-agent-00000001",
		agents.ActionCall{ActionName: "fixture", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	f.publish(t, command)
	f.waitAck(t, command.ID)
	f.commandBarrier(t)

	// Assert
	if f.calls.Load() != 0 || f.bus.resultAttempts.Load() != 0 {
		t.Fatalf("another agent's command must not execute or produce an outcome: calls=%d outcomes=%d", f.calls.Load(), f.bus.resultAttempts.Load())
	}
	if acknowledged := f.bus.acknowledged(); len(acknowledged) != 2 || acknowledged[0] != command.ID {
		t.Fatalf("ignored delivery and subsequent discovery must both complete: %v", acknowledged)
	}
}

func TestUnknownCommandStopsConsumptionWithoutAcknowledgement(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, false)
	command, err := messages.NewMessage("fixture.unsupported", f.agent.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	f.publish(t, command)
	err = f.waitService(t)

	// Assert
	if err == nil || !strings.Contains(err.Error(), string(command.Type)) {
		t.Fatalf("unsupported command must return a service error identifying its type: %v", err)
	}
	if acknowledged := f.bus.acknowledged(); len(acknowledged) != 0 {
		t.Fatalf("unsupported command must remain unacknowledged: %v", acknowledged)
	}
	if f.calls.Load() != 0 || f.bus.resultAttempts.Load() != 0 {
		t.Fatalf("unsupported command must not execute or publish a handled outcome: calls=%d outcomes=%d", f.calls.Load(), f.bus.resultAttempts.Load())
	}
}

func TestResultPublicationRetryAcknowledgesCommandAfterAcceptance(t *testing.T) {
	// Arrange
	f := newResultFixture(t, true, false)
	command := f.actionCommand(t)

	// Act
	f.publish(t, command)
	initial := f.nextAttempt(t)
	retried := f.receive(t, messages.ActionSucceeded, command.ID)
	receipt := f.receipt(t, retried)
	f.publish(t, receipt)
	f.waitAck(t, receipt.ID)
	f.waitAck(t, command.ID)

	// Assert
	if !reflect.DeepEqual(initial, retried) || f.bus.resultAttempts.Load() < 2 {
		t.Fatalf("retry must preserve the original result: initial=%+v retried=%+v", initial, retried)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("publication retry repeated action execution: %d calls", f.calls.Load())
	}
	acknowledged := f.bus.acknowledged()
	if len(acknowledged) != 2 || acknowledged[0] != receipt.ID || acknowledged[1] != command.ID {
		t.Fatalf("confirmed result acceptance must allow its waiting command to finish: %v", acknowledged)
	}
	var output agents.ActionOutput
	if err := agents.Decode(retried.Payload, &output); err != nil || output.OutputArtifact == nil {
		t.Fatalf("retry lost persisted output: %+v, %v", output, err)
	}
	data, err := output.ResolveOutput(t.Context(), f.artifacts)
	if err != nil || string(data) != `{"ok":true}` {
		t.Fatalf("retry output artifact = %s, %v", data, err)
	}
}

func TestInvalidResultReceiptCannotCompletePendingOriginalCommand(t *testing.T) {
	// Arrange
	f := newResultFixture(t, true, false)
	command := f.actionCommand(t)
	f.publish(t, command)
	result := f.receive(t, messages.ActionSucceeded, command.ID)
	invalid := f.receipt(t, result)
	invalid.Payload = []byte(`{"result_id":"unrelated-result"}`)
	valid := f.receipt(t, result)

	// Act
	f.publish(t, invalid)
	f.waitAck(t, invalid.ID)
	afterInvalid := f.bus.acknowledged()
	f.publish(t, valid)
	f.waitAck(t, valid.ID)
	f.waitAck(t, command.ID)

	// Assert
	if len(afterInvalid) != 1 || afterInvalid[0] != invalid.ID {
		t.Fatalf("invalid receipt must leave its original command pending: %v", afterInvalid)
	}
	acknowledged := f.bus.acknowledged()
	if len(acknowledged) != 3 || acknowledged[0] != invalid.ID || acknowledged[1] != valid.ID || acknowledged[2] != command.ID {
		t.Fatalf("only acknowledged valid acceptance must release the waiting command: %v", acknowledged)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("receipt validation or completion repeated action execution: %d calls", f.calls.Load())
	}
}

func TestUnrelatedReceiptAfterAcceptanceIsIgnored(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, false)
	command := f.actionCommand(t)
	f.publish(t, command)
	result := f.receive(t, messages.ActionSucceeded, command.ID)
	f.waitAck(t, command.ID)
	valid := f.receipt(t, result)
	f.publish(t, valid)
	f.waitAck(t, valid.ID)
	unrelated := f.receipt(t, result)
	unrelated.AgentID = "other-agent-00000001"
	unrelated.CorrelationID = "00000000-0000-4000-8000-000000000002"

	// Act
	f.publish(t, unrelated)
	f.waitAck(t, unrelated.ID)
	f.commandBarrier(t)

	// Assert
	if f.calls.Load() != 1 {
		t.Fatalf("ignoring an unrelated receipt must not repeat action execution: %d calls", f.calls.Load())
	}
	acknowledged := f.bus.acknowledged()
	if len(acknowledged) != 4 || acknowledged[2] != unrelated.ID {
		t.Fatalf("unrelated receipt and subsequent discovery must complete after the accepted result: %v", acknowledged)
	}
}

func TestAcceptedCommandReplayRetriesFailedPublicationWithoutRepeatingAction(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, false)
	command := f.actionCommand(t)
	f.publish(t, command)
	original := f.receive(t, messages.ActionSucceeded, command.ID)
	receipt := f.receipt(t, original)
	f.publish(t, receipt)
	f.waitAck(t, receipt.ID)
	stop, err := messages.NewMessage(messages.StopAgent, f.agent.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Graceful stop proves receipt handling marked the outcome accepted before
	// replay. Restart the same object to retain its execution cache.
	f.publish(t, stop)
	f.receive(t, messages.AgentStopped, stop.ID)
	if err := f.waitService(t); err != nil {
		t.Fatal(err)
	}
	failedAttempt := f.bus.resultAttempts.Load() + 1
	f.bus.failResultAttempt.Store(failedAttempt)
	f.restart(t)
	previousAcks := len(f.bus.acknowledged())

	// Act
	f.publish(t, command)
	retried := f.receive(t, messages.ActionSucceeded, command.ID)
	replayedReceipt := f.receipt(t, retried)
	f.publish(t, replayedReceipt)
	f.waitAck(t, replayedReceipt.ID)
	f.waitAck(t, command.ID)

	// Assert
	if !reflect.DeepEqual(original, retried) || f.bus.resultAttempts.Load() <= failedAttempt {
		t.Fatalf("failed replay publication must retry the same result: original=%+v retried=%+v attempts=%d", original, retried, f.bus.resultAttempts.Load())
	}
	if f.calls.Load() != 1 {
		t.Fatalf("replaying an accepted command repeated action execution: %d calls", f.calls.Load())
	}
	acknowledged := f.bus.acknowledged()[previousAcks:]
	if len(acknowledged) != 2 || acknowledged[0] != replayedReceipt.ID || acknowledged[1] != command.ID {
		t.Fatalf("accepted replay must complete its waiting command: %v", acknowledged)
	}
}

func TestCommandAcknowledgementFailurePreservesCommandAfterResultAcceptance(t *testing.T) {
	// Arrange
	f := newResultFixture(t, true, false)
	command := f.actionCommand(t)
	f.bus.mu.Lock()
	f.bus.failAckID = command.ID
	f.bus.mu.Unlock()
	f.publish(t, command)
	result := f.receive(t, messages.ActionSucceeded, command.ID)
	receipt := f.receipt(t, result)

	// Act
	f.publish(t, receipt)
	err := f.waitService(t)

	// Assert
	if !errors.Is(err, errCommandAck) {
		t.Fatalf("service must expose failed original acknowledgement: %v", err)
	}
	if acknowledged := f.bus.acknowledged(); len(acknowledged) != 1 || acknowledged[0] != receipt.ID {
		t.Fatalf("failed command ACK must retain the command while preserving accepted receipt processing: %v", acknowledged)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("acknowledgement failure repeated action execution: %d calls", f.calls.Load())
	}
}

func TestCancellationWhileAwaitingResultAcceptancePreservesCommand(t *testing.T) {
	// Arrange
	f := newResultFixture(t, true, false)
	command := f.actionCommand(t)
	f.publish(t, command)
	initial := f.nextAttempt(t)
	retried := f.receive(t, messages.ActionSucceeded, command.ID)

	// Act
	f.cancel()
	err := f.waitService(t)

	// Assert
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must release the waiting handler: %v", err)
	}
	if acknowledged := f.bus.acknowledged(); len(acknowledged) != 0 {
		t.Fatalf("cancellation before result acceptance must retain the command: %v", acknowledged)
	}
	if !reflect.DeepEqual(initial, retried) || f.calls.Load() != 1 {
		t.Fatalf("waiting for acceptance must preserve the outcome without repeating execution: initial=%+v retried=%+v calls=%d", initial, retried, f.calls.Load())
	}
}

func TestArtifactPersistenceFailureReceiptPreservesOriginalCommand(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, true)
	command := f.actionCommand(t)

	// Act
	f.publish(t, command)
	result := f.receive(t, messages.CommandFailed, command.ID)
	receipt := f.receipt(t, result)
	f.publish(t, receipt)
	f.waitAck(t, receipt.ID)

	// Assert
	var detail agents.CommandError
	if err := agents.Decode(result.Payload, &detail); err != nil || detail.Message == "" {
		t.Fatalf("artifact failure = %+v, %v", detail, err)
	}
	acknowledged := f.bus.acknowledged()
	if len(acknowledged) != 1 || acknowledged[0] != receipt.ID {
		t.Fatalf("artifact failure must preserve original command after receipt: %v", acknowledged)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("artifact failure repeated action execution: %d calls", f.calls.Load())
	}
}

func TestArtifactPersistenceFailureReplayPreservesOriginalCommand(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, true)
	command := f.actionCommand(t)
	f.publish(t, command)
	original := f.receive(t, messages.CommandFailed, command.ID)
	receipt := f.receipt(t, original)
	f.publish(t, receipt)
	f.waitAck(t, receipt.ID)

	// Act
	f.publish(t, command)
	replayed := f.receive(t, messages.CommandFailed, command.ID)
	f.commandBarrier(t)

	// Assert
	if !reflect.DeepEqual(original, replayed) || f.calls.Load() != 1 {
		t.Fatalf("replay changed the failed outcome or repeated execution: original=%+v replay=%+v calls=%d", original, replayed, f.calls.Load())
	}
	for _, ack := range f.bus.acknowledged() {
		if ack == command.ID {
			t.Fatal("replaying an artifact persistence failure acknowledged the retained command")
		}
	}
}

func TestConflictingCommandPreservesRetainedArtifactFailure(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, true)
	command := f.actionCommand(t)
	f.publish(t, command)
	original := f.receive(t, messages.CommandFailed, command.ID)
	receipt := f.receipt(t, original)
	f.publish(t, receipt)
	f.waitAck(t, receipt.ID)
	conflicting := command
	conflicting.Payload = []byte(`{"action_name":"fixture","input":{"changed":true}}`)

	// Act
	f.publish(t, conflicting)
	f.commandBarrier(t)

	// Assert
	if f.calls.Load() != 1 {
		t.Fatalf("conflicting command repeated execution: %d calls", f.calls.Load())
	}
	for _, ack := range f.bus.acknowledged() {
		if ack == command.ID {
			t.Fatal("rejecting a conflicting command acknowledged the retained original with the same ID")
		}
	}
}

func TestResultReceiptAcknowledgementFailurePreservesWaitingCommand(t *testing.T) {
	// Arrange
	f := newResultFixture(t, true, false)
	command := f.actionCommand(t)
	f.publish(t, command)
	result := f.receive(t, messages.ActionSucceeded, command.ID)
	receipt := f.receipt(t, result)
	f.bus.mu.Lock()
	f.bus.failAckID = receipt.ID
	f.bus.mu.Unlock()

	// Act
	f.publish(t, receipt)
	err := f.waitService(t)

	// Assert
	if !errors.Is(err, errCommandAck) {
		t.Fatalf("service must expose failed receipt acknowledgement: %v", err)
	}
	if acknowledged := f.bus.acknowledged(); len(acknowledged) != 0 {
		t.Fatalf("receipt ACK failure must leave the receipt and its waiting command unacknowledged: %v", acknowledged)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("receipt acknowledgement failure repeated action execution: %d calls", f.calls.Load())
	}
}

func TestStoppedPublicationFailureLeavesStopUnacknowledged(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, false)
	f.bus.failStopped.Store(true)
	stop, err := messages.NewMessage(messages.StopAgent, f.agent.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	f.publish(t, stop)
	err = f.waitService(t)

	// Assert
	if !errors.Is(err, errStoppedPublication) {
		t.Fatalf("service must expose failed stopped announcement: %v", err)
	}
	if acknowledged := f.bus.acknowledged(); len(acknowledged) != 0 {
		t.Fatalf("failed stopped announcement must leave the stop command unacknowledged: %v", acknowledged)
	}
}

func TestStopAcknowledgementFailurePreservesStoppedAnnouncement(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, false)
	stop, err := messages.NewMessage(messages.StopAgent, f.agent.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.bus.mu.Lock()
	f.bus.failAckID = stop.ID
	f.bus.mu.Unlock()

	// Act
	f.publish(t, stop)
	stopped := f.receive(t, messages.AgentStopped, stop.ID)
	err = f.waitService(t)

	// Assert
	if !errors.Is(err, errCommandAck) {
		t.Fatalf("service must expose failed stop acknowledgement: %v", err)
	}
	var description agents.AgentSnapshot
	if err := agents.Decode(stopped.Payload, &description); err != nil || description.Status != agents.AgentStopped {
		t.Fatalf("stop acknowledgement failure must preserve the stopped announcement: %+v, %v", description, err)
	}
	if acknowledged := f.bus.acknowledged(); len(acknowledged) != 0 {
		t.Fatalf("failed stop acknowledgement must leave the command unacknowledged: %v", acknowledged)
	}
}

func TestCancellationDuringStoppedPublicationLeavesStopUnacknowledged(t *testing.T) {
	// Arrange
	f := newResultFixture(t, false, false)
	stop, err := messages.NewMessage(messages.StopAgent, f.agent.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.bus.mu.Lock()
	f.bus.beforeStopped = f.cancel
	f.bus.mu.Unlock()

	// Act
	f.publish(t, stop)
	stopped := f.receive(t, messages.AgentStopped, stop.ID)
	err = f.waitService(t)

	// Assert
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("service must expose parent cancellation during stop completion: %v", err)
	}
	var description agents.AgentSnapshot
	if err := agents.Decode(stopped.Payload, &description); err != nil || description.Status != agents.AgentStopped {
		t.Fatalf("shutdown must still publish the stopped announcement: %+v, %v", description, err)
	}
	if acknowledged := f.bus.acknowledged(); len(acknowledged) != 0 {
		t.Fatalf("parent cancellation must prevent acknowledging the stop command: %v", acknowledged)
	}
}

type resultFixture struct {
	agent     *agents.Agent
	bus       *resultFaultBus
	events    comms.Subscription
	artifacts artifactstore.ArtifactStore
	calls     *atomic.Int64
	cancel    context.CancelFunc
	done      chan error
	finished  bool
}

type resultFaultBus struct {
	comms.Bus
	failFirstResult   bool
	failResultAttempt atomic.Int64
	failStopped       atomic.Bool
	resultAttempts    atomic.Int64
	attempts          chan messages.Message
	mu                sync.Mutex
	failAckID         string
	beforeStopped     func()
	acks              []string
	ack               chan string
}

func (b *resultFaultBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if message.Type == messages.AgentStopped {
		if b.failStopped.Load() {
			return errStoppedPublication
		}
		b.mu.Lock()
		beforeStopped := b.beforeStopped
		b.mu.Unlock()
		if beforeStopped != nil {
			beforeStopped()
		}
	}
	if message.Type == messages.ActionSucceeded || message.Type == messages.CommandFailed {
		attempt := b.resultAttempts.Add(1)
		b.attempts <- message
		if (b.failFirstResult && attempt == 1) || b.failResultAttempt.Load() == attempt {
			return errResultPublication
		}
	}
	return b.Bus.Publish(ctx, topic, message)
}

func (b *resultFaultBus) Subscribe(ctx context.Context, topic string) (comms.Subscription, error) {
	subscription, err := b.Bus.Subscribe(ctx, topic)
	if err != nil {
		return nil, err
	}
	return &resultFaultSubscription{Subscription: subscription, bus: b}, nil
}

func (b *resultFaultBus) acknowledged() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.acks...)
}

type resultFaultSubscription struct {
	comms.Subscription
	bus *resultFaultBus
}

func (s *resultFaultSubscription) Ack(ctx context.Context, id string) error {
	s.bus.mu.Lock()
	fail := s.bus.failAckID == id
	s.bus.mu.Unlock()
	if fail {
		return errCommandAck
	}
	if err := s.Subscription.Ack(ctx, id); err != nil {
		return err
	}
	s.bus.mu.Lock()
	s.bus.acks = append(s.bus.acks, id)
	s.bus.mu.Unlock()
	s.bus.ack <- id
	return nil
}

type resultFaultArtifacts struct {
	artifactstore.ArtifactStore
	fail bool
}

func (s resultFaultArtifacts) Put(ctx context.Context, value artifact.Artifact, owner artifact.Owner) (artifact.Ref, error) {
	if s.fail {
		return artifact.Ref{}, errArtifactWrite
	}
	return s.ArtifactStore.Put(ctx, value, owner)
}

func newResultFixture(t *testing.T, failFirstResult, failArtifacts bool) *resultFixture {
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
	calls := &atomic.Int64{}
	fixtureAction, err := action.NewFunc(action.FuncConfig[struct{}, struct {
		OK bool `json:"ok"`
	}]{
		Action: actionmodel.Action{Name: "fixture"},
		Run: func(context.Context, struct{}) (struct {
			OK bool `json:"ok"`
		}, error) {
			calls.Add(1)
			return struct {
				OK bool `json:"ok"`
			}{OK: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	zeroLimit, heartbeat := 0, time.Duration(0)
	artifacts := resultFaultArtifacts{ArtifactStore: storage.ArtifactStore, fail: failArtifacts}
	agent, err := agents.NewAgent(agents.AgentConfig{
		Actions: []action.Executor{fixtureAction}, ArtifactStore: artifacts, InlineOutputLimit: &zeroLimit, HeartbeatInterval: &heartbeat,
		Server: agents.ServerInfo{IPAddress: "192.0.2.1", Hostname: "fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := bus.Subscribe(t.Context(), messages.ControllerEvents(messages.DefaultControllerType))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	f := &resultFixture{
		agent: agent, bus: &resultFaultBus{Bus: bus, failFirstResult: failFirstResult, attempts: make(chan messages.Message, 32), ack: make(chan string, 32)},
		events: events, artifacts: artifacts, calls: calls, cancel: cancel, done: make(chan error, 1),
	}
	go func() { f.done <- agent.Serve(ctx, f.bus, nil) }()
	t.Cleanup(func() {
		f.cancel()
		if !f.finished {
			if err := f.waitService(t); err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		}
	})
	f.receive(t, messages.AgentStarted, "")
	return f
}

func (f *resultFixture) restart(t *testing.T) {
	t.Helper()
	if !f.finished {
		t.Fatal("stop agent before restarting the same runtime")
	}
	f.cancel()
	ctx, cancel := context.WithCancel(t.Context())
	f.cancel, f.finished = cancel, false
	go func() { f.done <- f.agent.Serve(ctx, f.bus, nil) }()
	f.receive(t, messages.AgentStarted, "")
}

func (f *resultFixture) actionCommand(t *testing.T) messages.Message {
	t.Helper()
	command, err := messages.NewMessage(messages.RunAction, f.agent.ID(), agents.ActionCall{ActionName: "fixture", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func (f *resultFixture) receipt(t *testing.T, result messages.Message) messages.Message {
	t.Helper()
	receipt, err := messages.NewMessage(messages.ActionResultAccepted, f.agent.ID(), agentevents.ResultReceipt{ResultID: result.ID})
	if err != nil {
		t.Fatal(err)
	}
	receipt.CorrelationID, receipt.Work = result.CorrelationID, result.Work
	return receipt
}

func (f *resultFixture) publish(t *testing.T, command messages.Message) {
	t.Helper()
	topic := messages.AgentCommands(f.agent.ID())
	if command.Type == messages.ActionResultAccepted {
		topic = messages.AgentReceipts(f.agent.ID())
	}
	if err := f.bus.Bus.Publish(t.Context(), topic, command); err != nil {
		t.Fatal(err)
	}
}

func (f *resultFixture) commandBarrier(t *testing.T) {
	t.Helper()
	// Command delivery is FIFO. An acknowledged discovery proves that the
	// earlier delivery has finished its handling and acknowledgement decision.
	discovery, err := messages.NewMessage(messages.DiscoverActions, f.agent.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, discovery)
	f.receive(t, messages.AgentUpdated, discovery.ID)
	f.waitAck(t, discovery.ID)
}

func (f *resultFixture) receive(t *testing.T, kind messages.MessageType, correlation string) messages.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		message, err := f.events.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.events.Ack(ctx, message.ID); err != nil {
			t.Fatal(err)
		}
		if message.Type == kind && message.CorrelationID == correlation {
			return message
		}
	}
}

func (f *resultFixture) nextAttempt(t *testing.T) messages.Message {
	t.Helper()
	select {
	case message := <-f.bus.attempts:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not attempt result publication")
		return messages.Message{}
	}
}

func (f *resultFixture) waitAck(t *testing.T, id string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ack := <-f.bus.ack:
			if ack == id {
				return
			}
		case <-deadline.C:
			t.Fatalf("agent did not acknowledge message %s", id)
		}
	}
}

func (f *resultFixture) waitService(t *testing.T) error {
	t.Helper()
	select {
	case err := <-f.done:
		f.finished = true
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("agent service did not finish")
		return nil
	}
}
