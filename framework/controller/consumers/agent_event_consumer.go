package consumers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/internal/state"
)

type AgentEventConsumer struct {
	Inventory agentmodel.AgentStore
	Actions   actionmodel.ActionStore
	State     *state.State
}

func (c AgentEventConsumer) Consume(ctx context.Context, message messages.Message) error {
	receivedAt := time.Now().UTC()
	if message.Type != controllerevents.CommandFailed {
		fresh, err := freshObservation(message.CreatedAt, receivedAt, c.State.HeartbeatTimeout)
		if err != nil {
			return InvalidMessage(err)
		}
		if !fresh {
			if pending, ok := c.State.Pending[message.CorrelationID]; ok && pending.AgentID == message.AgentID {
				delete(c.State.Pending, message.CorrelationID)
				pending.Reply <- state.CommandReply{Message: message, Err: errors.New("agent reply is older than the heartbeat timeout")}
			}
			return nil
		}
	}
	record, err := c.Inventory.Get(ctx, message.AgentID)
	if err != nil && !errors.Is(err, agentmodel.ErrUnknownAgent) {
		return fmt.Errorf("read agent inventory for event: %w", err)
	}
	missingAgent := errors.Is(err, agentmodel.ErrUnknownAgent)
	if missingAgent && message.Type != controllerevents.AgentStarted && message.Type != controllerevents.AgentHeartbeat {
		return InvalidMessage(errors.New("event refers to an unknown agent"))
	}
	var resultErr error
	changed := false
	switch message.Type {
	case controllerevents.AgentStarted, controllerevents.AgentHeartbeat, controllerevents.AgentUpdated, controllerevents.AgentStopped:
		var snapshot agentmodel.AgentSnapshot
		snapshot, resultErr = descriptionReply(message)
		if resultErr == nil && message.Type == controllerevents.AgentStopped && snapshot.Status != agentmodel.AgentStopped {
			resultErr = errors.New("stopped event must describe a stopped agent")
		}
		if resultErr == nil {
			record, changed, err = c.observe(ctx, record, snapshot, message, receivedAt)
			if errors.Is(err, agentmodel.ErrUnknownAgent) {
				// The store retained an expiry marker and rejected an older replay.
				return nil
			}
			if err != nil {
				return err
			}
		}
	case controllerevents.CommandFailed:
		var detail executions.CommandError
		resultErr = domain.Decode(message.Payload, &detail)
		if resultErr == nil && (detail.ErrorType == "" || detail.Message == "") {
			resultErr = errors.New("command failure must contain an error type and message")
		}
	default:
		return InvalidMessage(fmt.Errorf("unsupported event type %q", message.Type))
	}
	if err := c.State.CheckOpen(); err != nil {
		return err
	}
	if changed {
		c.State.Notify()
	}
	var handlingErr error
	if resultErr != nil {
		handlingErr = InvalidMessage(resultErr)
	}
	if pending, ok := c.State.Pending[message.CorrelationID]; ok && pending.AgentID == message.AgentID {
		if resultErr == nil {
			switch {
			case message.Type == controllerevents.CommandFailed:
				var detail executions.CommandError
				_ = domain.Decode(message.Payload, &detail)
				resultErr = &executions.CommandFailure{AgentID: message.AgentID, CommandID: message.CorrelationID, Detail: detail}
			case message.Type != pending.Expected:
				resultErr = fmt.Errorf("unexpected agent reply %q, expected %q", message.Type, pending.Expected)
				handlingErr = InvalidMessage(resultErr)
			}
		}
		delete(c.State.Pending, message.CorrelationID)
		pending.Reply <- state.CommandReply{Message: message, Record: c.State.Snapshot(record, time.Now().UTC()), Err: resultErr}
	}
	if changed && message.Type == controllerevents.AgentStopped && record.Status == agentmodel.AgentStopped {
		c.State.FailAgent(message.AgentID, state.ErrAgentNotRunning)
	}
	return handlingErr
}

func (c AgentEventConsumer) observe(ctx context.Context, record agentmodel.Agent, snapshot agentmodel.AgentSnapshot, message messages.Message, receivedAt time.Time) (agentmodel.Agent, bool, error) {
	newSnapshot := message.CreatedAt.After(record.ObservedAt)
	heartbeat := message.Type == controllerevents.AgentStarted || message.Type == controllerevents.AgentHeartbeat
	heartbeatAt := message.CreatedAt
	if heartbeatAt.After(receivedAt) {
		heartbeatAt = receivedAt
	}
	renew := heartbeat && heartbeatAt.After(record.LastHeartbeat) && (newSnapshot || record.Status != agentmodel.AgentStopped)
	changed := newSnapshot || renew
	if newSnapshot {
		record.AgentSnapshot = snapshot
		record.ObservedAt, record.Stale = message.CreatedAt, false
	}
	if renew {
		// Liveness and snapshot ordering use independent source clocks. A
		// delayed heartbeat can renew the current snapshot without reverting it.
		record.LastHeartbeat = heartbeatAt
		record.LeaseExpiresAt = heartbeatAt.Add(c.State.HeartbeatTimeout)
		record.Stale = false
	}
	if changed {
		if receivedAt.After(record.LastSeen) {
			record.LastSeen = receivedAt
		}
		if err := c.Inventory.Upsert(ctx, record); errors.Is(err, agentmodel.ErrStaleObservation) {
			current, err := c.Inventory.Get(ctx, message.AgentID)
			if err != nil {
				return agentmodel.Agent{}, false, err
			}
			record = current
			changed = false
		} else if err != nil {
			return agentmodel.Agent{}, false, fmt.Errorf("persist agent observation: %w", err)
		}
	}
	if message.CreatedAt.Equal(record.ObservedAt) {
		// Repeating the accepted observation can repair catalog writes that
		// failed after inventory was saved. Never replay older definitions.
		for _, entry := range record.Actions {
			entry.Status, entry.StatusReason = actionmodel.Unknown, ""
			entry.MissingRequirements = nil
			if err := c.Actions.Upsert(ctx, entry); err != nil {
				return agentmodel.Agent{}, false, fmt.Errorf("persist announced action %q: %w", entry.Name, err)
			}
		}
	}
	return record, changed || message.CreatedAt.Equal(record.ObservedAt), nil
}

func descriptionReply(message messages.Message) (agentmodel.AgentSnapshot, error) {
	var description agentmodel.AgentSnapshot
	if err := domain.Decode(message.Payload, &description); err != nil {
		return agentmodel.AgentSnapshot{}, err
	}
	if description.ID != message.AgentID {
		return agentmodel.AgentSnapshot{}, errors.New("agent description must identify the reporting agent")
	}
	if !id.ValidType(string(description.Type)) {
		return agentmodel.AgentSnapshot{}, errors.New("agent description must declare a valid type")
	}
	kind, ok := id.RuntimeType(description.ID, id.Agent)
	if !ok || kind != strings.TrimSuffix(string(description.Type), "-agent") {
		return agentmodel.AgentSnapshot{}, errors.New("agent ID must match its type")
	}
	if err := description.Server.Validate(); err != nil {
		return agentmodel.AgentSnapshot{}, err
	}
	switch description.Status {
	case agentmodel.AgentIdle, agentmodel.AgentDiscovering, agentmodel.AgentReady, agentmodel.AgentWorking, agentmodel.AgentUnavailable, agentmodel.AgentStopped:
	default:
		return agentmodel.AgentSnapshot{}, errors.New("agent description has an invalid status")
	}
	seen := make(map[string]bool, len(description.Actions))
	for _, entry := range description.Actions {
		if err := entry.Validate(); err != nil {
			return agentmodel.AgentSnapshot{}, fmt.Errorf("agent action %q: %w", entry.Name, err)
		}
		if strings.TrimSpace(entry.Name) == "" || seen[entry.Name] {
			return agentmodel.AgentSnapshot{}, errors.New("agent action names must be nonblank and unique")
		}
		seen[entry.Name] = true
		if entry.Status != actionmodel.Unknown && entry.Status != actionmodel.Ready && entry.Status != actionmodel.Unavailable {
			return agentmodel.AgentSnapshot{}, errors.New("agent action has an invalid status")
		}
	}
	if err := validateWorkRequirements(description, seen); err != nil {
		return agentmodel.AgentSnapshot{}, err
	}
	return description, nil
}

func validateWorkRequirements(snapshot agentmodel.AgentSnapshot, actions map[string]bool) error {
	work := snapshot.Work
	if work == nil || len(work.MissingRequirements) == 0 {
		return nil
	}
	if snapshot.Status != agentmodel.AgentWorking || !id.Valid(work.CommandID) || !actions[work.ActionName] {
		return errors.New("runtime requirements must identify an executing command and action")
	}
	workContext := messages.WorkContext{OperationID: work.OperationID, TaskID: work.TaskID, StepID: work.StepID}
	if err := workContext.Validate(); err != nil {
		return err
	}
	seen := make(map[actionmodel.Requirement]bool, len(work.MissingRequirements))
	for _, requirement := range work.MissingRequirements {
		if err := requirement.Validate(); err != nil {
			return fmt.Errorf("runtime requirement: %w", err)
		}
		if seen[requirement] {
			return errors.New("runtime requirements must be unique")
		}
		seen[requirement] = true
	}
	return nil
}
