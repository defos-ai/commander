package builder

import (
	"fmt"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

const AgentType agent.AgentType = "builder-agent"

// NewAgent supplies a fresh action catalog when config.Actions is nil.
// An explicitly empty catalog stays empty and does not open workspace storage.
func NewAgent(config agent.AgentConfig, workspaceDir string) (*agent.Agent, error) {
	config.Type = AgentType
	if config.Responsibilities == nil {
		config.Responsibilities = []string{
			"Build Commander agents from the example-agent template with Codex, verify them with Go, and return their source as an artifact",
			"Use checkout-template followed by build-agent in one build-agent operation; same-agent placement keeps the workspace on its owning builder. Supply CODEX_API_KEY through Admin or Playground's secret form, never in action inputs. Review the source artifact before publication or installation.",
			"Publish a reviewed source artifact through a separate publish-agent mission. Read its schema: use explicit public/private visibility or the advertised default. Without either, ask Public (recommended) or Private and wait for the operator's answer. Supply managed GITHUB_TOKEN through the secret form. Source publication does not create an installable release.",
		}
	}
	if config.Actions == nil {
		var err error
		config.Actions, err = NewActions(workspaceDir, config.ArtifactFileStore, "")
		if err != nil {
			return nil, err
		}
	}
	return agent.NewAgent(config)
}

// NewActions creates fresh executors so agents never share discovery state.
func NewActions(workspaceDir string, files artifacts.ArtifactFileStore, defaultVisibility string) ([]action.Executor, error) {
	checkout, err := builderactions.NewCheckoutTemplate(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("create checkout-template: %w", err)
	}
	build, err := builderactions.NewBuildAgent(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("create build-agent: %w", err)
	}
	publish, err := builderactions.NewPublishAgent(files, defaultVisibility)
	if err != nil {
		return nil, fmt.Errorf("create publish-agent: %w", err)
	}
	return []action.Executor{checkout, build, publish}, nil
}

// ActionNames lists the catalog without opening workspace storage.
func ActionNames() []string {
	return []string{builderactions.CheckoutTemplateName, builderactions.BuildAgentName, builderactions.PublishAgentName}
}
