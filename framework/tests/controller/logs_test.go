package controller_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestControllerExecutionLogsPreserveExecutionAndAcceptLateReplays(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "running"
		if completed {
			name = "completed"
		}
		t.Run(name, func(t *testing.T) {
			fixture := fixtures.NewLogConsumer(t)
			if completed {
				fixture.Executions.Change(func(record *executions.Execution) {
					record.Result, record.CompletedAt = &fixture.Result, &fixture.Result.CreatedAt
				})
			}
			before := fixture.Executions.Snapshot()
			message := fixtures.LogMessage(t, fixture.Log)

			for range 2 {
				if err := fixture.Consumer.Consume(t.Context(), message); err != nil {
					t.Fatal(err)
				}
			}

			snapshot := fixture.Executions.LogSnapshot()
			if len(snapshot.Records) != 1 || !snapshot.Records[fixture.Log.ID].Equal(fixture.Log) || snapshot.Appends != 2 || snapshot.Reads != 2 {
				t.Fatalf("log replay changed its identity or skipped readback: %+v", snapshot)
			}
			if remaining := time.Until(snapshot.Deadline); remaining <= 0 || remaining > 5*time.Second {
				t.Fatalf("log processing did not receive a bounded context: %s", remaining)
			}
			if !reflect.DeepEqual(before, fixture.Executions.Snapshot()) {
				t.Fatal("progress log changed the execution outcome or checkpoints")
			}
		})
	}
}

func TestControllerExecutionLogsRequireSavedOwnership(t *testing.T) {
	for _, mismatch := range []string{"agent", "command", "operation", "task", "action", "unknown claim"} {
		t.Run(mismatch, func(t *testing.T) {
			fixture := fixtures.NewLogConsumer(t)
			log := fixture.Log
			switch mismatch {
			case "agent":
				log.AgentID = "fixture-agent-00000002"
				fixture.Executions.IgnoreLookup()
			case "command":
				log.CommandID = id.New()
				fixture.Executions.IgnoreLookup()
			case "operation":
				log.Work.OperationID = id.New()
			case "task":
				log.Work.TaskID = id.New()
			case "action":
				log.ActionName = "other-action"
			case "unknown claim":
				fixture.Executions.FailRead(executions.ErrUnknownExecution)
			}
			message := fixtures.LogMessage(t, log)

			err := fixture.Consumer.Consume(t.Context(), message)

			if !errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("unowned log was accepted: %v", err)
			}
			if snapshot := fixture.Executions.LogSnapshot(); snapshot.Appends != 0 || snapshot.Reads != 0 || len(snapshot.Records) != 0 {
				t.Fatalf("unowned log reached persistence: %+v", snapshot)
			}
		})
	}
}

func TestControllerExecutionLogsKeepEarlierCallerDeadline(t *testing.T) {
	fixture := fixtures.NewLogConsumer(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()

	err := fixture.Consumer.Consume(ctx, fixtures.LogMessage(t, fixture.Log))

	if err != nil || !fixture.Executions.LogSnapshot().Deadline.Equal(deadline) {
		t.Fatalf("log processing extended the caller's deadline: %v", err)
	}
}

func TestControllerExecutionLogsRejectInconsistentEnvelope(t *testing.T) {
	fixture := fixtures.NewLogConsumer(t)
	message := fixtures.LogMessage(t, fixture.Log)
	message.CorrelationID = id.New()

	err := fixture.Consumer.Consume(t.Context(), message)

	if !errors.Is(err, consumers.ErrInvalidMessage) || fixture.Executions.LogSnapshot().Appends != 0 {
		t.Fatalf("mismatched log envelope reached persistence: %v", err)
	}
}

func TestControllerExecutionLogsRequireSuccessfulAppendAndReadback(t *testing.T) {
	for _, failure := range []string{"claim read", "corrupt claim", "append", "uncertain append", "readback", "missing readback", "different readback"} {
		t.Run(failure, func(t *testing.T) {
			fixture := fixtures.NewLogConsumer(t)
			unavailable := errors.New("database unavailable")
			var expected error
			switch failure {
			case "claim read":
				fixture.Executions.FailRead(unavailable)
				expected = unavailable
			case "corrupt claim":
				fixture.Executions.Change(func(record *executions.Execution) {
					record.Command.Payload = fixtures.JSON(t, map[string]any{"unexpected": true})
				})
			case "append":
				fixture.Executions.FailLogAppend(unavailable, false)
				expected = unavailable
			case "uncertain append":
				fixture.Executions.FailLogAppend(unavailable, true)
				expected = unavailable
			case "readback":
				fixture.Executions.FailLogRead(unavailable)
				expected = unavailable
			case "missing readback":
				fixture.Executions.DiscardLogAppends()
				expected = executions.ErrUnknownLog
			case "different readback":
				other := fixture.Log
				other.Message = "another observation"
				fixture.Executions.ReadLogAs(other)
			}
			message := fixtures.LogMessage(t, fixture.Log)

			err := fixture.Consumer.Consume(t.Context(), message)

			if err == nil || errors.Is(err, consumers.ErrInvalidMessage) || expected != nil && !errors.Is(err, expected) {
				t.Fatalf("backend failure must remain a processing failure: %v", err)
			}
			if failure == "uncertain append" {
				snapshot := fixture.Executions.LogSnapshot()
				if len(snapshot.Records) != 1 || snapshot.Reads != 0 {
					t.Fatal("an uncertain append was treated as successful persistence")
				}
			}
		})
	}
}

func TestControllerExecutionLogConflictingReplayIsRejected(t *testing.T) {
	fixture := fixtures.NewLogConsumer(t)
	if err := fixture.Consumer.Consume(t.Context(), fixtures.LogMessage(t, fixture.Log)); err != nil {
		t.Fatal(err)
	}
	conflicting := fixture.Log
	conflicting.Message = "changed observation"

	err := fixture.Consumer.Consume(t.Context(), fixtures.LogMessage(t, conflicting))

	if !errors.Is(err, consumers.ErrInvalidMessage) || !errors.Is(err, executions.ErrLogConflict) {
		t.Fatalf("conflicting log replay was accepted: %v", err)
	}
	if snapshot := fixture.Executions.LogSnapshot(); len(snapshot.Records) != 1 || !snapshot.Records[fixture.Log.ID].Equal(fixture.Log) || snapshot.Reads != 1 {
		t.Fatalf("conflicting replay changed the original log: %+v", snapshot)
	}
}

func TestControllerContinuesAfterRejectedExecutionLog(t *testing.T) {
	for _, failure := range []string{"claim", "append", "timeout", "readback"} {
		t.Run(failure, func(t *testing.T) {
			fixture := fixtures.NewLogConsumer(t)
			unavailable := errors.New("log persistence unavailable")
			switch failure {
			case "claim":
				fixture.Executions.FailRead(unavailable)
			case "append":
				fixture.Executions.FailLogAppend(unavailable, false)
			case "timeout":
				fixture.Executions.FailLogAppend(context.DeadlineExceeded, false)
			case "readback":
				fixture.Executions.FailLogRead(unavailable)
			}
			ctx, c, bus, _ := fixtures.StartLogController(t, fixture.Executions)
			message := fixtures.LogMessage(t, fixture.Log)

			if err := bus.Send(ctx, message); err != nil {
				t.Fatal(err)
			}

			select {
			case rejected := <-bus.Rejected():
				if rejected != message.ID {
					t.Fatalf("rejected another delivery: %s", rejected)
				}
			case <-c.Done():
				t.Fatalf("log processing stopped coordination: %v", c.Err())
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			assertNoLogAck(t, bus)

			fixtures.Observe(t, ctx, bus, controllerevents.AgentStarted, "")
			finished := fixtures.Discover(ctx, c)
			command := fixtures.Receive(t, ctx, bus.Commands())
			fixtures.Observe(t, ctx, bus, controllerevents.AgentUpdated, command.ID)

			if err := fixtures.Receive(t, ctx, finished); err != nil || c.Err() != nil {
				t.Fatalf("controller could not process work after rejecting telemetry: %v, %v", err, c.Err())
			}
		})
	}
}

func TestControllerExecutionLogAckFollowsPersistenceAndReadback(t *testing.T) {
	fixture := fixtures.NewLogConsumer(t)
	ctx, c, bus, inventory := fixtures.StartLogController(t, fixture.Executions)
	appendGate, readGate := fixtures.NewGate(t), fixtures.NewGate(t)
	fixture.Executions.BlockNextLogAppend(appendGate)
	fixture.Executions.BlockNextLogRead(readGate)
	message := fixtures.LogMessage(t, fixture.Log)

	if err := bus.Send(ctx, message); err != nil {
		t.Fatal(err)
	}
	fixtures.Receive(t, ctx, appendGate.Entered())

	assertNoLogAck(t, bus)
	if snapshot := fixture.Executions.LogSnapshot(); len(snapshot.Records) != 0 || snapshot.Reads != 0 {
		t.Fatalf("readback preceded persistence: %+v", snapshot)
	}

	appendGate.Open()
	fixtures.Receive(t, ctx, readGate.Entered())

	assertNoLogAck(t, bus)
	if snapshot := fixture.Executions.LogSnapshot(); len(snapshot.Records) != 1 || snapshot.Reads != 1 {
		t.Fatalf("saved log was not read back before acknowledgement: %+v", snapshot)
	}
	if inventory.Snapshot() != nil {
		t.Fatal("execution log changed agent inventory")
	}

	readGate.Open()
	ack := fixtures.Receive(t, ctx, bus.Acknowledged())

	if ack != message.ID || c.Err() != nil {
		t.Fatalf("persisted log was not acknowledged: %s, %v", ack, c.Err())
	}
	select {
	case response := <-bus.Commands():
		t.Fatalf("one-way log emitted an agent response: %+v", response)
	default:
	}
}

func assertNoLogAck(t *testing.T, bus *fakes.Bus) {
	t.Helper()
	select {
	case id := <-bus.Acknowledged():
		t.Fatalf("log acknowledged before confirmed persistence: %s", id)
	default:
	}
}
