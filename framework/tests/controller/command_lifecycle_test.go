package controller_test

import (
	"context"
	"errors"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestControllerCloseJoinsCommandCleanupBeforeStoppingCoreEvents(t *testing.T) {
	fixture := fixtures.NewOwnedOperations(t)
	ctx, bus := fixture.Context, fixture.Bus
	cleanupGate := fixtures.NewGate(t)
	defer cleanupGate.Open()
	cleanupError := errors.New("command subscription cleanup failed")
	commandTopic := messages.ControllerCommands(messages.DefaultControllerType)
	cancellationTopic := messages.ControllerCancellations(messages.DefaultControllerType)
	bus.BlockInboxClose(commandTopic, cleanupGate, cleanupError)
	c, err := controller.NewController(ctx, bus, fixture.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupGate.Open()
		closing, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.Close(closing); !errors.Is(err, cleanupError) {
			t.Errorf("controller cleanup: %v", err)
		}
	})
	closing, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	first := make(chan error, 1)

	go func() { first <- c.Close(closing) }()
	fixtures.Receive(t, ctx, cleanupGate.Entered())
	fixtures.Receive(t, ctx, bus.InboxClosed(cancellationTopic))

	if err := fixtures.Receive(t, ctx, first); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close did not bound independent command cleanup: %v", err)
	}
	if bus.CloseCalls() != 0 || ctx.Err() != nil {
		t.Fatalf("core cleanup overtook command intake: events=%d, parent=%v", bus.CloseCalls(), ctx.Err())
	}
	fixtures.Observe(t, ctx, bus, controllerevents.AgentHeartbeat, "")
	retry := make(chan error, 1)

	go func() { retry <- c.Close(ctx) }()
	select {
	case err := <-retry:
		t.Fatalf("retry skipped pending command cleanup: %v", err)
	default:
	}
	cleanupGate.Open()

	if err := fixtures.Receive(t, ctx, retry); !errors.Is(err, cleanupError) {
		t.Fatalf("retry lost the command cleanup failure: %v", err)
	}
	if err := c.Close(ctx); !errors.Is(err, cleanupError) {
		t.Fatalf("later Close changed the retained cleanup result: %v", err)
	}
	if bus.InboxCloseCalls(commandTopic) != 1 || bus.InboxCloseCalls(cancellationTopic) != 1 || bus.CloseCalls() != 1 {
		t.Fatalf("shutdown repeated inbox cleanup: commands=%d, cancellations=%d, events=%d", bus.InboxCloseCalls(commandTopic), bus.InboxCloseCalls(cancellationTopic), bus.CloseCalls())
	}
}
