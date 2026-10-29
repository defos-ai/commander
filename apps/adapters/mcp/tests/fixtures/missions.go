package fixtures

import "github.com/defos-ai/commander/framework/domain/missions"

func MissionDefinition(mission missions.Mission) Object {
	return Object{
		"name": mission.Name, "objectives": mission.Objectives,
		"required_actions": mission.RequiredActions, "optional_actions": mission.OptionalActions,
		"starting_actions": mission.StartingActions, "action_definitions": mission.ActionDefinitions,
		"rules": mission.Rules, "completion": mission.Completion,
		"input_schema": mission.InputSchema, "output_schema": mission.OutputSchema,
	}
}

func (a *Client) CancelActiveOperation(id string) {
	a.t.Helper()
	if operation := a.Get(id); !operation.Status.Terminal() {
		a.Call("cancel_operation", Object{"operation_id": id}, nil)
	}
}
