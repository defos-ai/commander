package controller_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/domain/operations"
	runnerfakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestControllerCloseKeepsRoutingUntilAcceptedOperationResultsDrain(t *testing.T) {
	for _, test := range []struct {
		name         string
		cancelParent bool
	}{
		{name: "explicit Close"},
		{name: "parent cancellation", cancelParent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := fixtures.NewOwnedOperations(t)
			ctx, bus := fixture.Context, fixture.Bus
			hostContext, cancelHost := context.WithCancelCause(ctx)
			defer cancelHost(nil)
			hostFixture := fixture
			hostFixture.Context = hostContext
			c := hostFixture.Start(t)
			work := c.Operations()
			op, err := work.Start(ctx, commands.StartOperationCommand{MissionName: "review", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			task, err := work.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			command := fixtures.Receive(t, ctx, bus.Commands())
			if command.Type != agentcommands.RunAction || command.Work.OperationID != op.ID || command.Work.TaskID != task.ID {
				t.Fatalf("controller-owned operation did not dispatch its saved task: %+v", command)
			}
			pending := fixtures.Discover(ctx, c)
			if command := fixtures.Receive(t, ctx, bus.Commands()); command.Type != agentcommands.DiscoverActions {
				t.Fatalf("expected a pending public discovery call, got %s", command.Type)
			}
			result := fixtures.SuccessfulOperationResult(t, command)
			if err := bus.Send(ctx, result); err != nil {
				t.Fatal(err)
			}
			fixtures.Receive(t, ctx, fixture.ResultGate.Entered())
			if ack := fixtures.Receive(t, ctx, bus.Acknowledged()); ack != result.ID {
				t.Fatalf("operation result was not accepted before shutdown: %s", ack)
			}
			if receipt := fixtures.Receive(t, ctx, bus.Commands()); receipt.Type != messages.ActionResultAccepted || receipt.CorrelationID != command.ID {
				t.Fatalf("operation result has no matching persistence receipt: %+v", receipt)
			}
			closing, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			defer cancel()
			closed := make(chan error, 1)

			parentCause := errors.New("controller parent cancelled")
			if test.cancelParent {
				cancelHost(parentCause)
			} else {
				go func() { closed <- c.Close(closing) }()
			}
			fixtures.Receive(t, ctx, c.Done())
			callerError := fixtures.Receive(t, ctx, pending)
			if test.cancelParent {
				if !errors.Is(c.Err(), parentCause) || !errors.Is(callerError, parentCause) {
					t.Fatalf("parent cancellation lost its cause: controller=%v, caller=%v", c.Err(), callerError)
				}
				go func() { closed <- c.Close(closing) }()
			}
			closeError := fixtures.Receive(t, ctx, closed)
			_, discoverError := c.DiscoverActions(ctx, fixtures.AgentID, nil)
			_, assignError := work.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect", Input: []byte(`{}`)})

			if !errors.Is(closeError, context.DeadlineExceeded) || !errors.Is(callerError, controller.ErrControllerClosed) || !errors.Is(discoverError, controller.ErrControllerClosed) || !errors.Is(assignError, operations.ErrClosed) {
				t.Fatalf("shutdown left intake open or failed to bound draining: close=%v, pending=%v, discover=%v, assign=%v", closeError, callerError, discoverError, assignError)
			}
			if bus.CloseCalls() != 0 {
				t.Fatal("subscription cleanup started before accepted operation results drained")
			}
			fixtures.Observe(t, ctx, bus, controllerevents.AgentHeartbeat, "")
			before, err := fixture.Config.OperationStore.Get(ctx, op.ID)
			if err != nil || len(before.Tasks) != 1 || before.Tasks[0].Status != operations.TaskRunning || before.Tasks[0].Result != nil {
				t.Fatalf("result gate failed to retain pending operation persistence: %+v, %v", before, err)
			}
			fixture.ResultGate.Open()
			joined := make(chan error, 2)

			for range 2 {
				go func() { joined <- c.Close(ctx) }()
			}
			for range 2 {
				if err := fixtures.Receive(t, ctx, joined); err != nil {
					t.Fatalf("retrying controller Close did not join its runner: %v", err)
				}
			}
			saved, err := fixture.Config.OperationStore.Get(ctx, op.ID)
			if err != nil || len(saved.Tasks) != 1 || saved.Tasks[0].ID != task.ID || saved.Tasks[0].Status != operations.TaskSucceeded || saved.Tasks[0].Result == nil || !bytes.Equal(saved.Tasks[0].Result.Output, []byte(`{"ok":true}`)) {
				t.Fatalf("controller shutdown lost the late successful result: %+v, %v", saved, err)
			}
			finished := 0
			for _, event := range saved.Events {
				if event.Type == operations.TaskFinished && event.TaskID == task.ID {
					finished++
				}
			}
			if finished != 1 || bus.CloseCalls() != 1 {
				t.Fatalf("shutdown repeated result acceptance or subscription cleanup: results=%d, cleanup=%d", finished, bus.CloseCalls())
			}
		})
	}
}

func TestControllerRecoveryFailureCleansUnstartedWorkersAndPreservesWork(t *testing.T) {
	fixture := fixtures.NewOwnedOperations(t)
	ctx, bus := fixture.Context, fixture.Bus
	saved := fixture.SeedQueued(t)
	failure, cleanupError := errors.New("operation recovery failed"), errors.New("subscription cleanup failed")
	fixture.Config.OperationStore = &runnerfakes.RecoveryFailureStore{OperationStore: fixture.Config.OperationStore, Failure: failure}
	bus.BlockClose(nil, cleanupError)
	fixtures.Send(t, ctx, bus, controllerevents.AgentHeartbeat, "")

	c, startupError := controller.NewController(ctx, bus, fixture.Config)
	retained, readError := fixture.Config.OperationStore.Get(ctx, saved.ID)

	if c != nil || !errors.Is(startupError, failure) || !errors.Is(startupError, cleanupError) || bus.CloseCalls() != 1 || ctx.Err() != nil {
		t.Fatalf("startup did not preserve recovery and cleanup failures: controller=%v, error=%v, cleanup=%d, parent=%v", c, startupError, bus.CloseCalls(), ctx.Err())
	}
	if readError != nil || !reflect.DeepEqual(retained, saved) {
		t.Fatalf("failed startup changed saved operation work: %+v, %v", retained, readError)
	}
	select {
	case command := <-bus.Commands():
		t.Fatalf("failed recovery dispatched work before startup completed: %+v", command)
	case ack := <-bus.Acknowledged():
		t.Fatalf("failed startup accepted an event before recovery completed: %s", ack)
	default:
	}
}
