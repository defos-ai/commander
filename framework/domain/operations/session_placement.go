package operations

import (
	"fmt"
	"slices"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

type TaskPlacement struct {
	AgentType           string                `json:"agent_type,omitempty"`
	MissingRequirements []actions.Requirement `json:"missing_requirements,omitempty"`
	TaskID              string                `json:"task_id"`
	OwnerAgentID        string                `json:"owner_agent_id,omitempty"`
	Reason              string                `json:"reason,omitempty"`
}

func findSession(scopes []sessions.Scope, intent *sessions.Intent) (sessions.Record, error) {
	for _, scope := range scopes {
		if scope.ID == intent.ScopeID {
			record, err := scope.Find(intent.ID)
			if err != nil && intent.Mode == sessions.Create && scope.Current != "" {
				return record, sessions.ErrScopeReserved
			}
			return record, err
		}
	}
	return sessions.Record{}, sessions.ErrUnknownSession
}

// SessionPlacement is shared by scheduling and public preflight. Accepted session
// intent constrains only its own task to the resource's exact owner.
func (o Operation) SessionPlacement(task Task, records []agents.Agent, scopes []sessions.Scope) TaskPlacement {
	result := TaskPlacement{TaskID: task.ID}
	owner, err := sessionConstraint(task, scopes, true)
	result.OwnerAgentID = owner
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	if owner == "" {
		seen, compatible := false, false
		for _, record := range records {
			for _, a := range record.Actions {
				if a.Name == task.ActionName {
					seen = true
					compatible = compatible || task.MatchesSessionMode(a.SessionMode)
				}
			}
		}
		if seen && !compatible {
			result.Reason = "action_session_contract_changed"
		}
		return result
	}
	for _, record := range records {
		if record.ID != owner {
			continue
		}
		if record.Stale || record.Status != agents.AgentReady && record.Status != agents.AgentWorking {
			result.Reason = "owner_unavailable"
			return result
		}
		for _, a := range record.Actions {
			if a.Name == task.ActionName {
				if !task.MatchesSessionMode(a.SessionMode) {
					result.Reason = "action_session_contract_changed"
					return result
				}
				assessment := AssessAgentAction(record, a)
				result.Reason = assessment.Reason
				result.AgentType = assessment.AgentType
				result.MissingRequirements = slices.Clone(assessment.MissingRequirements)
				return result
			}
		}
		result.Reason = "session owner does not provide the action"
		return result
	}
	result.Reason = "owner_unavailable"
	return result
}

// MatchesSessionMode checks static compatibility without reinterpreting saved intent.
func (t Task) MatchesSessionMode(mode string) bool {
	if t.SessionIntent == nil {
		return mode == ""
	}
	if t.SessionIntent.Error != "" {
		return false
	}
	if t.SessionIntent.Mode == sessions.Use {
		return mode == "use"
	}
	return mode == "create"
}

func sessionConstraint(task Task, scopes []sessions.Scope, check bool) (string, error) {
	intent := task.SessionIntent
	if intent == nil {
		return "", nil
	}
	if err := intent.Validate(); err != nil {
		return "", err
	}
	record, err := findSession(scopes, intent)
	if err != nil {
		return "", err
	}
	owner := record.OwnerAgentID
	if !check {
		return owner, nil
	}
	if err := record.Available(); err != nil {
		return owner, err
	}
	if intent.Mode == sessions.Create {
		if record.CommandID != task.CommandID || record.OperationID != task.OperationID || record.TaskID != task.ID {
			return owner, sessions.ErrConflict
		}
		return owner, nil
	}
	if owner == "" {
		return "", fmt.Errorf("owner_unavailable")
	}
	if record.Observation == nil {
		return owner, fmt.Errorf("session_not_usable")
	}
	state := record.Observation.State
	if state == "lost" || state == "closed" || state == "failed" {
		return owner, fmt.Errorf("session_not_usable: %s", state)
	}
	if intent.Mode == sessions.Use && state != "connected" {
		return owner, fmt.Errorf("session_not_usable: verification required")
	}
	return owner, nil
}
