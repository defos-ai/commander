package runner

import (
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

// Session routing is a restriction on eligible candidates, never a selector
// preference. Losing the owning process must not redirect stateful work.
func sessionOwner(task operations.Task, records []agents.Agent) (string, error) {
	mode := ""
	seen := false
	for _, record := range records {
		for _, action := range record.Actions {
			if action.Name == task.ActionName {
				if seen && mode != action.SessionMode {
					return "", errors.New("conflicting session action metadata")
				}
				mode = action.SessionMode
				seen = true
			}
		}
	}
	if mode == "" {
		return "", nil
	}
	var input struct {
		SessionID string `json:"session_id"`
		ScopeID   string `json:"session_scope"`
	}
	if err := json.Unmarshal(task.Input, &input); err != nil {
		return "", err
	}
	if !id.Valid(input.ScopeID) || input.SessionID != "" && !id.Valid(input.SessionID) {
		return "", errors.New("session action requires a canonical session_scope and valid session_id")
	}
	if mode == "use" && input.SessionID == "" {
		return "", errors.New("session_id is required")
	}
	owner := ""
	for _, record := range records {
		for _, item := range record.Sessions {
			if item.ScopeID != input.ScopeID {
				continue
			}
			if input.SessionID != "" && item.ID != input.SessionID {
				continue
			}
			if input.SessionID == "" && (item.State == "closed" || item.State == "failed") {
				continue
			}
			if item.OwnerAgentID != record.ID {
				return "", errors.New("invalid session owner")
			}
			if owner != "" && owner != record.ID {
				return "", errors.New("session scope has conflicting owners")
			}
			if record.Stale || record.Status == agents.AgentStopped || item.State == "lost" {
				return "", errors.New("session owner unavailable; close this session before replacing it")
			}
			if mode == "create" && input.SessionID == "" {
				return "", errors.New("close the current session before connecting another")
			}
			if mode == "use" && item.State != "connected" {
				return "", errors.New("session is not verified and connected")
			}
			owner = record.ID
		}
	}
	if owner == "" && (mode == "use" || input.SessionID != "") {
		return "", errors.New("unknown session; owner routing cannot be established")
	}
	return owner, nil
}

// The runner serializes admissions. Reserve the scope while listener creation
// is queued/running, before its first inventory snapshot can reach the controller.
func (r *Runner) checkSessionCreation(task operations.Task, records []agents.Agent) error {
	scope, err := creationScope(task, records)
	if err != nil || scope == "" {
		return err
	}
	for _, state := range r.states {
		for _, other := range state.operation.Tasks {
			if other.ID == task.ID || other.Status != operations.TaskQueued && other.Status != operations.TaskRunning {
				continue
			}
			otherScope, err := creationScope(other, records)
			if err != nil {
				return err
			}
			if otherScope == scope {
				return errors.New("a session connection request is already pending for this scope")
			}

		}
	}
	return nil
}

func creationScope(task operations.Task, records []agents.Agent) (string, error) {
	isCreate := false
	for _, record := range records {
		for _, action := range record.Actions {
			if action.Name == task.ActionName && action.SessionMode == "create" {
				isCreate = true
			}
		}
	}
	if !isCreate {
		return "", nil
	}
	var input struct {
		Scope string `json:"session_scope"`
		ID    string `json:"session_id"`
	}
	if err := json.Unmarshal(task.Input, &input); err != nil {
		return "", err
	}
	if input.ID != "" {
		return "", nil
	}
	return input.Scope, nil
}
