package runner

import (
	"context"
	"reflect"
	"slices"
	"sort"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
)

func assessPreflight(mission missions.Mission, records []agents.Agent) *operations.Preflight {
	names := slices.Clone(mission.RequiredActions)
	for _, step := range mission.Steps {
		names = append(names, step.RequiredActions...)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	result := &operations.Preflight{Ready: true, Version: 1, Actions: make([]operations.ActionReadiness, 0, len(names))}
	for _, name := range names {
		readiness := operations.ActionReadiness{ActionName: name}
		for _, record := range records {
			for _, action := range record.Actions {
				if action.Name != name {
					continue
				}
				candidate := assessAgentAction(record, action)
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

func assessAgentAction(agent agents.Agent, action actions.Action) operations.AgentReadiness {
	result := operations.AgentReadiness{AgentID: agent.ID, MissingRequirements: slices.Clone(action.MissingRequirements)}
	for _, permission := range action.RequiredPermissions {
		requirement := actions.Requirement{Kind: actions.PermissionRequirement, Name: permission}
		if !slices.Contains(agent.GrantedPermissions, permission) && !slices.Contains(result.MissingRequirements, requirement) {
			result.MissingRequirements = append(result.MissingRequirements, requirement)
		}
	}
	switch {
	case agent.Stale:
		result.Reason = "Agent heartbeat has expired"
	case agent.Status != agents.AgentReady && agent.Status != agents.AgentWorking:
		result.Reason = "Agent is not ready"
	case len(result.MissingRequirements) != 0:
		result.Reason = "Action has missing requirements"
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

func (r *Runner) refreshPreflight(ctx context.Context, state *operationState, records []agents.Agent) error {
	if r.catalog == nil {
		return nil
	}
	preflight := assessPreflight(state.operation.Mission, records)
	if previous := state.operation.Preflight; previous != nil {
		if preflight.Ready == previous.Ready && reflect.DeepEqual(preflight.Actions, previous.Actions) {
			return nil
		}
		preflight.Version = previous.Version + 1
	}
	next := state.operation.Clone()
	next.Preflight = preflight
	refreshStatus(&next)
	message := "Mission requirements are ready"
	if !preflight.Ready {
		message = "Mission requirements are unavailable; inspect preflight before continuing"
	}
	appendEvent(&next, operations.PreflightUpdated, "", "", message)
	return r.save(ctx, state, next)
}
