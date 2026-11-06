package builder

import (
	"fmt"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/internal/workspaces"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

const AgentType agent.AgentType = "builder-agent"

func Responsibilities() []string {
	return []string{
		"Build Commander agents from the example-agent template with Codex, verify them with Go, and return their source as an artifact",
		"Define the requested action contracts before checkout: names, descriptions, input/output object schemas and required secret names, never values. Translate the operator's natural-language request into these contracts, assign checkout-template with {}, then preserve the mission instructions and actions unchanged when assigning build-agent through the checkout's continuation. The controller retains the workspace session and routes work to its owning builder; session and workspace IDs are output metadata, not action inputs. Supply OPENAI_API_KEY through Admin or Playground's secret form, never in action inputs. Review the source artifact before publication or installation.",
		"After a successful build and source review, offer publish-agent as an optional continuation in the same build-agent operation. Publish only when the operator requests or agrees to it; skipping publication completes the build with its source artifact. Read publish-agent's schema: preserve explicit public/private visibility or the advertised default; without either, ask Public (recommended) or Private and wait. GITHUB_TOKEN is needed only for publication and uses the managed-secret form. Source publication does not create an installable release.",
		"After publication, release-agent is optional in the same build-agent operation. Obtain the operator's release request and stable version before starting it, using the saved source commit and generated agent_type. Preserve workflow_run_id on automatic status checks. Inspect failed or uncertain runs; never dispatch a replacement blindly. Only status released provides a verified pinned package.",
		"After a verified release, offer to install that agent. Honor an explicit installation choice already supplied; otherwise ask a linked Install this agent? question with Install and Finish options and wait for the operator's answer. Expiry is not approval. Finish the build operation with its source and release links first. If installation is approved, use the existing install-agent mission in a separate operation, passing the released package and visibility unchanged to spawn-agent through the controller. Review its saved result and recheck the installed agent's action readiness before completing installation; registration alone is insufficient. Declining or failing installation preserves the successful build and release. Inspect uncertain launches before retrying.",
	}
}

func NewAgent(config agent.AgentConfig, workspaceDir string) (*agent.Agent, error) {
	config.Type = AgentType
	if config.Responsibilities == nil {
		config.Responsibilities = Responsibilities()
	}
	if config.Actions == nil {
		if config.Sessions != nil {
			return nil, fmt.Errorf("custom workspace sessions require matching builder actions")
		}
		var err error
		config.Actions, config.Sessions, err = NewActions(workspaceDir, config.ArtifactFileStore, "")
		if err != nil {
			return nil, err
		}
	}
	for _, executor := range config.Actions {
		if executor != nil && executor.Action().SessionMode != "" && config.Sessions == nil {
			return nil, fmt.Errorf("session actions require their matching workspace provider")
		}
	}
	return agent.NewAgent(config)
}

func NewActions(workspaceDir string, files artifacts.ArtifactFileStore, defaultVisibility string) ([]action.Executor, sessions.Provider, error) {
	provider, err := workspaces.New(workspaceDir)
	if err != nil {
		return nil, nil, err
	}
	checkout, err := builderactions.NewCheckoutTemplate(provider)
	if err != nil {
		return nil, nil, fmt.Errorf("create checkout-template: %w", err)
	}
	build, err := builderactions.NewBuildAgent(provider)
	if err != nil {
		return nil, nil, fmt.Errorf("create build-agent: %w", err)
	}
	publish, err := builderactions.NewPublishAgent(files, defaultVisibility)
	if err != nil {
		return nil, nil, fmt.Errorf("create publish-agent: %w", err)
	}
	release, err := builderactions.NewReleaseAgent()
	if err != nil {
		return nil, nil, fmt.Errorf("create release-agent: %w", err)
	}
	return []action.Executor{checkout, build, publish, release}, provider, nil
}

func ActionDescriptions() []manifest.Action {
	return []manifest.Action{
		{Name: builderactions.CheckoutTemplateName, Description: builderactions.CheckoutTemplateDescription},
		{Name: builderactions.BuildAgentName, Description: builderactions.BuildAgentDescription},
		{Name: builderactions.PublishAgentName, Description: builderactions.PublishAgentDescription},
		{Name: builderactions.ReleaseAgentName, Description: builderactions.ReleaseAgentDescription},
	}
}

func ActionNames() []string {
	descriptions := ActionDescriptions()
	names := make([]string, len(descriptions))
	for i, description := range descriptions {
		names[i] = description.Name
	}
	return names
}
