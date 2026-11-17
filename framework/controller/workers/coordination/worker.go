package coordination

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/controller/leases"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/internal/state"
)

type Dependencies struct {
	Bus              comms.Bus
	Inventory        agents.AgentStore
	Executions       executions.ExecutionStore
	Artifacts        artifacts.ArtifactStore
	HeartbeatTimeout time.Duration
	SweepInterval    time.Duration
	Stop             func(error)
	Logger           *slog.Logger
}

type Worker struct {
	lifecycle      *workers.Lifecycle
	state          *state.State
	requests       chan func()
	bus            comms.Bus
	inventory      agents.AgentStore
	executionStore executions.ExecutionStore
	artifactStore  artifacts.ArtifactStore
	leaseRegistry  *leases.Registry
	sweepInterval  time.Duration
	stop           func(error)
	logger         *slog.Logger
	stopPublished  map[string]time.Time
}

func New(ctx context.Context, deps Dependencies) (*Worker, error) {
	if ctx == nil || deps.Bus == nil || deps.Inventory == nil || deps.Executions == nil || deps.Artifacts == nil || deps.Stop == nil {
		return nil, errors.New("coordination requires a lifetime, stores, bus and shutdown handler")
	}
	if deps.HeartbeatTimeout <= 0 || deps.SweepInterval <= 0 {
		return nil, errors.New("coordination requires positive heartbeat and sweep intervals")
	}
	registry, err := leases.NewRegistry(deps.Inventory, deps.HeartbeatTimeout)
	if err != nil {
		return nil, err
	}
	if _, err := registry.Sweep(ctx, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("expire agent leases: %w", err)
	}
	lifetime := workers.NewLifecycle(ctx)
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Worker{
		lifecycle: lifetime,
		state:     state.New(lifetime.Context(), deps.HeartbeatTimeout), requests: make(chan func()),
		bus: deps.Bus, inventory: deps.Inventory, executionStore: deps.Executions,
		artifactStore: deps.Artifacts, leaseRegistry: registry, sweepInterval: deps.SweepInterval, stop: deps.Stop, logger: deps.Logger,
		stopPublished: make(map[string]time.Time),
	}, nil
}

// Stop fences admission before the owner loop releases failed observations or callers.
func (c *Worker) Stop(err error) { c.stop(err) }

func (c *Worker) Run(ctx context.Context) error {
	return c.lifecycle.Run(ctx, c.run)
}

func (c *Worker) Close(ctx context.Context) error { return c.lifecycle.Close(ctx) }

func (c *Worker) run(ctx context.Context) (err error) {
	defer func() {
		c.Stop(err)
		c.state.Close()
	}()
	if err := c.reconcileStops(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(c.sweepInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case request := <-c.requests:
			c.state.PrunePending()
			request()
		case <-ticker.C:
			if ctx.Err() != nil {
				return nil
			}
			c.state.PrunePending()
			if err := c.expireLeases(); err != nil {
				return fmt.Errorf("expire agent leases: %w", err)
			}
			if err := c.reconcileStops(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
