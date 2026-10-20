package operations

import (
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
)

// AssessPreflight checks mission requirements against the operation's saved
// affinity. Capacity remains a scheduler concern, so working agents can be ready.
func (o Operation) AssessPreflight(records []agents.Agent) (*Preflight, error) {
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
	names = append(names, group...)
	slices.Sort(names)
	names = slices.Compact(names)
	result := &Preflight{Ready: true, Version: 1, Actions: make([]ActionReadiness, 0, len(names))}
	for _, name := range names {
		readiness := ActionReadiness{ActionName: name}
		grouped := slices.Contains(group, name)
		foundOwner := false
		for _, record := range records {
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
		if !slices.ContainsFunc(record.Actions, func(action actions.Action) bool { return action.Name == name }) {
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

func (o *Operation) RefreshPreflight(records []agents.Agent) (bool, error) {
	if o.Status.Terminal() {
		return false, ErrFinished
	}
	next, err := o.AssessPreflight(records)
	if err != nil {
		return false, err
	}
	if previous := o.Preflight; previous != nil {
		if next.Ready == previous.Ready && reflect.DeepEqual(next.Actions, previous.Actions) {
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
