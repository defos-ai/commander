package operations

import (
	"fmt"
	"slices"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/id"
)

// Affinity is a read-only view of a saved same-agent rule and its dispatch history.
// An error leaves the history inspectable without suggesting the group is unbound.
type Affinity struct {
	ForActions []string `json:"for_actions"`
	AgentID    string   `json:"agent_id,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func (o Operation) AssessAffinity() *Affinity {
	rule, exists := o.Mission.Rules[missions.SameAgent]
	if !exists {
		return nil
	}
	result := &Affinity{ForActions: slices.Clone(rule.ForActions)}
	agentID, err := o.AffinityAgentID()
	if err != nil {
		result.Error = err.Error()
	} else {
		result.AgentID = agentID
	}
	return result
}

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
