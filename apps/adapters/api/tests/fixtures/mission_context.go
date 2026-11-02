package fixtures

import (
	"encoding/json"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/id"
)

func ContextMission() models.CreateMissionRequest {
	return models.CreateMissionRequest{
		Name:         "context-sharing-" + id.New(),
		Objectives:   []string{"Analyze vulnerability findings as they arrive."},
		InputSchema:  json.RawMessage(`{}`),
		OutputSchema: json.RawMessage(`{}`),
		Context: &models.MissionContextPolicyRequest{Sharing: []models.MissionContextSharingRequest{{
			FromActions: []string{"find-vulnerabilities"},
			ToActions:   []string{"analyze-vulnerabilities"},
			Include:     []string{"results", "artifacts", "decisions"},
		}}},
	}
}

func InvalidContextPolicies() []*models.MissionContextPolicyRequest {
	var policies []*models.MissionContextPolicyRequest
	for _, field := range []string{"from_actions", "to_actions", "include"} {
		policy := ContextMission().Context
		switch field {
		case "from_actions":
			policy.Sharing[0].FromActions = nil
		case "to_actions":
			policy.Sharing[0].ToActions = nil
		case "include":
			policy.Sharing[0].Include = nil
		}
		policies = append(policies, policy)
	}
	for _, names := range [][]string{{"find-vulnerabilities", "find-vulnerabilities"}, {" find-vulnerabilities"}, {""}} {
		policy := ContextMission().Context
		policy.Sharing[0].FromActions = names
		policies = append(policies, policy)
	}
	for _, kinds := range [][]string{{"results", "results"}, {"secrets"}} {
		policy := ContextMission().Context
		policy.Sharing[0].Include = kinds
		policies = append(policies, policy)
	}
	return policies
}
