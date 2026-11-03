package fixtures

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/domain/missions"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	operationfakes "github.com/defos-ai/commander/framework/tests/fakes/operation"
)

func StartLifecycleController(t *testing.T, cleanupError error, sweepInterval time.Duration) (context.Context, *controller.Controller, *fakes.Bus, *fakes.Inventory, *fakes.Gate) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	bus, inventory, gate := fakes.NewBus(), &fakes.Inventory{}, fakes.NewGate()
	bus.BlockClose(gate, cleanupError)
	c, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: inventory, ActionStore: fakes.UnusedActions{}, ArtifactStore: fakes.UnusedArtifacts{},
		MissionStore: operationfakes.NewMissionStore(missions.Mission{}), OperationStore: operationfakes.NewOperationStore(), ReportStore: operationfakes.ReportStore{},
		ExecutionStore: fakes.UnusedExecutions{}, HeartbeatTimeout: time.Hour, LeaseSweepInterval: sweepInterval,
	})
	if err != nil {
		gate.Open()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		gate.Open()
		closing, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := c.Close(closing); !errors.Is(err, cleanupError) {
			t.Errorf("controller cleanup: %v", err)
		}
	})
	return ctx, c, bus, inventory, gate
}
