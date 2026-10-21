package mappings

import (
	"bytes"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
)

func ToMission(request models.CreateMissionRequest) missions.Mission {
	mission := missions.Mission{
		Name:            request.Name,
		Objectives:      slices.Clone(request.Objectives),
		RequiredActions: slices.Clone(request.RequiredActions),
		OptionalActions: slices.Clone(request.OptionalActions),
		StartingActions: slices.Clone(request.StartingActions),
		InputSchema:     bytes.Clone(request.InputSchema),
		OutputSchema:    bytes.Clone(request.OutputSchema),
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
	if request.Steps != nil {
		mission.Steps = make([]missions.Step, len(request.Steps))
		for i, step := range request.Steps {
			mapped := missions.Step{
				ID:              step.ID,
				Objective:       step.Objective,
				RequiredActions: slices.Clone(step.RequiredActions),
			}
			if step.AgentTypes != nil {
				mapped.AgentTypes = make([]agents.AgentType, len(step.AgentTypes))
				for j, agentType := range step.AgentTypes {
					mapped.AgentTypes[j] = agents.AgentType(agentType)
				}
			}
			mission.Steps[i] = mapped
		}
	}
	return mission
}

func ToMissionResponse(mission missions.Mission) models.MissionResponse {
	response := models.MissionResponse{
		Name:            mission.Name,
		Objectives:      slices.Clone(mission.Objectives),
		RequiredActions: slices.Clone(mission.RequiredActions),
		OptionalActions: slices.Clone(mission.OptionalActions),
		StartingActions: slices.Clone(mission.StartingActions),
		InputSchema:     bytes.Clone(mission.InputSchema),
		OutputSchema:    bytes.Clone(mission.OutputSchema),
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
	if mission.Steps != nil {
		response.Steps = make([]models.MissionStepResponse, len(mission.Steps))
		for i, step := range mission.Steps {
			mapped := models.MissionStepResponse{
				ID:              step.ID,
				Objective:       step.Objective,
				RequiredActions: slices.Clone(step.RequiredActions),
			}
			if step.AgentTypes != nil {
				mapped.AgentTypes = make([]string, len(step.AgentTypes))
				for j, agentType := range step.AgentTypes {
					mapped.AgentTypes[j] = string(agentType)
				}
			}
			response.Steps[i] = mapped
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
