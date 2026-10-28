package fixtures

import (
	"encoding/json"

	"github.com/defos-ai/commander/framework/domain/actions"
)

func ActionDefinition() actions.Definition {
	return actions.Definition{
		Name: "discover", Description: "Discover the requested target", RequiredSecrets: []string{"DISCOVERY_KEY"},
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","maximum":9007199254740993}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","x-example":9007199254740993}`),
	}
}
