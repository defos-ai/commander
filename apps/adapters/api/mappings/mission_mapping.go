package mappings

import (
	"bytes"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
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
		mission.Policies = &policies.MissionPolicies{}
		if request.Policies.Completion != nil {
			mission.Policies.Completion = &policies.CompletionPolicy{DecisionBy: policies.Decision(request.Policies.Completion.DecisionBy)}
		}
		if request.Policies.Continuation != nil {
			mission.Policies.Continuation = &policies.ContinuationPolicy{
				AggregateResults: slices.Clone(request.Policies.Continuation.AggregateResults),
				ModelReview:      slices.Clone(request.Policies.Continuation.ModelReview),
			}
		}
		if request.Policies.Sharing != nil {
			mission.Policies.Sharing = make(policies.SharingPolicy, len(request.Policies.Sharing))
			for i, sharing := range request.Policies.Sharing {
				mission.Policies.Sharing[i] = policies.SharingRoute{
					FromActions: slices.Clone(sharing.FromActions),
					ToActions:   slices.Clone(sharing.ToActions),
					Include:     sharingKinds(sharing.Include),
				}
			}
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
		if mission.Policies.Completion != nil {
			response.Policies.Completion = &models.MissionCompletionPolicyResponse{DecisionBy: string(mission.Policies.Completion.DecisionBy)}
		}
		if mission.Policies.Continuation != nil {
			response.Policies.Continuation = &models.MissionContinuationPolicyResponse{
				AggregateResults: slices.Clone(mission.Policies.Continuation.AggregateResults),
				ModelReview:      slices.Clone(mission.Policies.Continuation.ModelReview),
			}
		}
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

func sharingKinds(names []string) []policies.SharingKind {
	if names == nil {
		return nil
	}
	kinds := make([]policies.SharingKind, len(names))
	for i, name := range names {
		kinds[i] = policies.SharingKind(name)
	}
	return kinds
}

func sharingKindNames(kinds []policies.SharingKind) []string {
	if kinds == nil {
		return nil
	}
	names := make([]string, len(kinds))
	for i, kind := range kinds {
		names[i] = string(kind)
	}
	return names
}
