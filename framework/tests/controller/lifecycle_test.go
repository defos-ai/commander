package controller_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestControllerCloseWakesCallersAndJoinsWorkersAndCleanup(t *testing.T) {
	cleanupError := errors.New("subscription cleanup failed")
	ctx, c, bus, inventory, cleanupGate := fixtures.StartLifecycleController(t, cleanupError, time.Hour)
	workGate := fixtures.NewGate(t)
	fixtures.Observe(t, ctx, bus, controllerevents.AgentStarted, "")
	pending := fixtures.Discover(ctx, c)
	command := fixtures.Receive(t, ctx, bus.Commands())
	if command.Type != agentcommands.DiscoverActions {
		t.Fatalf("expected a pending discovery command, got %s", command.Type)
	}
	inventory.BlockNextUpsert(workGate)
	fixtures.Send(t, ctx, bus, controllerevents.AgentHeartbeat, "")
	fixtures.Receive(t, ctx, workGate.Entered())
	closing, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	first := make(chan error, 1)

	go func() { first <- c.Close(closing) }()
	closeErr := fixtures.Receive(t, ctx, first)
	callerErr := fixtures.Receive(t, ctx, pending)
	fixtures.Receive(t, ctx, cleanupGate.Entered())

	if !errors.Is(closeErr, context.DeadlineExceeded) || !errors.Is(callerErr, controller.ErrControllerClosed) {
		t.Fatalf("shutdown did not bound Close and release its pending caller: close=%v, caller=%v", closeErr, callerErr)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("shutdown waited for provider cleanup before cancelling coordination")
	}
	if !errors.Is(c.Err(), controller.ErrControllerClosed) {
		t.Fatalf("controller lost its shutdown cause: %v", c.Err())
	}
	cleanupGate.Open()
	fixtures.Receive(t, ctx, bus.Closed())
	workerDeadline, stopWaiting := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stopWaiting()
	second := make(chan error, 1)

	go func() { second <- c.Close(workerDeadline) }()

	if err := fixtures.Receive(t, ctx, second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close returned before its in-flight event worker finished: %v", err)
	}
	workGate.Open()
	joined := make(chan error, 2)

	for range 2 {
		go func() { joined <- c.Close(ctx) }()
	}
	for range 2 {
		if err := fixtures.Receive(t, ctx, joined); !errors.Is(err, cleanupError) {
			t.Fatalf("retry or concurrent Close lost the saved cleanup error: %v", err)
		}
	}

	if bus.CloseCalls() != 1 || !errors.Is(c.Err(), controller.ErrControllerClosed) {
		t.Fatalf("shutdown repeated cleanup or changed its cause: calls=%d, cause=%v", bus.CloseCalls(), c.Err())
	}
	select {
	case ack := <-bus.Acknowledged():
		t.Fatalf("cancelled event was acknowledged after shutdown: %s", ack)
	default:
	}
}

func TestControllerFatalWorkerFailureStopsAdmissionAndPreservesCause(t *testing.T) {
	for _, test := range []struct {
		name          string
		cause         error
		observation   bool
		sweepInterval time.Duration
	}{
		{name: "observation", cause: errors.New("agent inventory unavailable"), observation: true, sweepInterval: time.Hour},
		{name: "lease sweep", cause: errors.New("agent lease storage unavailable"), sweepInterval: 10 * time.Millisecond},
		{name: "lease sweep deadline", cause: context.DeadlineExceeded, sweepInterval: 10 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleanupError := errors.New("subscription cleanup failed")
			ctx, c, bus, inventory, cleanupGate := fixtures.StartLifecycleController(t, cleanupError, test.sweepInterval)
			failureGate := fixtures.NewGate(t)
			fixtures.Observe(t, ctx, bus, controllerevents.AgentStarted, "")
			pending := fixtures.Discover(ctx, c)
			if command := fixtures.Receive(t, ctx, bus.Commands()); command.Type != agentcommands.DiscoverActions {
				t.Fatalf("expected a pending discovery command, got %s", command.Type)
			}
			if test.observation {
				inventory.BlockNextUpsert(failureGate)
				inventory.FailNextUpsert(test.cause)
				fixtures.Send(t, ctx, bus, controllerevents.AgentHeartbeat, "")
			} else {
				inventory.BlockNextExpiry(failureGate)
			}
			fixtures.Receive(t, ctx, failureGate.Entered())
			waiting := fixtures.Discover(ctx, c)
			if !test.observation {
				inventory.FailNextExpiry(test.cause)
			}

			failureGate.Open()
			fixtures.Receive(t, ctx, c.Done())
			pendingError := fixtures.Receive(t, ctx, pending)
			waitingError := fixtures.Receive(t, ctx, waiting)
			_, subsequentError := c.DiscoverActions(ctx, fixtures.AgentID, nil)
			fixtures.Receive(t, ctx, cleanupGate.Entered())

			if ctx.Err() != nil || !errors.Is(c.Err(), test.cause) {
				t.Fatalf("controller lost the failure while its caller context was active: %v", c.Err())
			}
			for _, err := range []error{pendingError, waitingError, subsequentError} {
				if !errors.Is(err, test.cause) || !errors.Is(err, controller.ErrControllerClosed) {
					t.Fatalf("fatal failure lost its cause or left a command waiting: %v", err)
				}
			}
			select {
			case command := <-bus.Commands():
				t.Fatalf("a command was published after the fatal failure: %s", command.Type)
			default:
			}
			select {
			case ack := <-bus.Acknowledged():
				t.Fatalf("failed processing was acknowledged: %s", ack)
			default:
			}
			closing, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			defer cancel()

			if err := c.Close(closing); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Close did not bound cleanup after a fatal failure: %v", err)
			}
			cleanupGate.Open()
			if err := c.Close(ctx); !errors.Is(err, cleanupError) {
				t.Fatalf("retrying Close lost the subscription cleanup result: %v", err)
			}

			if bus.CloseCalls() != 1 || !errors.Is(c.Err(), test.cause) {
				t.Fatalf("Close repeated cleanup or replaced the failure: calls=%d, cause=%v", bus.CloseCalls(), c.Err())
			}
		})
	}
}
