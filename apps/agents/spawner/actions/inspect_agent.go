package actions

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"slices"

	"github.com/defos-ai/commander/apps/agents/spawner/internal/packageaccess"
	downloads "github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/packages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

const InspectAgentName = "inspect-agent"
const InspectAgentDescription = "Inspect a released agent package before launching it. Return its exact package/version, declared dependencies, native platforms and optional Docker image, plus this spawner's configured runtime. native_platform identifies the execution environment only in native mode, including the AIO container when applicable; Docker/Kubernetes target platforms are not inferred. Declared software, versions and images are installation guidance, not readiness evidence. Use the returned exact package with spawn-agent after preparing the execution environment. Private repositories use GITHUB_TOKEN through the existing managed-secret flow. Inspection launches no software and grants no installation approval; spawn-agent independently applies its source policy."

type InspectAgentInput struct {
	Package    string              `json:"package"`
	Visibility packages.Visibility `json:"visibility,omitempty"`
}

func (input InspectAgentInput) Validate() error {
	if input.Visibility != packages.Public && input.Visibility != packages.Private {
		return errors.New("visibility must be public or private")
	}
	_, err := packages.ParseSelector(input.Package)
	return err
}

type InspectAgentOutput struct {
	Package         string               `json:"package"`
	AgentType       string               `json:"agent_type"`
	Version         string               `json:"version"`
	Runtime         runtimes.RuntimeKind `json:"runtime"`
	NativePlatform  string               `json:"native_platform,omitempty"`
	NativePlatforms []string             `json:"native_platforms"`
	DockerImage     string               `json:"docker_image,omitempty"`
	Dependencies    []agents.Dependency  `json:"dependencies,omitempty"`
}

func NewInspectAgent(resolver *downloads.Resolver, kind runtimes.RuntimeKind) (*action.Func[InspectAgentInput, InspectAgentOutput], error) {
	if resolver == nil {
		return nil, errors.New("package resolver is required")
	}
	switch kind {
	case runtimes.RuntimeNative, runtimes.RuntimeDocker, runtimes.RuntimeKubernetes:
	default:
		return nil, errors.New("inspection runtime must be native, docker or kubernetes")
	}
	return action.NewFunc(action.FuncConfig[InspectAgentInput, InspectAgentOutput]{
		Action: actionmodel.Action{
			Name: InspectAgentName, Description: InspectAgentDescription,
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"package":{"type":"string","description":"GitHub repository URL or github.com/owner/repository[/module/path], optionally followed by @stable or @vMAJOR.MINOR.PATCH; omitted version means stable."},
					"visibility":{"type":"string","enum":["public","private"],"default":"public","description":"Repository visibility supplied by the source list or operator. Private reads use managed credentials; never include tokens in input."}
				},
				"required":["package"],"additionalProperties":false
			}`),
		},
		NewInput: func() InspectAgentInput { return InspectAgentInput{Visibility: packages.Public} },
		Run: func(ctx context.Context, input InspectAgentInput) (InspectAgentOutput, error) {
			ctx, token, err := packageaccess.Acquire(ctx, input.Visibility)
			if err != nil {
				return InspectAgentOutput{}, err
			}
			defer clear(token)
			var pkg downloads.Package
			err = action.RunWithProgress(ctx, "Package inspection", func() error {
				ref, err := resolver.Pin(ctx, input.Package)
				if err != nil {
					return err
				}
				pkg, err = resolver.Resolve(ctx, ref.String())
				return err
			})
			if err != nil {
				return InspectAgentOutput{}, err
			}
			output := InspectAgentOutput{
				Package: pkg.Reference.String(), AgentType: pkg.Manifest.Type, Version: pkg.Manifest.Version, Runtime: kind,
				NativePlatforms: make([]string, 0, len(pkg.Manifest.Native)), Dependencies: slices.Clone(pkg.Manifest.Dependencies),
			}
			for platform := range pkg.Manifest.Native {
				output.NativePlatforms = append(output.NativePlatforms, platform)
			}
			slices.Sort(output.NativePlatforms)
			if kind == runtimes.RuntimeNative {
				output.NativePlatform = runtime.GOOS + "/" + runtime.GOARCH
			}
			if pkg.Manifest.Docker != nil {
				output.DockerImage = pkg.Manifest.Docker.Image
			}
			return output, nil
		},
	})
}
