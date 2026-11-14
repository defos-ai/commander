package readiness

import (
	"slices"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

type ActionSummary struct {
	Name                string                    `json:"name"`
	SessionMode         string                    `json:"session_mode,omitempty"`
	Status              actionmodel.Status        `json:"status"`
	StatusReason        string                    `json:"status_reason,omitempty"`
	MissingRequirements []actionmodel.Requirement `json:"missing_requirements,omitempty"`
	RequiredSecrets     []string                  `json:"required_secrets,omitempty"`
}

func SummarizeActions(advertised []actionmodel.Action) []ActionSummary {
	readiness := make([]ActionSummary, len(advertised))
	for i, definition := range advertised {
		readiness[i] = ActionSummary{
			Name: definition.Name, SessionMode: definition.SessionMode, Status: definition.Status,
			StatusReason:        definition.StatusReason,
			MissingRequirements: slices.Clone(definition.MissingRequirements),
			RequiredSecrets:     slices.Clone(definition.RequiredSecrets),
		}
	}
	return readiness
}
