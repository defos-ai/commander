package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"commander/framework/agent/consumers"
	"commander/framework/agent/environment"
	"commander/framework/comms"
	agentcommands "commander/framework/comms/clients/agent/commands"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
)

// Serve discovers dependencies, announces the agent, and consumes its own inbox.
// Agents publish state and periodic full inventory heartbeats through the
// controller event client. Stop commands finish after active work; context
// cancellation interrupts active actions. A later controller can discover this
// agent on its next heartbeat even if it missed startup.
func (a *Agent) Serve(ctx context.Context, bus comms.Bus, scope *environment.DiscoveryScope) (serveErr error) {
	if bus == nil {
		return errors.New("event bus is required")
	}
	events := controllerevents.Client{Bus: bus}
	a.mu.Lock()
	if a.serving || a.status == AgentWorking || a.status == AgentDiscovering {
		a.mu.Unlock()
		return errors.New("agent is already serving or busy")
	}
	a.serving = true
	a.mu.Unlock()
	ctx, endService := context.WithCancelCause(ctx)
	workersCtx, stopWorkers := context.WithCancel(ctx)
	var heartbeatDone chan struct{}
	var resultsDone chan struct{}
	var receiptsDone chan struct{}
	var outcomesDone <-chan struct{}
	var subscription comms.Subscription
	var receipts comms.Subscription
	var stopCommandID string
	var stopAnnouncementErr error
	stopAcknowledged := false
	consumer := comms.Consumer{
		Logger: a.config.Logger,
		ContinueOnError: func(command messages.Message, err error) bool {
			// Artifact failures and conflicting commands stay retained while
			// later independent commands can still be processed.
			return command.Type == agentcommands.RunAction && !errors.Is(err, comms.ErrClosed)
		},
		AfterAck: func(command messages.Message) {
			if stopCommandID != "" && command.ID == stopCommandID {
				stopAcknowledged = true
				endService(nil)
			}
		},
	}
	stopBackground := func() {
		stopWorkers()
		if heartbeatDone != nil {
			<-heartbeatDone
		}
		if resultsDone != nil {
			<-resultsDone
		}
		if receiptsDone != nil {
			<-receiptsDone
		}
		if outcomesDone != nil {
			<-outcomesDone
		}
	}
	announceStopped := func(ctx context.Context, commandID string) error {
		return a.stateChange(ctx, func() error { a.status = AgentStopped; return nil }, func(ctx context.Context, snapshot AgentSnapshot) error {
			return events.State(ctx, controllerevents.AgentStopped, snapshot, commandID)
		})
	}
	defer func() {
		endService(nil)
		stopBackground()
		// Keep the serving claim through the final announcement so a concurrent
		// restart cannot publish its startup ahead of this stopped snapshot.
		defer func() {
			if receipts != nil {
				if err := receipts.Close(); err != nil && !errors.Is(err, comms.ErrClosed) {
					serveErr = errors.Join(serveErr, err)
				}
			}
			if subscription != nil {
				if err := subscription.Close(); err != nil && !errors.Is(err, comms.ErrClosed) {
					serveErr = errors.Join(serveErr, err)
				}
			}
			a.mu.Lock()
			a.serving = false
			a.mu.Unlock()
		}()
		if stopCommandID != "" {
			// The stop handler already attempted its final announcement.
			return
		}
		// Signals and failed startup still announce shutdown, without a command.
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := announceStopped(shutdown, ""); err != nil && !errors.Is(err, comms.ErrClosed) {
			serveErr = errors.Join(serveErr, err)
		}
	}()
	var err error
	subscription, err = bus.Subscribe(ctx, messages.AgentCommands(a.ID()))
	if err != nil {
		return err
	}
	consumer.Subscription = subscription
	receipts, err = bus.Subscribe(ctx, messages.AgentReceipts(a.ID()))
	if err != nil {
		return err
	}
	outcomesDone = a.outcomes.Start(workersCtx)
	receiptsDone = make(chan struct{})
	confirmation := consumers.ResultReceiptConsumer{
		AgentID: a.ID(), Outcomes: &a.outcomes, Logger: a.config.Logger,
	}
	receiptConsumer := comms.Consumer{
		Subscription: receipts, Logger: a.config.Logger,
		AfterAck: func(receipt messages.Message) {
			if err := confirmation.AfterAck(workersCtx, receipt); err != nil && workersCtx.Err() == nil {
				endService(err)
			}
		},
	}
	go func() {
		defer close(receiptsDone)
		err := receiptConsumer.Consume(workersCtx, confirmation.Consume)
		if workersCtx.Err() == nil {
			endService(err)
		}
	}()
	if err := a.discover(ctx, scope, true); err != nil {
		return err
	}
	if err := a.publishState(ctx, events, controllerevents.AgentStarted, ""); err != nil {
		return err
	}
	a.config.Logger.Info("agent ready", "action_count", len(a.config.Actions))
	runtime := agentRuntime{agent: a, events: events, shutdown: func(ctx context.Context, commandID string) error {
		stopCommandID = commandID
		stopBackground()
		// Finish shutdown before returning to the consumer for ACK. Announce
		// even after cancellation; the consumer still checks ctx before ACK.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		stopAnnouncementErr = announceStopped(cleanup, commandID)
		if stopAnnouncementErr != nil {
			return stopAnnouncementErr
		}
		return context.Cause(ctx)
	}}
	actionConsumer := consumers.ActionConsumer{
		Runtime: runtime, AgentID: a.ID(), Events: events, Logger: a.config.Logger, Outcomes: &a.outcomes,
	}
	resultsDone = make(chan struct{})
	go func() {
		defer close(resultsDone)
		if err := actionConsumer.RetryResults(workersCtx); workersCtx.Err() == nil {
			endService(err)
		}
	}()
	if a.heartbeatInterval > 0 {
		heartbeatDone = make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			ticker := time.NewTicker(a.heartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-workersCtx.Done():
					return
				case <-ticker.C:
					if err := a.publishState(workersCtx, events, controllerevents.AgentHeartbeat, ""); err != nil {
						if workersCtx.Err() == nil {
							a.config.Logger.Error("heartbeat failed", "error_type", fmt.Sprintf("%T", err))
							endService(err)
						}
						return
					}
					a.config.Logger.Debug("heartbeat sent", "status", a.Status())
				}
			}
		}()
	}
	commands := consumers.CommandConsumer{
		AgentID: a.ID(),
		Action:  actionConsumer,
		Discovery: consumers.DiscoveryConsumer{
			Runtime: runtime, AgentID: a.ID(), Events: events, Logger: a.config.Logger,
		},
		Stop: consumers.StopConsumer{
			Runtime: runtime, AgentID: a.ID(), Events: events, Logger: a.config.Logger, Outcomes: &a.outcomes,
		},
	}
	err = consumer.Consume(ctx, commands.Consume)
	if stopAcknowledged {
		return nil
	}
	if stopCommandID != "" && !errors.Is(stopAnnouncementErr, comms.ErrClosed) {
		// Preserve stop ACK failures, including a closed subscription.
		return err
	}
	if errors.Is(err, comms.ErrClosed) || errors.Is(context.Cause(ctx), comms.ErrClosed) {
		return nil
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

func (a *Agent) publishState(ctx context.Context, events controllerevents.Client, kind messages.MessageType, correlationID string) error {
	return a.stateChange(ctx, nil, func(ctx context.Context, snapshot AgentSnapshot) error {
		return events.State(ctx, kind, snapshot, correlationID)
	})
}
