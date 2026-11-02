package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
)

const (
	sharingQueueSize = 16
	sharingTimeout   = 2 * time.Second
)

type sharedData struct {
	AgentID    string
	CommandID  string
	Work       messages.WorkContext
	QuestionID string
}

func (c *Controller) queueSharedResult(message messages.Message) {
	if c.operationStore == nil || message.Type != messages.ActionSucceeded || message.Work.OperationID == "" {
		return
	}
	c.queueSharedData(sharedData{AgentID: message.AgentID, CommandID: message.CorrelationID, Work: message.Work})
}

// ShareQuestionAnswer is a runner notification after a successful answer save.
// The worker rereads saved evidence; the notification supplies no answer data.
func (c *Controller) ShareQuestionAnswer(operationID, questionID string) {
	if c.operationStore == nil || !id.Valid(operationID) || !id.Valid(questionID) {
		return
	}
	c.queueSharedData(sharedData{Work: messages.WorkContext{OperationID: operationID}, QuestionID: questionID})
}

func (c *Controller) queueSharedData(source sharedData) {
	select {
	case <-c.state.Context.Done():
	case c.sharingQueue <- source:
	default:
		c.logger.Warn("shared data forwarding queue is full", "operation_id", source.Work.OperationID,
			"task_id", source.Work.TaskID, "command_id", source.CommandID, "question_id", source.QuestionID)
	}
}

func (c *Controller) forwardSharedData() {
	defer close(c.sharingDone)
	for {
		select {
		case <-c.state.Context.Done():
			return
		case source := <-c.sharingQueue:
			ctx, cancel := context.WithTimeout(c.state.Context, sharingTimeout)
			var err error
			if source.QuestionID != "" {
				err = c.forwardSharedDecision(ctx, source)
			} else {
				err = c.forwardSharedResult(ctx, source)
			}
			cancel()
			if err != nil && c.state.Context.Err() == nil {
				c.logger.Warn("shared data forwarding failed", "operation_id", source.Work.OperationID,
					"task_id", source.Work.TaskID, "command_id", source.CommandID, "question_id", source.QuestionID, "error_type", fmt.Sprintf("%T", err))
			}
		}
	}
}
