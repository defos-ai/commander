package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/apps/agents/spawner/native"
	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

const SpawnAgentName = "spawn-agent"

// Spawner owns launch records and processes. A retry observes the saved launch
// for the same request rather than starting another process with the same ID.
type Spawner interface {
	Spawn(ctx context.Context, requestID, reference string) (native.Result, error)
}

type SpawnAgentInput struct {
	Package string `json:"package" jsonschema:"Exact public GitHub release, for example github.com/defos-ai/commander-example-agent@v0.1.0"`
}

func (input SpawnAgentInput) Validate() error {
	_, err := packages.ParseReference(input.Package)
	return err
}

func NewSpawnAgent(launcher Spawner) (*action.Func[SpawnAgentInput, native.Result], error) {
	if launcher == nil {
		return nil, errors.New("agent launcher is required")
	}
	return action.NewFunc(action.FuncConfig[SpawnAgentInput, native.Result]{
		Action: actionmodel.Action{
			Name:        SpawnAgentName,
			Description: "Launch a versioned agent package and wait for its exact agent ID and type to announce registration. Returns its actions and current readiness; the controller independently persists registration. Launch settings are controlled by the spawner host.",
			// Registered action schemas are raw JSON objects, not byte arrays.
			OutputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"agent_id":{"type":"string"},
					"agent_type":{"type":"string"},
					"package":{"type":"string"},
					"pid":{"type":"integer","minimum":1},
					"actions":{"type":"array","items":{"type":"object"}}
				},
				"required":["agent_id","agent_type","package","pid","actions"],
				"additionalProperties":false
			}`),
		},
		Run: func(ctx context.Context, input SpawnAgentInput) (native.Result, error) {
			requestID, err := spawnRequestID(ctx)
			if err != nil {
				return native.Result{}, err
			}
			return launcher.Spawn(ctx, requestID, input.Package)
		},
	})
}

func spawnRequestID(ctx context.Context) (string, error) {
	execution, ok := action.ExecutionFromContext(ctx)
	if !ok {
		return "", errors.New("spawn-agent requires task or command execution identity")
	}
	if err := execution.Work.Validate(); err != nil {
		return "", fmt.Errorf("spawn-agent execution identity: %w", err)
	}
	if execution.CommandID != "" && !id.Valid(execution.CommandID) {
		return "", errors.New("spawn-agent command ID must be a UUID")
	}
	if execution.Work.TaskID != "" {
		return "task:" + execution.Work.TaskID, nil
	}
	if execution.CommandID != "" {
		return "command:" + execution.CommandID, nil
	}
	return "", errors.New("spawn-agent requires task or command execution identity")
}
