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
