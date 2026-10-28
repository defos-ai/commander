package fixtures

import (
	"time"

	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/domain/actions"
)

const ExampleMission = `{
  "name":"analyze-text",
  "objectives":["Analyze text and save a summary artifact.","Ask the operator to accept the result before completing."],
  "required_actions":["analyze-text","write-summary"],
  "starting_actions":["analyze-text"],
  "rules":{
    "aggregate-results":{"for_actions":["analyze-text"]},
    "decide-next-actions":{"for_actions":["write-summary"],"params":{"decision_by":"model"}}
  },
  "input_schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false},
  "output_schema":{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}
}`

func SecretFormOperation(names ...string) bridge.Operation {
	var requirements []actions.Requirement
	for _, name := range names {
		requirements = append(requirements, actions.Requirement{Kind: actions.SecretRequirement, Name: name})
	}
	return bridge.Operation{ID: "secret-form-operation", Status: "waiting", UpdatedAt: time.Now(), Progress: bridge.Progress{TaskCounts: map[string]int{}}, Preflight: &bridge.Preflight{Version: 1, Actions: []bridge.ActionReadiness{{UnavailableAgents: []bridge.AgentReadiness{{AgentType: "builder-agent", MissingRequirements: requirements}}}}}}
}

func RuntimeSecretFormOperation(names ...string) bridge.Operation {
	var requirements []actions.Requirement
	for _, name := range names {
		requirements = append(requirements, actions.Requirement{Kind: actions.SecretRequirement, Name: name})
	}
	deadline := time.Now().Add(time.Minute)
	return bridge.Operation{ID: "secret-form-operation", Status: "running", UpdatedAt: time.Now(), Progress: bridge.Progress{TaskCounts: map[string]int{"running": 1}, TotalTasks: 1}, Tasks: []bridge.Task{{
		ID: "running-task", Status: "running", ActionName: "build", DeadlineAt: &deadline, AgentType: "builder-agent", MissingRequirements: requirements,
	}}}
}
