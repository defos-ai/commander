package sharing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

const (
	sharingQueueSize   = 16
	sharingTimeout     = 2 * time.Second
	batchSweepInterval = time.Minute
)

type AgentReader interface {
	Agent(context.Context, string) (agents.Agent, error)
}

type Dependencies struct {
	ControllerID string
	Bus          comms.Bus
	Operations   operations.OperationStore
	Executions   executions.ExecutionStore
	Artifacts    artifacts.ArtifactStore
	Agents       AgentReader
	Logger       *slog.Logger
}

type Worker struct {
	controllerID   string
	lifecycle      *workers.Lifecycle
	ctx            context.Context
	bus            comms.Bus
	operationStore operations.OperationStore
	executionStore executions.ExecutionStore
	artifactStore  artifacts.ArtifactStore
	agents         AgentReader
	logger         *slog.Logger
	queue          chan sharedData
	batches        map[string]batchRecipients
}

func New(ctx context.Context, config Dependencies) *Worker {
	life := workers.NewLifecycle(ctx)
	return &Worker{
		controllerID: config.ControllerID, batches: make(map[string]batchRecipients),
		lifecycle: life, ctx: life.Context(), bus: config.Bus, operationStore: config.Operations,
		executionStore: config.Executions, artifactStore: config.Artifacts,
		agents: config.Agents, logger: config.Logger,
		queue: make(chan sharedData, sharingQueueSize),
	}
}

type sharedData struct {
	AgentID     string
	CommandID   string
	OperationID string
	TaskID      string
	QuestionID  string
	BatchID     string
	Events      []operations.Event
}

func (w *Worker) QueueResult(message messages.Message) {
	if w.operationStore == nil || message.Type != messages.ActionSucceeded || message.OperationID == "" {
		return
	}
	w.queueSharedData(sharedData{AgentID: message.AgentID, CommandID: message.CorrelationID, OperationID: message.OperationID, TaskID: message.TaskID})
}

// QueueOperationEvents observes confirmed saves only, never recovered history.
// One notification retains the order of boundaries and data saved together.
func (w *Worker) QueueOperationEvents(events []operations.Event) {
	if w.operationStore == nil {
		return
	}
	var selected []operations.Event
	for _, event := range events {
		switch event.Type {
		case operations.TaskBatchStarted, operations.TaskBatchFinished, operations.TaskFinished,
			operations.QuestionAnsweredEvent, operations.OperationCompleted, operations.OperationFailed, operations.OperationCancelled:
			selected = append(selected, event)
		}
	}
	if len(selected) != 0 {
		w.queueSharedData(sharedData{OperationID: selected[0].OperationID, Events: selected})
	}
}

func (w *Worker) queueSharedData(source sharedData) {
	select {
	case <-w.ctx.Done():
	case w.queue <- source:
	default:
		w.logger.Warn("shared data forwarding queue is full", "operation_id", source.OperationID,
			"task_id", source.TaskID, "command_id", source.CommandID, "question_id", source.QuestionID)
	}
}

func (w *Worker) Run(ctx context.Context) error {
	return w.lifecycle.Run(ctx, w.run)
}

func (w *Worker) Close(ctx context.Context) error {
	return w.lifecycle.Close(ctx)
}

func (w *Worker) run(ctx context.Context) error {
	sweep := time.NewTicker(batchSweepInterval)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sweep.C:
			cleanup, cancel := context.WithTimeout(ctx, sharingTimeout)
			err := w.pruneSavedBatches(cleanup)
			cancel()
			if err != nil && ctx.Err() == nil {
				w.logger.Warn("shared batch cleanup failed", "error_type", fmt.Sprintf("%T", err))
			}
		case source := <-w.queue:
			forwarding, cancel := context.WithTimeout(ctx, sharingTimeout)
			var err error
			if len(source.Events) != 0 {
				err = w.forwardOperationEvents(forwarding, source.Events)
			} else {
				err = w.forwardSharedResult(forwarding, source)
			}
			cancel()
			if err != nil && ctx.Err() == nil {
				w.logger.Warn("shared data forwarding failed", "operation_id", source.OperationID,
					"task_id", source.TaskID, "command_id", source.CommandID, "question_id", source.QuestionID, "error_type", fmt.Sprintf("%T", err))
			}
		}
	}
}
