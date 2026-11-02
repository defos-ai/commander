package mappings

import (
	"bytes"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/missions"
)

func ToMission(request models.CreateMissionRequest) missions.Mission {
	mission := missions.Mission{
		Name:              request.Name,
		Objectives:        slices.Clone(request.Objectives),
		ActionDefinitions: ToActionDefinitions(request.ActionDefinitions),
		RequiredActions:   slices.Clone(request.RequiredActions),
		OptionalActions:   slices.Clone(request.OptionalActions),
		StartingActions:   slices.Clone(request.StartingActions),
		InputSchema:       bytes.Clone(request.InputSchema),
		OutputSchema:      bytes.Clone(request.OutputSchema),
	}
	if request.Policies != nil {
		mission.Policies = &missions.MissionPolicies{}
		if request.Policies.Sharing != nil {
			mission.Policies.Sharing = make(missions.SharingPolicy, len(request.Policies.Sharing))
			for i, sharing := range request.Policies.Sharing {
				mission.Policies.Sharing[i] = missions.SharingRoute{
					FromActions: slices.Clone(sharing.FromActions),
					ToActions:   slices.Clone(sharing.ToActions),
					Include:     sharingKinds(sharing.Include),
				}
			}
		}
	}
	if request.Completion != nil {
		mission.Completion = &missions.CompletionPolicy{DecisionBy: missions.Decision(request.Completion.DecisionBy)}
	}
	if request.Rules != nil {
		mission.Rules = make(missions.RuleSet, len(request.Rules))
		for name, rule := range request.Rules {
			mapped := missions.Rule{ForActions: slices.Clone(rule.ForActions)}
			if rule.Params != nil {
				mapped.Params = &missions.RuleParams{DecisionBy: missions.Decision(rule.Params.DecisionBy)}
			}
			mission.Rules[missions.RuleName(name)] = mapped
		}
	}
	return mission
}

func ToMissionResponse(mission missions.Mission) models.MissionResponse {
	response := models.MissionResponse{
		Name:              mission.Name,
		Objectives:        slices.Clone(mission.Objectives),
		ActionDefinitions: ToActionDefinitionResponses(mission.ActionDefinitions),
		RequiredActions:   slices.Clone(mission.RequiredActions),
		OptionalActions:   slices.Clone(mission.OptionalActions),
		StartingActions:   slices.Clone(mission.StartingActions),
		InputSchema:       bytes.Clone(mission.InputSchema),
		OutputSchema:      bytes.Clone(mission.OutputSchema),
	}
	if mission.Policies != nil {
		response.Policies = &models.MissionPoliciesResponse{}
		if mission.Policies.Sharing != nil {
			response.Policies.Sharing = make([]models.MissionSharingPolicyResponse, len(mission.Policies.Sharing))
			for i, sharing := range mission.Policies.Sharing {
				response.Policies.Sharing[i] = models.MissionSharingPolicyResponse{
					FromActions: slices.Clone(sharing.FromActions),
					ToActions:   slices.Clone(sharing.ToActions),
					Include:     sharingKindNames(sharing.Include),
				}
			}
		}
	}
	if mission.Completion != nil {
		response.Completion = &models.MissionCompletionPolicyResponse{DecisionBy: string(mission.Completion.DecisionBy)}
	}
	if mission.Rules != nil {
		response.Rules = make(map[string]models.MissionRuleResponse, len(mission.Rules))
		for name, rule := range mission.Rules {
			mapped := models.MissionRuleResponse{ForActions: slices.Clone(rule.ForActions)}
			if rule.Params != nil {
				mapped.Params = &models.MissionRuleParamsResponse{DecisionBy: string(rule.Params.DecisionBy)}
			}
			response.Rules[string(name)] = mapped
		}
	}
	return response
}

func ToMissionResponses(missions []missions.Mission) []models.MissionResponse {
	if missions == nil {
		return nil
	}
	responses := make([]models.MissionResponse, len(missions))
	for i, mission := range missions {
		responses[i] = ToMissionResponse(mission)
	}
	return responses
}

func sharingKinds(names []string) []missions.SharingKind {
	if names == nil {
		return nil
	}
	kinds := make([]missions.SharingKind, len(names))
	for i, name := range names {
		kinds[i] = missions.SharingKind(name)
	}
	return kinds
}

func sharingKindNames(kinds []missions.SharingKind) []string {
	if kinds == nil {
		return nil
	}
	names := make([]string, len(kinds))
	for i, kind := range kinds {
		names[i] = string(kind)
	}
	return names
}
