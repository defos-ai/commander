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
	if mission.Automations != nil {
		response.Automations = make(map[string]models.MissionAutomationResponse, len(mission.Automations))
		for name, automation := range mission.Automations {
			response.Automations[name] = models.MissionAutomationResponse{
				Run:      slices.Clone(automation.Run),
				Continue: string(automation.Continue),
				Decide:   string(automation.Decide),
			}
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
