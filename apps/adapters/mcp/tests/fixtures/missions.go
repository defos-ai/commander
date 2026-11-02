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
		"rules": mission.Rules, "completion": mission.Completion, "policies": mission.Policies,
		"input_schema": mission.InputSchema, "output_schema": mission.OutputSchema,
	}
}

func (a *Client) CancelActiveOperation(id string) {
	a.t.Helper()
	if operation := a.Get(id); !operation.Status.Terminal() {
		a.Call("cancel_operation", Object{"operation_id": id}, nil)
	}
}

func SharingMission() missions.Mission {
	return missions.Mission{
		Name:         "sharing-" + id.New(),
		Objectives:   []string{"Analyze vulnerability findings as they arrive."},
		InputSchema:  json.RawMessage(`{}`),
		OutputSchema: json.RawMessage(`{}`),
		Policies: &missions.MissionPolicies{Sharing: missions.SharingPolicy{{
			FromActions: []string{"find-vulnerabilities"},
			ToActions:   []string{"analyze-vulnerabilities"},
			Include:     []missions.SharingKind{missions.ShareResults, missions.ShareArtifacts},
		}}},
	}
}
