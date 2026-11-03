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

type AgentReader interface {
	Agent(context.Context, string) (agents.Agent, error)
}

type Dependencies struct {
	Bus        comms.Bus
	Operations operations.OperationStore
	Executions executions.ExecutionStore
	Artifacts  artifacts.ArtifactStore
	Agents     AgentReader
	Logger     *slog.Logger
}

type Worker struct {
	ctx            context.Context
	bus            comms.Bus
	operationStore operations.OperationStore
	executionStore executions.ExecutionStore
	artifactStore  artifacts.ArtifactStore
	agents         AgentReader
	logger         *slog.Logger
	queue          chan sharedData
}

func New(ctx context.Context, config Dependencies) *Worker {
	return &Worker{
		ctx: ctx, bus: config.Bus, operationStore: config.Operations,
		executionStore: config.Executions, artifactStore: config.Artifacts,
		agents: config.Agents, logger: config.Logger,
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

func (w *Worker) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case source := <-w.queue:
			forwarding, cancel := context.WithTimeout(ctx, sharingTimeout)
			var err error
			if source.QuestionID != "" {
				err = w.forwardSharedDecision(forwarding, source)
			} else {
				err = w.forwardSharedResult(forwarding, source)
			}
			cancel()
			if err != nil && ctx.Err() == nil {
				w.logger.Warn("shared data forwarding failed", "operation_id", source.Work.OperationID,
					"task_id", source.Work.TaskID, "command_id", source.CommandID, "question_id", source.QuestionID, "error_type", fmt.Sprintf("%T", err))
			}
		}
	}
}
