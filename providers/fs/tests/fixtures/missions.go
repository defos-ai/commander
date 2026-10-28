package fixtures

import (
	"encoding/json"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
)

func Mission() missions.Mission {
	return missions.Mission{
		Name: "review", Objectives: []string{"Review a target", "Summarize findings"}, RequiredActions: []string{"inspect"},
		StartingActions: []string{"inspect"}, OptionalActions: []string{"publish"},
		ActionDefinitions: []actions.Definition{{
			Name: "inspect", Description: "Inspect the requested target", RequiredSecrets: []string{"INSPECTION_KEY"},
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","maximum":9007199254740993}}}`),
			OutputSchema: json.RawMessage(`{"type":"object","x-example":9007199254740993}`),
		}},
		Rules:       missions.RuleSet{missions.AggregateResults: {ForActions: []string{"inspect"}}},
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
}
