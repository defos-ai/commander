package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/controller/leases"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/controller/workers/command"
	"github.com/defos-ai/commander/framework/controller/workers/coordination"
	"github.com/defos-ai/commander/framework/controller/workers/event"
	"github.com/defos-ai/commander/framework/controller/workers/operation"
	"github.com/defos-ai/commander/framework/controller/workers/session"
	"github.com/defos-ai/commander/framework/controller/workers/sharing"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/internal/state"
)

const DefaultHeartbeatTimeout = leases.DefaultTimeout

var (
	ErrControllerClosed = state.ErrControllerClosed
	ErrUnknownAgent     = agentmodel.ErrUnknownAgent
	ErrAgentNotRunning  = state.ErrAgentNotRunning
	ErrUnknownArtifact  = artifacts.ErrUnknownArtifact
)

type ControllerConfig struct {
	SessionStore       sessions.Store
	MissionStore       missions.MissionStore
	OperationStore     operations.OperationStore
	ReportStore        reports.ReportStore
	Type               string
	ID                 string
	ArtifactStore      artifacts.ArtifactStore
	AgentStore         agentmodel.AgentStore
	ActionStore        actionmodel.ActionStore
	ExecutionStore     executions.ExecutionStore
	SecretStore        secrets.SecretStore
	SecretRequestStore secrets.RequestStore
	SecretDecryptor    consumers.SecretDecryptor
	HeartbeatTimeout   time.Duration
	LeaseSweepInterval time.Duration
	AgentSelector      operation.AgentSelector
	TaskTimeout        time.Duration
	Capacity           int
	Logger             *slog.Logger
}

// Controller assembles components and owns their shared lifetime. Each worker
// owns its dependencies and behavior; public commands delegate to coordination.
type Controller struct {
	id             string
	controllerType string
	coordination   *coordination.Worker
	sessions       *session.Worker
	sharing        *sharing.Worker
	events         *event.Worker
	operations     *operation.Worker
	commands       *command.Worker
	workers        []workers.Worker
	coreWorkers    []workers.Worker
	lifetime       context.Context
	cancel         context.CancelCauseFunc
	coreLifetime   context.Context
	cancelCore     context.CancelCauseFunc
	closeOnce      sync.Once
	closeErr       error // written before cleanupDone closes
	workersDone    chan struct{}
	cleanupDone    chan struct{}
}

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
	if config.MissionStore == nil || config.OperationStore == nil || config.ReportStore == nil {
		return nil, errors.New("controller requires mission, operation and report stores")
	}
	if (config.SecretStore == nil) != (config.SecretRequestStore == nil) {
		return nil, errors.New("controller secret distribution requires both secret and request stores")
	}
	if config.SecretDecryptor != nil && config.SecretStore == nil {
		return nil, errors.New("controller secret decryption requires secret and request stores")
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	lifetime, cancel := context.WithCancelCause(ctx)
	// Core workers remain available until accepted operation results have drained.
	coreLifetime, cancelCore := context.WithCancelCause(context.WithoutCancel(ctx))
	stopStartup := context.AfterFunc(ctx, func() { cancelCore(context.Cause(ctx)) })
	c := &Controller{
		id: config.ID, controllerType: config.Type, lifetime: lifetime, cancel: cancel,
		coreLifetime: coreLifetime, cancelCore: cancelCore,
		workersDone: make(chan struct{}), cleanupDone: make(chan struct{}),
	}
	workers, err := assemble(c, bus, config)
	if err != nil {
		stopStartup()
		return nil, err
	}
	stopShutdown := context.AfterFunc(lifetime, func() { c.beginClose(context.Cause(lifetime)) })
	stopStartup()
	if err := ctx.Err(); err != nil {
		stopShutdown()
		c.beginClose(err)
		<-c.cleanupDone
		return nil, errors.Join(err, c.closeErr)
	}
	c.start(workers...)
	return c, nil
}

func (c *Controller) ID() string { return c.id }

func (c *Controller) Type() string { return c.controllerType }

func (c *Controller) Operations() controllercommands.Operations { return c.operations }
