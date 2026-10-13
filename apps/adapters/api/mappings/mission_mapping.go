package mappings

import (
	"bytes"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/missions"
)

func ToMissionResponse(mission missions.Mission) models.MissionResponse {
	response := models.MissionResponse{
		Name:            mission.Name,
		Objective:       mission.Objective,
		RequiredActions: slices.Clone(mission.RequiredActions),
		StartingActions: slices.Clone(mission.StartingActions),
		InputSchema:     bytes.Clone(mission.InputSchema),
		OutputSchema:    bytes.Clone(mission.OutputSchema),
	}
	if mission.Rules != nil {
		response.Rules = make(map[string]models.MissionRuleResponse, len(mission.Rules))
		for name, rule := range mission.Rules {
			mapped := models.MissionRuleResponse{Actions: slices.Clone(rule.Actions)}
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
