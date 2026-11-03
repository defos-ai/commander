package e2e

import (
	"encoding/json"
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
)

func (s *SessionStack) SessionMission(name string, groups ...policies.SessionPolicy) {
	s.t.Helper()
	definition := models.CreateMissionRequest{Name: name, Objectives: []string{"Exercise controller-managed resource bindings."},
		Policies:    &models.MissionPoliciesRequest{Session: make([]models.MissionSessionPolicyRequest, len(groups))},
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}
	for i, group := range groups {
		definition.Policies.Session[i] = models.MissionSessionPolicyRequest{CreateAction: group.CreateAction, UseActions: group.UseActions}
		definition.RequiredActions = append(definition.RequiredActions, group.CreateAction)
		definition.OptionalActions = append(definition.OptionalActions, group.UseActions...)
	}
	s.Request(http.MethodPost, "/api/v1/missions", definition, http.StatusCreated, nil)
}
