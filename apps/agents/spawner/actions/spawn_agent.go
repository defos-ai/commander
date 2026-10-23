package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	downloads "github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/packages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

const SpawnAgentName = "spawn-agent"
const SpawnAgentDescription = "Launch any compatible GitHub agent package; catalog inclusion is not required. A repository URL or omitted version selects stable; @stable and exact @vMAJOR.MINOR.PATCH references are also accepted. The spawner saves the resolved version for retries. Pass the package reference and repository visibility from the operator or catalog. Private packages use the host's GITHUB_TOKEN or request it through managed secrets; never put tokens in input. Waits for the assigned agent ID and type to announce registration and returns actions and current readiness; the controller independently persists registration. Launch settings are controlled by the spawner host. Read the spawner agent's responsibilities for the installation workflow."

// Spawner uses execution checkpoints for action launches. A retry observes the
// saved launch for the same command rather than starting another agent.
type Spawner interface {
	Spawn(ctx context.Context, requestID, reference string) (runtimes.Result, error)
}

type SpawnAgentInput struct {
	Package    string              `json:"package"`
	Visibility packages.Visibility `json:"visibility,omitempty"`
}

func (input SpawnAgentInput) Validate() error {
	if input.Visibility != packages.Public && input.Visibility != packages.Private {
		return errors.New("visibility must be public or private")
	}
	_, err := packages.ParseSelector(input.Package)
	return err
}

func NewSpawnAgent(launcher Spawner) (*action.Func[SpawnAgentInput, runtimes.Result], error) {
	if launcher == nil {
		return nil, errors.New("agent launcher is required")
	}
	return action.NewFunc(action.FuncConfig[SpawnAgentInput, runtimes.Result]{
		Action: actionmodel.Action{
			Name:        SpawnAgentName,
			Description: SpawnAgentDescription,
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"package":{"type":"string","description":"GitHub repository URL or github.com/owner/repository[/module/path], optionally followed by @stable or @vMAJOR.MINOR.PATCH; omitted version means stable"},
					"visibility":{"type":"string","enum":["public","private"],"default":"public","description":"GitHub repository visibility from the catalog or operator; omitted means public. This does not grant access or change Docker image visibility."}
				},
				"required":["package"],
				"additionalProperties":false
			}`),
			// Registered action schemas are raw JSON objects, not byte arrays.
			OutputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"agent_id":{"type":"string"},
					"agent_type":{"type":"string"},
					"package":{"type":"string"},
					"runtime":{"type":"string","enum":["native","docker"]},
					"pid":{"type":"integer","minimum":1},
					"container_id":{"type":"string","minLength":1},
					"actions":{"type":"array","items":{"type":"object"}}
				},
				"required":["agent_id","agent_type","package","runtime","actions"],
				"oneOf":[
					{"properties":{"runtime":{"const":"native"}},"required":["pid"],"not":{"required":["container_id"]}},
					{"properties":{"runtime":{"const":"docker"}},"required":["container_id"],"not":{"required":["pid"]}}
				],
				"additionalProperties":false
			}`),
		},
		NewInput: func() SpawnAgentInput { return SpawnAgentInput{Visibility: packages.Public} },
		Run: func(ctx context.Context, input SpawnAgentInput) (runtimes.Result, error) {
			requestID, err := spawnRequestID(ctx)
			if err != nil {
				return runtimes.Result{}, err
			}
			var token []byte
			if input.Visibility == packages.Private {
				token = []byte(os.Getenv("GITHUB_TOKEN"))
				if len(token) == 0 {
					token, err = action.RequestSecret(ctx, "GITHUB_TOKEN")
					if err != nil {
						return runtimes.Result{}, fmt.Errorf("private package requires GITHUB_TOKEN: %w", err)
					}
				}
				defer clear(token)
				if len(token) == 0 {
					return runtimes.Result{}, errors.New("GITHUB_TOKEN must not be empty")
				}
			}
			if err := ctx.Err(); err != nil {
				return runtimes.Result{}, err
			}
			// Public requests explicitly clear any inherited download token.
			return launcher.Spawn(downloads.WithGitHubToken(ctx, token), requestID, input.Package)
		},
	})
}

func spawnRequestID(ctx context.Context) (string, error) {
	execution, ok := action.ExecutionFromContext(ctx)
	if !ok || execution.CommandID == "" {
		return "", errors.New("spawn-agent requires a controller-dispatched execution")
	}
	if err := execution.Work.Validate(); err != nil {
		return "", fmt.Errorf("spawn-agent execution identity: %w", err)
	}
	if !id.Valid(execution.CommandID) {
		return "", errors.New("spawn-agent command ID must be a UUID")
	}
	return "command:" + execution.CommandID, nil
}
