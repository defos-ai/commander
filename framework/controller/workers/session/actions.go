package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

// PrepareAction retains the supplied command identity. Coordination claims its
// execution afterward and publishes through this worker's lifecycle gate.
func (w *Worker) PrepareAction(ctx context.Context, command messages.Message) (messages.Message, error) {
	if err := command.Validate(); err != nil {
		return messages.Message{}, err
	}
	if command.Type != messages.RunAction || command.CorrelationID != "" || command.ExpiresAt.IsZero() || !time.Now().Before(command.ExpiresAt) {
		return messages.Message{}, errors.New("session preparation requires an unexpired action command")
	}
	var call actions.ActionCall
	if err := domain.Decode(command.Payload, &call); err != nil {
		return messages.Message{}, err
	}
	if err := call.Validate(); err != nil {
		return messages.Message{}, err
	}
	if command.OperationID != call.OperationID || command.TaskID != call.TaskID {
		return messages.Message{}, model.ErrConflict
	}
	if err := w.lock(ctx); err != nil {
		return messages.Message{}, err
	}
	defer w.unlock()
	intent, err := w.actionIntent(ctx, command.AgentID, call)
	if err != nil {
		return messages.Message{}, err
	}
	call.Session, err = w.prepareBinding(ctx, command.AgentID, command.ID, call, intent)
	if err != nil {
		if errors.Is(err, model.ErrWriteUncertain) {
			return messages.Message{}, errors.Join(executions.ErrIndeterminate, err)
		}
		return messages.Message{}, err
	}
	command.Payload, err = json.Marshal(call)
	return command, err
}

func (w *Worker) actionIntent(ctx context.Context, agentID string, call actions.ActionCall) (*model.Intent, error) {
	agent, err := w.agents.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	agent = agent.Snapshot(time.Now().UTC())
	if agent.Stale || agent.Status != agents.AgentReady && agent.Status != agents.AgentWorking {
		return nil, agents.ErrAgentNotRunning
	}
	for _, definition := range agent.Actions {
		if definition.Name != call.ActionName {
			continue
		}
		if definition.SessionMode == "" {
			return nil, nil
		}
		if readiness := operations.AssessAgentAction(agent, definition); readiness.Reason != "" {
			return nil, errors.New(readiness.Reason)
		}
		return model.ResolveIntent(definition.SessionMode, call.Input)
	}
	return nil, nil
}

func (w *Worker) prepareBinding(ctx context.Context, agentID, commandID string, call actions.ActionCall, intent *model.Intent) (*model.Binding, error) {
	if intent == nil {
		if call.Session != nil {
			return nil, errors.New("unexpected session binding")
		}
		return nil, nil
	}
	resolved := *intent
	if resolved.Mode == model.Open && resolved.ID == "" {
		resolved.ID = commandID
	}
	if err := resolved.Validate(); err != nil {
		return nil, err
	}
	scope, err := w.store.Get(ctx, resolved.ScopeID)
	if errors.Is(err, model.ErrUnknownSession) && resolved.Mode == model.Open && resolved.ID == commandID {
		return w.bindCreation(ctx, agentID, commandID, call, resolved)
	}
	if err != nil {
		return nil, err
	}
	record, err := scope.Find(resolved.ID)
	if errors.Is(err, model.ErrUnknownSession) && resolved.Mode == model.Open && resolved.ID == commandID {
		return w.bindCreation(ctx, agentID, commandID, call, resolved)
	}
	if err != nil {
		return nil, err
	}
	if record.CommandID == commandID {
		if resolved.Mode != model.Open || !record.CreatedBy(commandID, call.OperationID, call.TaskID) {
			return nil, model.ErrConflict
		}
		return w.bindCreation(ctx, agentID, commandID, call, resolved)
	}
	if record.OwnerAgentID != agentID {
		return nil, model.ErrConflict
	}
	if err := record.Usable(resolved.Mode); err != nil {
		return nil, err
	}
	binding := record.Binding(resolved.Mode)
	if call.Session != nil && *call.Session != binding {
		return nil, model.ErrConflict
	}
	return &binding, nil
}

func (w *Worker) bindCreation(ctx context.Context, agentID, commandID string, call actions.ActionCall, intent model.Intent) (*model.Binding, error) {
	binding := model.Binding{
		Reference: model.Reference{ID: intent.ID, ScopeID: intent.ScopeID, OwnerAgentID: agentID}, Mode: model.Open,
		CommandID: commandID, OperationID: call.OperationID, TaskID: call.TaskID,
	}
	if call.Session != nil && *call.Session != binding {
		return nil, model.ErrConflict
	}
	change := model.Change{Kind: model.Reserve, ID: intent.ID, ScopeID: intent.ScopeID,
		CommandID: commandID, OperationID: call.OperationID, TaskID: call.TaskID}
	if _, err := w.apply(ctx, change); err != nil {
		return nil, err
	}
	change.Kind, change.OwnerAgentID = model.Bind, agentID
	record, err := w.apply(ctx, change)
	if err != nil {
		return nil, err
	}
	bound := record.Binding(model.Open)
	return &bound, nil
}
