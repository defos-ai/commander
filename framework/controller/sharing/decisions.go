package sharing

import (
	"context"
	"errors"
	"slices"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func (w *Worker) forwardSharedDecision(ctx context.Context, source sharedData) error {
	if w.operationStore == nil || source.Work.OperationID == "" || source.QuestionID == "" {
		return nil
	}
	op, err := w.operationStore.Get(ctx, source.Work.OperationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		return nil
	}
	if err != nil {
		return err
	}
	if op.ID != source.Work.OperationID {
		return errors.New("shared decision operation does not match its saved scope")
	}
	if op.Status.Terminal() || op.Mission.Policies == nil || len(op.Mission.Policies.Sharing) == 0 {
		return nil
	}
	if err := op.Mission.Policies.Validate(); err != nil {
		return err
	}
	index := slices.IndexFunc(op.Questions, func(question operations.Question) bool { return question.ID == source.QuestionID })
	if index < 0 {
		return nil
	}
	question := op.Questions[index].Clone()
	if question.Status != operations.QuestionAnswered || question.TaskID == "" {
		return nil
	}
	index = slices.IndexFunc(op.Tasks, func(task operations.Task) bool { return task.ID == question.TaskID })
	if index < 0 {
		return nil
	}
	task := op.Tasks[index]
	if task.OperationID != op.ID || question.CommandID != "" && question.CommandID != task.CommandID {
		return errors.New("shared decision does not match its saved task dispatch")
	}
	claim, err := w.executionStore.Get(ctx, task.AgentID, task.CommandID)
	if errors.Is(err, executions.ErrUnknownExecution) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := claim.Validate(); err != nil {
		return err
	}
	var call actions.ActionCall
	if err := domain.Decode(claim.Command.Payload, &call); err != nil {
		return err
	}
	if !sharedTaskMatches(task, claim.Command, call) {
		return errors.New("shared decision does not match its saved execution")
	}
	update := agentevents.ContextUpdate{Source: agentevents.ContextSource{
		AgentID: task.AgentID, CommandID: task.CommandID, ActionName: task.ActionName,
		Work: messages.WorkContext{OperationID: op.ID, TaskID: task.ID},
	}, Decision: &question}
	if err := update.Validate(); err != nil {
		return err
	}
	var forwardingErr error
	for _, recipient := range op.Tasks {
		if err := ctx.Err(); err != nil {
			return errors.Join(forwardingErr, err)
		}
		if recipient.ID == task.ID || recipient.OperationID != op.ID || recipient.Status != operations.TaskRunning ||
			recipient.DeadlineAt == nil || !time.Now().Before(*recipient.DeadlineAt) ||
			!sharesDecision(op.Mission.Policies.Sharing, task.ActionName, recipient.ActionName) {
			continue
		}
		forwardingErr = errors.Join(forwardingErr, w.publishSharedUpdate(ctx, recipient, update.Clone()))
	}
	return forwardingErr
}

func sharesDecision(policy policies.SharingPolicy, from, to string) bool {
	return slices.ContainsFunc(policy, func(route policies.SharingRoute) bool {
		return slices.Contains(route.FromActions, from) && slices.Contains(route.ToActions, to) && slices.Contains(route.Include, policies.ShareDecisions)
	})
}
