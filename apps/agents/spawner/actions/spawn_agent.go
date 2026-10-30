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
const SpawnAgentDescription = "Launch compatible agent releases; count defaults to 1 and must be between 1 and 100. For count 1, wait for the assigned agent's registration and return its identity, actions and readiness. For larger counts, return the requested batch and propose one spawn-agent task per instance for controller dispatch; review child results for actual launches. Each launch has its own deadline, checkpoints and source approval. Use an exact version when all instances must use the same release. A source-list entry is not required. github.com/defos-ai sources proceed automatically. For other owners each running launch asks an installation question: operator first, model allowed after fallback_at. Yes resumes that execution; No prevents its launch. Answer the saved question rather than assigning replacement work. No approval ID or boolean belongs in input and no owner trust is saved. A GitHub URL, omitted version or @stable selects the promoted Latest release; exact @vMAJOR.MINOR.PATCH is also accepted. Each launch saves its resolved version for retries. Preserve the supplied repository visibility; private packages use the host's GITHUB_TOKEN or managed secrets, never tokens in input. The controller independently persists registration. Launch settings remain controlled by the spawner host. Read the spawner agent's responsibilities for the installation workflow."

// Spawner uses execution checkpoints for action launches. A retry observes the
// saved launch for the same command rather than starting another agent.
type Spawner interface {
	Spawn(ctx context.Context, requestID, reference string) (runtimes.Result, error)
}

type SpawnAgentInput struct {
	Package    string              `json:"package"`
	Visibility packages.Visibility `json:"visibility,omitempty"`
	Count      int                 `json:"count,omitempty"`
}

func (input SpawnAgentInput) Validate() error {
	if input.Count < 1 || input.Count > 100 {
		return errors.New("count must be between 1 and 100")
	}
	if input.Visibility != packages.Public && input.Visibility != packages.Private {
		return errors.New("visibility must be public or private")
	}
	_, err := packages.ParseSelector(input.Package)
	return err
}

func NewSpawnAgent(launcher Spawner) (*action.Func[SpawnAgentInput, map[string]any], error) {
	if launcher == nil {
		return nil, errors.New("agent launcher is required")
	}
	return action.NewFunc(action.FuncConfig[SpawnAgentInput, map[string]any]{
		Action: actionmodel.Action{
			Name:        SpawnAgentName,
			Description: SpawnAgentDescription,
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"package":{"type":"string","description":"GitHub repository URL or github.com/owner/repository[/module/path], optionally followed by @stable or @vMAJOR.MINOR.PATCH; omitted version means stable. Owners outside defos-ai trigger an installation question from the running action."},
					"visibility":{"type":"string","enum":["public","private"],"default":"public","description":"GitHub repository visibility from the listed agent sources or operator; omitted means public. This does not grant access or change Docker image visibility."},
					"count":{"type":"integer","minimum":1,"maximum":100,"default":1,"description":"Number of instances requested. More than one proposes individual spawn-agent tasks for the controller; the limit applies to this request, not the deployment."}
				},
				"required":["package"],
				"additionalProperties":false
			}`),
			// Registered action schemas are raw JSON objects, not byte arrays.
			OutputSchema: json.RawMessage(`{
				"type":"object",
				"oneOf":[{
				"properties":{
					"agent_id":{"type":"string"},
					"agent_type":{"type":"string"},
					"package":{"type":"string"},
					"runtime":{"type":"string","enum":["native","docker","kubernetes"]},
					"runtime_details":{"type":"object"},
					"actions":{"type":"array","items":{"type":"object"}}
				},
				"required":["agent_id","agent_type","package","runtime","runtime_details","actions"],
				"oneOf":[
					{"properties":{"runtime":{"const":"native"},"runtime_details":{
						"properties":{"pid":{"type":"integer","minimum":1}},
						"required":["pid"],"additionalProperties":false
					}}},
					{"properties":{"runtime":{"const":"docker"},"runtime_details":{
						"properties":{"container_id":{"type":"string","minLength":1}},
						"required":["container_id"],"additionalProperties":false
					}}},
					{"properties":{"runtime":{"const":"kubernetes"},"runtime_details":{
						"properties":{
							"namespace":{"type":"string","minLength":1,"maxLength":63,"pattern":"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"},
							"pod_name":{"type":"string","minLength":1,"maxLength":253,"pattern":"^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"},
							"pod_uid":{"type":"string","minLength":1}
						},
						"required":["namespace","pod_name","pod_uid"],"additionalProperties":false
					}}}
				],
				"additionalProperties":false
				},{
					"properties":{
						"package":{"type":"string"},
						"visibility":{"type":"string","enum":["public","private"]},
						"count":{"type":"integer","minimum":2,"maximum":100}
					},
					"required":["package","visibility","count"],
					"additionalProperties":false
				}]
			}`),
		},
		NewInput: func() SpawnAgentInput { return SpawnAgentInput{Visibility: packages.Public, Count: 1} },
		Run: func(ctx context.Context, input SpawnAgentInput) (map[string]any, error) {
			requestID, err := spawnRequestID(ctx)
			if err != nil {
				return nil, err
			}
			if input.Count > 1 {
				return map[string]any{"package": input.Package, "visibility": input.Visibility, "count": input.Count}, nil
			}
			result, err := spawnSingle(ctx, launcher, requestID, input)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"agent_id": result.AgentID, "agent_type": result.AgentType, "package": result.Package,
				"runtime": result.Runtime, "runtime_details": result.RuntimeDetails, "actions": result.Actions,
			}, nil
		},
		NextActions: func(_ context.Context, input SpawnAgentInput, _ map[string]any) ([]action.NextAction, error) {
			if input.Count == 1 {
				return nil, nil
			}
			count := input.Count
			input.Count = 0 // Child requests use the default instead of expanding the batch again.
			data, err := json.Marshal(input)
			if err != nil {
				return nil, err
			}
			proposals := make([]action.NextAction, count)
			for i := range proposals {
				proposals[i] = action.NextAction{ActionName: SpawnAgentName, Input: data,
					Reason: fmt.Sprintf("Launch agent instance %d of %d", i+1, count)}
			}
			return proposals, nil
		},
	})
}

func spawnSingle(ctx context.Context, launcher Spawner, requestID string, input SpawnAgentInput) (runtimes.Result, error) {
	ctx, err := downloads.AuthorizeSource(ctx, input.Package, input.Visibility)
	if err != nil {
		return runtimes.Result{}, err
	}
	var token []byte
	if input.Visibility == packages.Private {
		token = []byte(os.Getenv("GITHUB_TOKEN"))
		if len(token) == 0 {
			err = action.RunWithProgress(ctx, "Private package access", func() error {
				var secretErr error
				token, secretErr = action.RequestSecret(ctx, "GITHUB_TOKEN")
				return secretErr
			})
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
