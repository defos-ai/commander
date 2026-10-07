package operations

import actionmodel "github.com/defos-ai/commander/framework/domain/actions"

type DecisionBy string

const (
	DecisionCode  DecisionBy = "code"
	DecisionModel DecisionBy = "model"
	DecisionUser  DecisionBy = "user"
)

type ContinuationStatus string

const (
	ContinuationPending  ContinuationStatus = "pending"
	ContinuationWaiting  ContinuationStatus = "waiting"
	ContinuationResolved ContinuationStatus = "resolved"
)

// A task owns one versioned continuation. Waiting code continuations collect
// their children before applying automation rules or requesting a model decision.
type Continuation struct {
	Version         uint64                   `json:"version"`
	DecisionBy      DecisionBy               `json:"decision_by"`
	Status          ContinuationStatus       `json:"status"`
	ProposedActions []actionmodel.NextAction `json:"proposed_actions"`
	CreatedTaskIDs  []string                 `json:"created_task_ids"`
	QuestionID      string                   `json:"question_id,omitempty"`
}

func (c Continuation) Clone() Continuation {
	c.ProposedActions = actionmodel.CloneNextActions(c.ProposedActions)
	c.CreatedTaskIDs = append([]string{}, c.CreatedTaskIDs...)
	return c
}
