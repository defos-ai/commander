package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
	"commander/framework/controller/leases"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/executions"
	"commander/framework/id"
	"commander/framework/internal/state"
)

const DefaultHeartbeatTimeout = leases.DefaultTimeout

var (
	ErrControllerClosed = state.ErrControllerClosed
	ErrUnknownAgent     = agentmodel.ErrUnknownAgent
	ErrAgentNotRunning  = state.ErrAgentNotRunning
	ErrUnknownArtifact  = artifacts.ErrUnknownArtifact
)

type ControllerConfig struct {
	// Type identifies this controller implementation; empty selects default.
	Type string
	// ID identifies this process; empty generates <type>-controller-<8hex>.
	ID                 string
	ArtifactStore      artifacts.ArtifactStore
	AgentStore         agentmodel.AgentStore
	ActionStore        actionmodel.ActionStore
	ExecutionStore     executions.ExecutionStore
	HeartbeatTimeout   time.Duration
	LeaseSweepInterval time.Duration
	// Logger receives message handling events. Nil is silent.
	Logger *slog.Logger
}

// Controller discovers agents from its own event topic and routes decisions over
// the bus. It neither owns agent runtimes nor calls a model or selects actions.
// The host supplies controller-owned database stores.
type Controller struct {
	state              *state.State
	id                 string
	controllerType     string
	bus                comms.Bus
	logger             *slog.Logger
	artifactStore      artifacts.ArtifactStore
	executionStore     executions.ExecutionStore
	events             comms.Subscription
	cancel             context.CancelCauseFunc
	inventory          agentmodel.AgentStore
	leaseRegistry      *leases.Registry
	leaseSweepInterval time.Duration
	closeOnce          sync.Once
	closeErr           error // written before cleanupDone closes
	eventsDone         chan struct{}
	loopDone           chan struct{}
	requests           chan func()
	cleanupDone        chan struct{}
}

// NewController subscribes immediately. Announcements and heartbeats register
// agents; the host is responsible for starting and stopping their runtimes.
func NewController(ctx context.Context, bus comms.Bus, config ControllerConfig) (*Controller, error) {
	if ctx == nil || bus == nil {
		return nil, errors.New("controller requires a context and event bus")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Type == "" {
		config.Type = messages.DefaultControllerType
	}
	if err := messages.ValidateControllerType(config.Type); err != nil {
		return nil, err
	}
	if config.ID == "" {
		var err error
		config.ID, err = id.ControllerID(config.Type)
		if err != nil {
			return nil, err
		}
	}
	if kind, ok := id.RuntimeType(config.ID, id.Controller); !ok || kind != config.Type {
		return nil, errors.New("controller ID must match its type and use <type>-controller-<8 lowercase hex characters>")
	}
	if config.HeartbeatTimeout < 0 {
		return nil, errors.New("heartbeat timeout must not be negative")
	}
	if config.HeartbeatTimeout == 0 {
		config.HeartbeatTimeout = DefaultHeartbeatTimeout
	}
	if config.LeaseSweepInterval < 0 {
		return nil, errors.New("lease sweep interval must not be negative")
	}
	if config.LeaseSweepInterval == 0 {
		config.LeaseSweepInterval = max(time.Millisecond, min(leases.DefaultSweepInterval, config.HeartbeatTimeout/3))
	}
	if config.ArtifactStore == nil {
		return nil, errors.New("controller requires an artifact store")
	}
	if config.AgentStore == nil {
		return nil, errors.New("controller requires an agent inventory store")
	}
	if config.ActionStore == nil {
		return nil, errors.New("controller requires an action store")
	}
	if config.ExecutionStore == nil {
		return nil, errors.New("controller requires an execution store")
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	registry, err := leases.NewRegistry(config.AgentStore, config.HeartbeatTimeout)
	if err != nil {
		return nil, err
	}
	if _, err := registry.Sweep(ctx, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("expire agent leases: %w", err)
	}
	lifetime, cancel := context.WithCancelCause(ctx)
	events, err := bus.Subscribe(lifetime, comms.Inbox{Topic: messages.ControllerEvents(config.Type), Name: config.ID, Mode: comms.TransientExclusive})
	if err != nil {
		cancel(err)
		return nil, err
	}
	c := &Controller{
		id: config.ID, controllerType: config.Type, bus: bus, artifactStore: config.ArtifactStore,
		executionStore: config.ExecutionStore, logger: config.Logger,
		events: events, cancel: cancel, inventory: config.AgentStore,
		leaseRegistry: registry, leaseSweepInterval: config.LeaseSweepInterval,
		state:      state.New(lifetime, config.HeartbeatTimeout),
		eventsDone: make(chan struct{}), loopDone: make(chan struct{}), cleanupDone: make(chan struct{}),
		requests: make(chan func()),
	}
	go c.run()
	go c.consumeEvents(config.ActionStore)

	return c, nil
}

func (c *Controller) ID() string { return c.id }

func (c *Controller) Type() string { return c.controllerType }
