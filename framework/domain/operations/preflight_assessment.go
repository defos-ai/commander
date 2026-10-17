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

func AssessPreflight(mission missions.Mission, records []agents.Agent) *Preflight {
	names := slices.Clone(mission.RequiredActions)
	for _, step := range mission.Steps {
		names = append(names, step.RequiredActions...)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	result := &Preflight{Ready: true, Version: 1, Actions: make([]ActionReadiness, 0, len(names))}
	for _, name := range names {
		readiness := ActionReadiness{ActionName: name}
		for _, record := range records {
			for _, action := range record.Actions {
				if action.Name != name {
					continue
				}
				candidate := AssessAgentAction(record, action)
				if candidate.Reason == "" {
					readiness.ReadyAgentIDs = append(readiness.ReadyAgentIDs, record.ID)
				} else {
					readiness.UnavailableAgents = append(readiness.UnavailableAgents, candidate)
				}
				break
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
	return result
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
	next := AssessPreflight(o.Mission, records)
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
