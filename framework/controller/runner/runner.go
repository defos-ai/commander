// Package runner owns operations while an external decision maker selects work.
package runner

import (
	"context"
	"errors"
	"sync"
	"time"

	"commander/framework/artifact"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
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

// ArtifactRestorer makes persisted evidence references available after restart.
type ArtifactRestorer interface {
	RestoreArtifacts([]artifact.Ref) error
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
	// Capacity is per agent for work owned by this runner; default one.
	Capacity int

	MissionStore     missions.MissionStore
	OperationStore   operations.OperationStore
	ArtifactRestorer ArtifactRestorer
	// ReportStore saves immutable reports before an operation is completed.
	ReportStore reports.ReportStore
	// TaskTimeout bounds the wait for a dispatched command. Default: five minutes.
	TaskTimeout time.Duration
}

type operationState struct {
	mu        sync.Mutex
	operation operations.Operation
	changed   chan struct{}
	err       error
}

type Runner struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	executor    ActionExecutor
	store       operations.OperationStore
	missions    missions.MissionStore
	reports     reports.ReportStore
	states      map[string]*operationState
	taskTimeout time.Duration
	catalog     AgentCatalog
	capacity    int
	lastAgent   string
	wake        chan struct{}
	closed      bool
	closeOnce   sync.Once
	closeErr    error // written before done closes
	wg          sync.WaitGroup
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
	if config.ArtifactRestorer == nil {
		return nil, errors.New("runner requires an artifact restorer")
	}
	if config.Capacity < 0 {
		return nil, errors.New("capacity must not be negative")
	}
	if config.Capacity == 0 {
		config.Capacity = 1
	}
	lifetime, cancel := context.WithCancel(ctx)
	r := &Runner{catalog: config.AgentCatalog, capacity: config.Capacity, wake: make(chan struct{}, 1), ctx: lifetime, cancel: cancel, executor: executor, store: config.OperationStore,
		missions: config.MissionStore, reports: config.ReportStore, states: make(map[string]*operationState), taskTimeout: config.TaskTimeout, done: make(chan struct{})}
	if err := r.recover(ctx, config.ArtifactRestorer); err != nil {
		cancel()
		return nil, err
	}
	r.resumeTasks()
	r.wg.Add(1)
	go r.schedule()
	go func() { <-lifetime.Done(); r.beginClose() }()
	return r, nil
}
