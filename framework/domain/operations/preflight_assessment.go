package operations

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

// AssessPreflight checks mission requirements and queued optional work against
// saved affinity. Capacity is separate, so working agents can provide coverage.
func (o Operation) AssessPreflight(records []agents.Agent, sessionScopes ...[]sessions.Scope) (*Preflight, error) {
	boundAgentID, err := o.AffinityAgentID()
	if err != nil {
		return nil, err
	}
	group := o.Mission.Rules[missions.SameAgent].ForActions
	commonAgents := make(map[string]bool)
	if len(group) != 0 && boundAgentID == "" {
		for _, record := range records {
			commonAgents[record.ID] = providesActions(record, group)
		}
	}
	names := slices.Clone(o.Mission.RequiredActions)
	for _, step := range o.Mission.Steps {
		names = append(names, step.RequiredActions...)
	}
	// Include selected optional work so missing requirements are visible while
	// it waits for dispatch.
	for _, task := range o.Tasks {
		if task.Status == TaskQueued && (slices.Contains(o.Mission.OptionalActions, task.ActionName) || task.SessionIntent != nil) {
			names = append(names, task.ActionName)
		}
	}
	names = append(names, group...)
	slices.Sort(names)
	names = slices.Compact(names)
	var scopes []sessions.Scope
	if len(sessionScopes) > 0 {
		scopes = sessionScopes[0]
	}
	result := &Preflight{Ready: true, Version: 1, Actions: make([]ActionReadiness, 0, len(names))}
	for _, task := range o.Tasks {
		if task.Status == TaskQueued {
			result.Tasks = append(result.Tasks, o.SessionPlacement(task, records, scopes))
		}
	}
	for _, name := range names {
		constraintOwner, constraintErr := o.sessionConstraint(Task{ActionName: name}, scopes)
		for _, task := range o.Tasks {
			if task.Status == TaskQueued && task.ActionName == name && slices.Contains(group, name) && task.SessionIntent != nil && task.SessionIntent.Mode != sessions.Create {
				owner, err := o.sessionConstraint(task, scopes)
				if err != nil {
					constraintErr = err
				}
				if constraintOwner != "" && owner != "" && constraintOwner != owner {
					constraintErr = fmt.Errorf("placement_conflict: action tasks require different session owners")
				}
				if owner != "" {
					constraintOwner = owner
				}
			}
		}
		owners := map[string]bool{}
		if !slices.Contains(group, name) {
			for _, task := range o.Tasks {
				if task.Status == TaskQueued && task.ActionName == name && task.SessionIntent != nil && task.SessionIntent.Mode != sessions.Create {
					owner, _ := sessionConstraint(task, scopes, false)
					if owner != "" {
						owners[owner] = true
					}
				}
			}
		}
		readiness := ActionReadiness{ActionName: name}
		grouped := slices.Contains(group, name)
		foundOwner := false
		for _, record := range records {
			if len(owners) > 0 && !owners[record.ID] {
				continue
			}
			if constraintOwner != "" && record.ID != constraintOwner {
				continue
			}
			if grouped && boundAgentID != "" && record.ID != boundAgentID {
				continue
			}
			foundOwner = foundOwner || record.ID == boundAgentID
			index := slices.IndexFunc(record.Actions, func(action actions.Action) bool { return action.Name == name })
			if index < 0 {
				if grouped && boundAgentID != "" {
					readiness.UnavailableAgents = append(readiness.UnavailableAgents, AgentReadiness{
						AgentID: record.ID, AgentType: string(record.Type), Reason: "Agent required by same-agent does not provide this action",
					})
				}
				continue
			}
			var candidate AgentReadiness
			if constraintErr != nil {
				readiness.UnavailableAgents = append(readiness.UnavailableAgents, AgentReadiness{AgentID: record.ID, Reason: constraintErr.Error()})
				continue
			}
			if grouped && boundAgentID == "" && !commonAgents[record.ID] {
				candidate = AgentReadiness{
					AgentID: record.ID, AgentType: string(record.Type), Reason: "Agent does not provide all actions required by same-agent",
				}
			} else {
				candidate = AssessAgentAction(record, record.Actions[index])
			}
			if candidate.Reason == "" {
				readiness.ReadyAgentIDs = append(readiness.ReadyAgentIDs, record.ID)
			} else {
				readiness.UnavailableAgents = append(readiness.UnavailableAgents, candidate)
			}
		}
		if grouped && boundAgentID != "" && !foundOwner {
			readiness.UnavailableAgents = append(readiness.UnavailableAgents, AgentReadiness{
				AgentID: boundAgentID, Reason: "Agent required by same-agent is not registered",
			})
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

func providesActions(record agents.Agent, names []string) bool {
	for _, name := range names {
		if !slices.ContainsFunc(record.Actions, func(a actions.Action) bool { return a.Name == name }) {
			return false
		}
	}
	return true
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
