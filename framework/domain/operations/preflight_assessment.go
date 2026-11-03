package operations

import (
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

// AssessPreflight checks mission requirements and queued optional work.
// Capacity is separate, so working agents can provide coverage.
func (o Operation) AssessPreflight(records []agents.Agent, sessionScopes ...[]sessions.Scope) (*Preflight, error) {
	if o.Mission.Policies != nil {
		if err := o.Mission.Policies.Validate(); err != nil {
			return nil, err
		}
	}
	names := slices.Clone(o.Mission.RequiredActions)
	for _, task := range o.Tasks {
		if task.Status == TaskQueued && (slices.Contains(o.Mission.OptionalActions, task.ActionName) || task.SessionIntent != nil) {
			names = append(names, task.ActionName)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	var scopes []sessions.Scope
	if len(sessionScopes) > 0 {
		scopes = sessionScopes[0]
	}
	result := &Preflight{Ready: true, Version: 1, Actions: make([]ActionReadiness, 0, len(names))}
	for _, task := range o.Tasks {
		if task.Status == TaskQueued {
			placement := o.SessionPlacement(task, records, scopes)
			// Unconstrained queue changes do not change readiness. Retain session
			// intent, owner restrictions and blockers as meaningful placement data.
			if task.SessionIntent != nil || placement.OwnerAgentID != "" || placement.Reason != "" {
				result.Tasks = append(result.Tasks, placement)
			}
		}
	}
	for _, name := range names {
		owners := map[string]bool{}
		for _, task := range o.Tasks {
			if task.Status == TaskQueued && task.ActionName == name && task.SessionIntent != nil && task.SessionIntent.Mode != sessions.Create {
				owner, _ := sessionConstraint(task, scopes, false)
				if owner != "" {
					owners[owner] = true
				}
			}
		}
		readiness := ActionReadiness{ActionName: name}
		for _, record := range records {
			if len(owners) > 0 && !owners[record.ID] {
				continue
			}
			index := slices.IndexFunc(record.Actions, func(action actions.Action) bool { return action.Name == name })
			if index < 0 {
				continue
			}
			candidate := AgentReadiness{AgentID: record.ID, AgentType: string(record.Type), Reason: "Agent does not provide the session policy's consumer contracts"}
			if o.SessionPolicyCompatible(Task{ActionName: name}, record) {
				candidate = AssessAgentAction(record, record.Actions[index])
			}
			if candidate.Reason == "" {
				readiness.ReadyAgentIDs = append(readiness.ReadyAgentIDs, record.ID)
			} else {
				readiness.UnavailableAgents = append(readiness.UnavailableAgents, candidate)
			}
		}
		if len(readiness.ReadyAgentIDs) != 0 {
			slices.Sort(readiness.ReadyAgentIDs)
			readiness.UnavailableAgents = nil
		} else {
			result.Ready = false
			sort.Slice(readiness.UnavailableAgents, func(i, j int) bool {
				return readiness.UnavailableAgents[i].AgentID < readiness.UnavailableAgents[j].AgentID
			})
		}
		result.Actions = append(result.Actions, readiness)
	}
	return result, nil
}

func AssessAgentAction(agent agents.Agent, action actions.Action) AgentReadiness {
	result := AgentReadiness{AgentID: agent.ID, AgentType: string(agent.Type)}
	switch {
	case agent.Stale:
		result.Reason = "Agent heartbeat has expired"
	case agent.Status != agents.AgentReady && agent.Status != agents.AgentWorking:
		result.Reason = "Agent is not ready"
	case len(action.MissingRequirements) != 0:
		result.Reason = "Action has missing requirements"
		// Stale and non-ready agents retain their last requirements in inventory,
		// but those snapshots must not become actionable operation guidance.
		result.MissingRequirements = slices.Clone(action.MissingRequirements)
	case action.Status == actions.Unknown || action.Status == "":
		result.Reason = "Action has not been discovered"
	case action.Status != actions.Ready:
		result.Reason = action.StatusReason
		if result.Reason == "" {
			result.Reason = "Action is unavailable"
		}
	}
	return result
}

func (o *Operation) RefreshPreflight(records []agents.Agent, scopes ...[]sessions.Scope) (bool, error) {
	if o.Status.Terminal() {
		return false, ErrFinished
	}
	next, err := o.AssessPreflight(records, scopes...)
	if err != nil {
		return false, err
	}
	if previous := o.Preflight; previous != nil {
		if next.Ready == previous.Ready && reflect.DeepEqual(next.Actions, previous.Actions) && reflect.DeepEqual(next.Tasks, previous.Tasks) {
			return false, nil
		}
		next.Version = previous.Version + 1
	}
	o.Preflight = next
	o.RefreshStatus()
	message := "Mission requirements are ready"
	if !next.Ready {
		message = "Mission requirements are unavailable; inspect preflight before continuing"
	}
	o.AppendEvent(PreflightUpdated, "", "", message)
	o.UpdatedAt = time.Now().UTC()
	return true, nil
}
