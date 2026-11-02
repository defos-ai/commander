package fixtures

import (
	"encoding/json"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/id"
)

func MissionDefinition(mission missions.Mission) Object {
	return Object{
		"name": mission.Name, "objectives": mission.Objectives,
		"required_actions": mission.RequiredActions, "optional_actions": mission.OptionalActions,
		"starting_actions": mission.StartingActions, "action_definitions": mission.ActionDefinitions,
		"rules": mission.Rules, "completion": mission.Completion, "context": mission.Context,
		"input_schema": mission.InputSchema, "output_schema": mission.OutputSchema,
	}
}

func (a *Client) CancelActiveOperation(id string) {
	a.t.Helper()
	if operation := a.Get(id); !operation.Status.Terminal() {
		a.Call("cancel_operation", Object{"operation_id": id}, nil)
	}
}

func ContextMission() missions.Mission {
	return missions.Mission{
		Name:         "context-sharing-" + id.New(),
		Objectives:   []string{"Analyze vulnerability findings as they arrive."},
		InputSchema:  json.RawMessage(`{}`),
		OutputSchema: json.RawMessage(`{}`),
		Context: &missions.ContextPolicy{Sharing: []missions.ContextSharing{{
			FromActions: []string{"find-vulnerabilities"},
			ToActions:   []string{"analyze-vulnerabilities"},
			Include:     []missions.ContextKind{missions.ContextResults, missions.ContextArtifacts},
		}}},
	}
}
