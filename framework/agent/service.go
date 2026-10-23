package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/agent/consumers"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
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
	if err := a.stateChange(ctx, func(next *agentState) error {
		if next.serving || next.status == AgentWorking || next.status == AgentDiscovering {
			return errors.New("agent is already serving or busy")
		}
		next.serving = true
		return nil
	}, nil); err != nil {
		return err
	}
	ctx, endService := context.WithCancelCause(ctx)
	workersCtx, stopWorkers := context.WithCancel(ctx)
	checkpoints := &checkpointClient{agentID: a.ID(), service: workersCtx, events: events, pending: make(map[string]checkpointWait)}
	var heartbeatDone chan struct{}
	var resultsDone chan struct{}
	var eventsDone chan struct{}
	var outcomesDone <-chan struct{}
	var sessionsDone <-chan struct{}
	var secretsDone chan struct{}
	var subscription comms.Subscription
	var eventSubscription comms.Subscription
	var stopCommandID string
	var stopAnnouncementErr error
	stopAcknowledged := false
	consumer := comms.Consumer{
		Logger: a.config.Logger,
		ContinueOnError: func(command messages.Message, err error) bool {
			// Artifact failures and conflicting commands are rejected while
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
		if sessionsDone != nil {
			<-sessionsDone
		}
		if heartbeatDone != nil {
			<-heartbeatDone
		}
		if resultsDone != nil {
			<-resultsDone
		}
		if eventsDone != nil {
			<-eventsDone
		}
		if outcomesDone != nil {
			<-outcomesDone
		}
		if secretsDone != nil {
			<-secretsDone
		}
	}
	announceStopped := func(ctx context.Context, commandID string) error {
		if closer, ok := a.config.Sessions.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				return err
			}
		}

		return a.stateChange(ctx, func(next *agentState) error {
			next.status, next.work = AgentStopped, nil
			return nil
		}, func(ctx context.Context, snapshot AgentSnapshot) error {
			return events.State(ctx, controllerevents.AgentStopped, snapshot, commandID)
		})
	}
	defer func() {
		endService(nil)
		stopBackground()
		a.secrets.clear()
		// Keep the serving claim through the final announcement so a concurrent
		// restart cannot publish its startup ahead of this stopped snapshot.
		defer func() {
			if eventSubscription != nil {
				if err := eventSubscription.Close(); err != nil && !errors.Is(err, comms.ErrClosed) {
					serveErr = errors.Join(serveErr, err)
				}
			}
			if subscription != nil {
				if err := subscription.Close(); err != nil && !errors.Is(err, comms.ErrClosed) {
					serveErr = errors.Join(serveErr, err)
				}
			}
			serveErr = errors.Join(serveErr, a.stateChange(context.WithoutCancel(ctx), func(next *agentState) error {
				next.serving = false
				return nil
			}, nil))
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
	a.secrets.clear()
	subscription, err = bus.Subscribe(ctx, comms.Inbox{Topic: messages.AgentCommands(a.ID()), Name: a.ID(), Mode: comms.TransientExclusive})
	if err != nil {
		return err
	}
	consumer.Subscription = subscription
	eventSubscription, err = bus.Subscribe(ctx, comms.Inbox{Topic: messages.AgentEvents(a.ID()), Name: a.ID(), Mode: comms.TransientExclusive})
	if err != nil {
		return err
	}
	outcomesDone = a.outcomes.Start(workersCtx)
	eventsDone = make(chan struct{})
	confirmation := consumers.ResultReceiptConsumer{
		AgentID: a.ID(), Outcomes: &a.outcomes, Logger: a.config.Logger,
	}
	eventConsumer := comms.Consumer{
		Subscription: eventSubscription, Logger: a.config.Logger,
		AfterAck: func(receipt messages.Message) {
			if receipt.Type != agentevents.ActionResultAccepted {
				return
			}
			if err := confirmation.AfterAck(workersCtx, receipt); err != nil && workersCtx.Err() == nil {
				endService(err)
			}
		},
	}
	go func() {
		defer close(eventsDone)
		err := eventConsumer.Consume(workersCtx, func(ctx context.Context, message messages.Message) error {
			if message.Type == agentevents.CheckpointResponded {
				checkpoints.receive(message)
				return nil
			}
			if message.Type == agentevents.SecretDelivered {
				if a.secrets != nil {
					if err := a.secrets.install(message, time.Now().UTC()); err != nil {
						a.config.Logger.Warn("secret delivery rejected", "message_id", message.ID)
					}
				}
				return nil
			}
			return confirmation.Consume(ctx, message)
		})
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
	sessionsDone, err = a.serveSessions(workersCtx, bus, events, endService)
	if err != nil {
		return err
	}
	if a.secrets != nil {
		a.secrets.start(workersCtx)
		secretsDone = make(chan struct{})
		go func() {
			defer close(secretsDone)
			a.synchronizeSecrets(workersCtx, events)
		}()
	}
	a.config.Logger.Info("agent ready", "action_count", len(a.config.Actions))
	runtime := agentRuntime{agent: a, events: events, checkpoints: checkpoints, shutdown: func(ctx context.Context, commandID string) error {
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
