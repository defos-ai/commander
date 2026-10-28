package agent

import (
	"context"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
)

const logPublicationTimeout = 5 * time.Second

type logClientKey struct{}

type logClient struct {
	agentID string
	service context.Context
	events  controllerevents.Client
}

type executionLogs struct {
	client     *logClient
	lifetime   context.Context
	commandID  string
	work       messages.WorkContext
	actionName string
}

func (r *executionLogs) Log(ctx context.Context, text string) error {
	if err := r.contextError(ctx); err != nil {
		return err
	}
	publication, cancel := context.WithTimeout(ctx, logPublicationTimeout)
	stopExecution := context.AfterFunc(r.lifetime, cancel)
	stopService := context.AfterFunc(r.client.service, cancel)
	defer func() { stopExecution(); stopService(); cancel() }()
	message, err := controllerevents.NewExecutionLog(executions.Log{
		ID: id.New(), AgentID: r.client.agentID, CommandID: r.commandID,
		ActionName: r.actionName, Work: r.work, Message: text, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if err := r.contextError(publication); err != nil {
		return err
	}
	if err := r.client.events.Publish(publication, message); err != nil {
		return err
	}
	return r.contextError(publication)
}

func (r *executionLogs) contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// AfterFunc cancels detached calls asynchronously; check their owners too.
	if err := r.lifetime.Err(); err != nil {
		return err
	}
	return r.client.service.Err()
}

var _ action.LogProvider = (*executionLogs)(nil)
