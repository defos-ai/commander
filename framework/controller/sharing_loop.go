package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
)

const (
	sharingQueueSize = 16
	sharingTimeout   = 2 * time.Second
)

type sharedResult struct {
	AgentID   string
	CommandID string
	Work      messages.WorkContext
}

func (c *Controller) queueSharedResult(message messages.Message) {
	if c.operationStore == nil || message.Type != messages.ActionSucceeded || message.Work.OperationID == "" {
		return
	}
	source := sharedResult{AgentID: message.AgentID, CommandID: message.CorrelationID, Work: message.Work}
	select {
	case <-c.state.Context.Done():
	case c.sharedResults <- source:
	default:
		c.logger.Warn("shared data forwarding queue is full", "operation_id", source.Work.OperationID,
			"task_id", source.Work.TaskID, "command_id", source.CommandID)
	}
}

func (c *Controller) forwardSharedResults() {
	defer close(c.sharingDone)
	for {
		select {
		case <-c.state.Context.Done():
			return
		case source := <-c.sharedResults:
			ctx, cancel := context.WithTimeout(c.state.Context, sharingTimeout)
			err := c.forwardSharedResult(ctx, source)
			cancel()
			if err != nil && c.state.Context.Err() == nil {
				c.logger.Warn("shared data forwarding failed", "operation_id", source.Work.OperationID,
					"task_id", source.Work.TaskID, "command_id", source.CommandID, "error_type", fmt.Sprintf("%T", err))
			}
		}
	}
}
