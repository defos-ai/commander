package mappings

import (
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

func ToRuntimeSessionResponse(value sessions.Session) models.RuntimeSessionResponse {
	s := value.Clone()
	return models.RuntimeSessionResponse{ID: s.ID, OwnerAgentID: s.OwnerAgentID, ScopeID: s.ScopeID,
		Kind: s.Kind, Label: s.Label, State: s.State, Endpoint: s.Endpoint, Peer: s.Peer,
		Hostname: s.Hostname, User: s.User, Reason: s.Reason, Busy: s.Busy, Revision: s.Revision,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, ConnectedAt: s.ConnectedAt, VerifiedAt: s.VerifiedAt}
}

func ToRuntimeSessionResponses(values []sessions.Session) []models.RuntimeSessionResponse {
	if values == nil {
		return nil
	}
	result := make([]models.RuntimeSessionResponse, len(values))
	for i := range values {
		result[i] = ToRuntimeSessionResponse(values[i])
	}
	return result
}
