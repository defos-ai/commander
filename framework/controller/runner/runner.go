// Package runner owns operations while an external decision maker selects work.
package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
)

// ActionExecutor dispatches new work and observes previously dispatched work.
// RecoverAction must never execute or republish the command.
type ActionExecutor interface {
	RunAction(context.Context, string, actionmodel.ActionCall) (actionmodel.ActionOutput, error)
	RecoverAction(context.Context, string, actionmodel.ActionCall) (actionmodel.ActionOutput, error)
}

// DurableActionExecutor can safely resume a reserved dispatch to its original
// instance using the same command envelope. RecoverAction remains observation-only.
type DurableActionExecutor interface {
	DispatchAction(context.Context, string, actionmodel.ActionCall) (actionmodel.ActionOutput, error)
}

var (
	ErrClosed          = operations.ErrClosed
	ErrFinished        = operations.ErrFinished
	ErrQuestionPending = operations.ErrQuestionPending
	ErrNotRunning      = operations.ErrNotRunning
)

type AgentCatalog interface {
	Agents(context.Context) ([]agentmodel.Agent, error)
}

type Config struct {
	AgentCatalog AgentCatalog
	// AgentSelector defaults to round-robin over eligible instances.
	AgentSelector AgentSelector
	Logger        *slog.Logger
	// Capacity is per agent for work owned by this runner; default one.
	Capacity int

	MissionStore   missions.MissionStore
	OperationStore operations.OperationStore
	// ReportStore saves immutable reports before an operation is completed.
	ReportStore reports.ReportStore
	// TaskTimeout bounds the wait for a dispatched command. Default: five minutes.
	TaskTimeout time.Duration
}

type operationState struct {
	operation operations.Operation
	changed   chan struct{}
	err       error
}

type Runner struct {
	ctx         context.Context
	cancel      context.CancelFunc
	executor    ActionExecutor
	store       operations.OperationStore
	missions    missions.MissionStore
	reports     reports.ReportStore
	states      map[string]*operationState
	taskTimeout time.Duration
	catalog     AgentCatalog
	selector    AgentSelector
	logger      *slog.Logger
	capacity    int
	lastAgent   string
	wake        chan struct{}
	requests    chan func()
	results     chan taskResult
	workers     int
	closeErr    error // written before done closes
	done        chan struct{}
}

func NewRunner(ctx context.Context, executor ActionExecutor, config Config) (*Runner, error) {
	if ctx == nil || executor == nil {
		return nil, errors.New("runner requires a context and action executor")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.TaskTimeout < 0 {
		return nil, errors.New("task timeout must not be negative")
	}
	if config.TaskTimeout == 0 {
		config.TaskTimeout = 5 * time.Minute
	}
	if config.OperationStore == nil {
		return nil, errors.New("runner requires an operation store")
	}
	if config.MissionStore == nil {
		return nil, errors.New("runner requires a mission store")
	}
	if config.ReportStore == nil {
		return nil, errors.New("runner requires a report store")
	}
	if config.AgentCatalog == nil {
		return nil, errors.New("runner requires an agent catalog")
	}
	if config.Capacity < 0 {
		return nil, errors.New("capacity must not be negative")
	}
	if config.Capacity == 0 {
		config.Capacity = 1
	}
	if config.AgentSelector == nil {
		config.AgentSelector = RoundRobinSelector{}
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	lifetime, cancel := context.WithCancel(ctx)
	r := &Runner{catalog: config.AgentCatalog, capacity: config.Capacity, wake: make(chan struct{}, 1), requests: make(chan func()), results: make(chan taskResult), ctx: lifetime, cancel: cancel, executor: executor, store: config.OperationStore,
		missions: config.MissionStore, reports: config.ReportStore, selector: config.AgentSelector, logger: config.Logger, states: make(map[string]*operationState), taskTimeout: config.TaskTimeout, done: make(chan struct{})}
	if err := r.recover(ctx); err != nil {
		cancel()
		return nil, err
	}
	go r.run()
	return r, nil
}
