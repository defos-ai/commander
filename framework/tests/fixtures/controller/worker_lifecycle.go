package fixtures

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/controller/workers/coordination"
	"github.com/defos-ai/commander/framework/controller/workers/event"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

type EventLifecycle struct {
	Context      context.Context
	Events       *event.Worker
	Coordination *coordination.Worker
	Bus          *fakes.Bus
	CleanupGate  *fakes.Gate
	eventsDone   <-chan struct{}
}

func NewEventLifecycle(t *testing.T, cleanupError error) *EventLifecycle {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	parent, stop := context.WithCancelCause(ctx)
	t.Cleanup(func() { stop(context.Canceled) })
	f := &EventLifecycle{Context: parent, Bus: fakes.NewBus(), CleanupGate: NewGate(t)}
	f.Bus.BlockClose(f.CleanupGate, cleanupError)
	inventory := &fakes.Inventory{}
	var err error
	f.Coordination, err = coordination.New(parent, coordination.Dependencies{
		Bus: f.Bus, Inventory: inventory,
		Executions: fakes.UnusedExecutions{}, Artifacts: fakes.UnusedArtifacts{},
		HeartbeatTimeout: time.Hour, SweepInterval: time.Hour, Stop: stop,
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinationDone := make(chan error, 1)
	go func() { coordinationDone <- f.Coordination.Run(parent) }()
	t.Cleanup(func() {
		f.CleanupGate.Open()
		closing, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if f.Events != nil {
			if err := f.Events.Close(closing); !errors.Is(err, cleanupError) {
				t.Errorf("event worker cleanup: %v", err)
			}
		}
		if f.eventsDone != nil {
			select {
			case <-f.eventsDone:
			case <-closing.Done():
				t.Errorf("event run cleanup: %v", closing.Err())
			}
		}
		if err := f.Coordination.Close(closing); err != nil {
			t.Errorf("coordination cleanup: %v", err)
		}
		select {
		case err := <-coordinationDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("coordination run: %v", err)
			}
		case <-closing.Done():
			t.Errorf("coordination run cleanup: %v", closing.Err())
		}
	})
	f.Events, err = event.New(parent, event.Dependencies{
		Bus: f.Bus, ControllerType: "default", ControllerID: "default-controller-00000001",
		Inventory: inventory, Actions: fakes.UnusedActions{}, Executions: fakes.UnusedExecutions{},
		Artifacts: fakes.UnusedArtifacts{}, Coordination: f.Coordination,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *EventLifecycle) StartEvents(ctx context.Context) <-chan error {
	done := make(chan struct{})
	result := make(chan error, 1)
	f.eventsDone = done
	go func() {
		defer close(done)
		result <- f.Events.Run(ctx)
	}()
	return result
}
