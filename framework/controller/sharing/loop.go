package sharing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

const (
	sharingQueueSize = 16
	sharingTimeout   = 2 * time.Second
)

type Config struct {
	Bus        comms.Bus
	Operations operations.OperationStore
	Executions executions.ExecutionStore
	Artifacts  artifacts.ArtifactStore
	Agent      func(context.Context, string) (agents.Agent, error)
	Logger     *slog.Logger
}

type Worker struct {
	ctx            context.Context
	bus            comms.Bus
	operationStore operations.OperationStore
	executionStore executions.ExecutionStore
	artifactStore  artifacts.ArtifactStore
	agent          func(context.Context, string) (agents.Agent, error)
	logger         *slog.Logger
	queue          chan sharedData
}

func NewWorker(ctx context.Context, config Config) *Worker {
	return &Worker{
		ctx: ctx, bus: config.Bus, operationStore: config.Operations,
		executionStore: config.Executions, artifactStore: config.Artifacts,
		agent: config.Agent, logger: config.Logger,
		queue: make(chan sharedData, sharingQueueSize),
	}
}

type sharedData struct {
	AgentID    string
	CommandID  string
	Work       messages.WorkContext
	QuestionID string
}

func (w *Worker) QueueResult(message messages.Message) {
	if w.operationStore == nil || message.Type != messages.ActionSucceeded || message.Work.OperationID == "" {
		return
	}
	w.queueSharedData(sharedData{AgentID: message.AgentID, CommandID: message.CorrelationID, Work: message.Work})
}

// ShareQuestionAnswer is a runner notification after a successful answer save.
// The worker rereads saved evidence; the notification supplies no answer data.
func (w *Worker) ShareQuestionAnswer(operationID, questionID string) {
	if w.operationStore == nil || !id.Valid(operationID) || !id.Valid(questionID) {
		return
	}
	w.queueSharedData(sharedData{Work: messages.WorkContext{OperationID: operationID}, QuestionID: questionID})
}

func (w *Worker) queueSharedData(source sharedData) {
	select {
	case <-w.ctx.Done():
	case w.queue <- source:
	default:
		w.logger.Warn("shared data forwarding queue is full", "operation_id", source.Work.OperationID,
			"task_id", source.Work.TaskID, "command_id", source.CommandID, "question_id", source.QuestionID)
	}
}

func (w *Worker) Run() {
	for {
		select {
		case <-w.ctx.Done():
			return
		case source := <-w.queue:
			ctx, cancel := context.WithTimeout(w.ctx, sharingTimeout)
			var err error
			if source.QuestionID != "" {
				err = w.forwardSharedDecision(ctx, source)
			} else {
				err = w.forwardSharedResult(ctx, source)
			}
			cancel()
			if err != nil && w.ctx.Err() == nil {
				w.logger.Warn("shared data forwarding failed", "operation_id", source.Work.OperationID,
					"task_id", source.Work.TaskID, "command_id", source.CommandID, "question_id", source.QuestionID, "error_type", fmt.Sprintf("%T", err))
			}
		}
	}
}
