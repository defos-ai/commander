package sharing

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func (w *Worker) forwardSharedResult(ctx context.Context, source sharedData) error {
	if w.operationStore == nil || source.OperationID == "" {
		return nil
	}
	stored, err := w.executionStore.Get(ctx, source.AgentID, source.CommandID)
	if err != nil {
		return err
	}
	if err := stored.Validate(); err != nil {
		return err
	}
	if stored.Command.AgentID != source.AgentID || stored.Command.ID != source.CommandID || (stored.Command.OperationID != source.OperationID || stored.Command.TaskID != source.TaskID) {
		return errors.New("shared result does not match its saved execution")
	}
	if stored.Result == nil || stored.Result.Type != messages.ActionSucceeded {
		return nil
	}
	var call actions.ActionCall
	if err := domain.Decode(stored.Command.Payload, &call); err != nil {
		return err
	}
	output, err := executions.DecodeActionResult(*stored.Result, source.AgentID, source.CommandID, call)
	if err != nil {
		return err
	}
	op, err := w.operationStore.Get(ctx, source.OperationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		return nil
	}
	if err != nil {
		return err
	}
	if op.ID != source.OperationID {
		return errors.New("shared result operation does not match its saved scope")
	}
	if op.Status.Terminal() || op.Mission.Policies == nil || len(op.Mission.Policies.Sharing) == 0 {
		return nil
	}
	if err := op.Mission.Policies.Validate(); err != nil {
		return err
	}
	index := slices.IndexFunc(op.Tasks, func(task operations.Task) bool { return task.ID == source.TaskID })
	if index < 0 {
		return nil
	}
	if !sharedTaskMatches(op.Tasks[index], stored.Command, call) {
		return errors.New("shared result has no matching saved task dispatch")
	}
	batchID := op.Tasks[index].BatchID
	// Batched results follow the operation worker's saved event sequence, not
	// the independently accepted execution notification.
	if batchID != "" && source.BatchID != batchID {
		return nil
	}
	identity := agentevents.SharedDataSource{AgentID: source.AgentID, CommandID: source.CommandID, ActionName: call.ActionName, OperationID: source.OperationID, TaskID: source.TaskID}
	var accepted []artifacts.Artifact
	var artifactErr, forwardingErr error
	artifactsRead := false
	for _, task := range op.Tasks {
		if err := ctx.Err(); err != nil {
			return errors.Join(forwardingErr, err)
		}
		if task.ID == source.TaskID || task.OperationID != op.ID || task.Status != operations.TaskRunning ||
			task.DeadlineAt == nil || !time.Now().Before(*task.DeadlineAt) {
			continue
		}
		if batchID != "" && !w.batchRecipient(batchID, source.TaskID, task) {
			continue
		}
		shareResults, shareArtifacts := sharedResultKinds(op.Mission.Policies.Sharing, call.ActionName, task.ActionName)
		if !shareResults && (!shareArtifacts || len(output.Artifacts) == 0) {
			continue
		}
		update := agentevents.SharedData{Source: identity, BatchID: op.Tasks[index].BatchID}
		if shareResults {
			update.Result = bytes.Clone(output.Output)
		}
		if shareArtifacts && len(output.Artifacts) != 0 {
			if !artifactsRead {
				accepted, artifactErr = w.sharedArtifacts(ctx, output.Artifacts)
				artifactsRead = true
			}
			if artifactErr != nil {
				forwardingErr = errors.Join(forwardingErr, artifactErr)
				continue
			}
			update.Artifacts = artifacts.CloneAll(accepted)
		}
		forwardingErr = errors.Join(forwardingErr, w.publishSharedUpdate(ctx, task, update))
	}
	return forwardingErr
}

func sharedResultKinds(policy policies.SharingPolicy, from, to string) (results, artifacts bool) {
	for _, route := range policy {
		if slices.Contains(route.FromActions, from) && slices.Contains(route.ToActions, to) {
			results = results || slices.Contains(route.Include, policies.ShareResults)
			artifacts = artifacts || slices.Contains(route.Include, policies.ShareArtifacts)
		}
	}
	return results, artifacts
}

func sharedTaskMatches(task operations.Task, command messages.Message, call actions.ActionCall) bool {
	return task.ID == task.CommandID && task.CommandID == command.ID && task.AgentID == command.AgentID &&
		task.OperationID == command.OperationID && task.ID == command.TaskID && (call.OperationID == command.OperationID && call.TaskID == command.TaskID) && task.ActionName == call.ActionName
}

func (w *Worker) publishSharedUpdate(ctx context.Context, task operations.Task, update agentevents.SharedData) error {
	message, err := agentevents.NewSharedData(task.AgentID, task.CommandID, task.OperationID, task.ID, update)
	if err != nil {
		return err
	}
	active, err := w.sharedRecipient(ctx, task)
	if err != nil || !active || !time.Now().Before(*task.DeadlineAt) {
		return err
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	if task.DeadlineAt.Before(deadline) {
		deadline = *task.DeadlineAt
	}
	publication, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	return (agentevents.Client{Bus: w.bus}).Publish(publication, message)
}

func (w *Worker) sharedRecipient(ctx context.Context, task operations.Task) (bool, error) {
	if task.Status != operations.TaskRunning || task.DeadlineAt == nil || !time.Now().Before(*task.DeadlineAt) ||
		!id.ValidAgent(task.AgentID) || !id.Valid(task.CommandID) {
		return false, nil
	}
	op, err := w.operationStore.Get(ctx, task.OperationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if op.ID != task.OperationID {
		return false, errors.New("sharing recipient operation does not match its saved scope")
	}
	if op.Status.Terminal() {
		return false, nil
	}
	index := slices.IndexFunc(op.Tasks, func(current operations.Task) bool { return current.ID == task.ID })
	if index < 0 {
		return false, nil
	}
	current := op.Tasks[index]
	if current.Status != operations.TaskRunning || current.OperationID != task.OperationID || current.AgentID != task.AgentID ||
		current.ActionName != task.ActionName || current.CommandID != task.CommandID || current.DeadlineAt == nil || !current.DeadlineAt.Equal(*task.DeadlineAt) {
		return false, nil
	}
	claim, err := w.executionStore.Get(ctx, task.AgentID, task.CommandID)
	if errors.Is(err, executions.ErrUnknownExecution) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := claim.Validate(); err != nil {
		return false, err
	}
	var call actions.ActionCall
	if err := domain.Decode(claim.Command.Payload, &call); err != nil {
		return false, err
	}
	if claim.Result != nil || !sharedTaskMatches(task, claim.Command, call) ||
		!claim.Command.ExpiresAt.IsZero() && !time.Now().Before(claim.Command.ExpiresAt) {
		return false, nil
	}
	record, err := w.agents.Agent(ctx, task.AgentID)
	if errors.Is(err, agents.ErrUnknownAgent) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	work := record.Work
	return record.ID == task.AgentID && !record.Stale && record.Status == agents.AgentWorking && work != nil &&
		work.OperationID == task.OperationID && work.TaskID == task.ID && work.CommandID == task.CommandID && work.ActionName == task.ActionName, nil
}

func (w *Worker) sharedArtifacts(ctx context.Context, refs []artifacts.Artifact) ([]artifacts.Artifact, error) {
	accepted := make([]artifacts.Artifact, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if seen[ref.ID] {
			continue
		}
		saved, err := w.artifactStore.Get(ctx, ref.ID)
		if err != nil {
			return nil, err
		}
		if !saved.Equal(ref) {
			return nil, errors.New("shared artifact does not match its accepted result")
		}
		seen[ref.ID] = true
		accepted = append(accepted, saved.Clone())
	}
	return accepted, nil
}
