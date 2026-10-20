package operations

import (
	"fmt"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/id"
)

// AffinityAgentID returns the agent bound by the operation's same-agent rule,
// or an empty ID before the first matching dispatch. Failed, interrupted and
// cancelled assignments retain the binding; queued tasks never establish it.
func (o Operation) AffinityAgentID() (string, error) {
	if err := o.Mission.Rules.Validate(); err != nil {
		return "", err
	}
	if _, exists := o.Mission.Rules[missions.SameAgent]; !exists {
		return "", nil
	}
	var agentID string
	for _, task := range o.Tasks {
		if !ruleMatches(o.Mission.Rules, missions.SameAgent, task.ActionName) || task.AgentID == "" {
			continue
		}
		if task.Status == TaskQueued || task.DeadlineAt == nil || task.OperationID != o.ID || !id.ValidAgent(task.AgentID) {
			return "", fmt.Errorf("%w: same-agent task %s has an invalid dispatch", ErrOperationConflict, task.ID)
		}
		if agentID != "" && task.AgentID != agentID {
			return "", fmt.Errorf("%w: same-agent tasks are assigned to different agents", ErrOperationConflict)
		}
		agentID = task.AgentID
	}
	return agentID, nil
}
