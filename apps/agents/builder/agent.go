package builder

import (
	"fmt"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

const AgentType agent.AgentType = "builder-agent"

// Responsibilities returns the default capabilities and usage guidance.
func Responsibilities() []string {
	return []string{
		"Build Commander agents from the example-agent template with Codex, verify them with Go, and return their source as an artifact",
		"Define the requested action contracts before checkout: names, descriptions, input/output object schemas and required secret names, never values. Translate the operator's natural-language request into these contracts, then preserve the mission instructions and actions unchanged when assigning build-agent after checkout-template in the same operation; same-agent placement keeps the workspace on its owning builder. Supply CODEX_API_KEY through Admin or Playground's secret form, never in action inputs. Review the source artifact before publication or installation.",
		"After a successful build and source review, offer publish-agent as an optional continuation in the same build-agent operation. Publish only when the operator requests or agrees to it; skipping publication completes the build with its source artifact. Read publish-agent's schema: preserve explicit public/private visibility or the advertised default; without either, ask Public (recommended) or Private and wait. GITHUB_TOKEN is needed only for publication and uses the managed-secret form. Source publication does not create an installable release.",
		"After publication, release-agent is optional in the same build-agent operation. Obtain the operator's release request and stable version before starting it, using the saved source commit and generated agent_type. Preserve workflow_run_id on automatic status checks. Inspect failed or uncertain runs; never dispatch a replacement blindly. Only status released provides a verified pinned package.",
		"After a verified release, offer to install that agent. Honor an explicit installation choice already supplied; otherwise ask a linked Install this agent? question with Install and Finish options and wait for the operator's answer. Expiry is not approval. Finish the build operation with its source and release links first. If installation is approved, use the existing install-agent mission in a separate operation, passing the released package and visibility unchanged to spawn-agent through the controller. Review its saved result and recheck the installed agent's action readiness before completing installation; registration alone is insufficient. Declining or failing installation preserves the successful build and release. Inspect uncertain launches before retrying.",
	}
}

// NewAgent supplies a fresh action catalog when config.Actions is nil.
// An explicitly empty catalog stays empty and does not open workspace storage.
func NewAgent(config agent.AgentConfig, workspaceDir string) (*agent.Agent, error) {
	config.Type = AgentType
	if config.Responsibilities == nil {
		config.Responsibilities = Responsibilities()
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
	release, err := builderactions.NewReleaseAgent()
	if err != nil {
		return nil, fmt.Errorf("create release-agent: %w", err)
	}
	return []action.Executor{checkout, build, publish, release}, nil
}

// ActionDescriptions describes packaged capabilities without starting discovery or providers.
func ActionDescriptions() []manifest.Action {
	return []manifest.Action{
		{Name: builderactions.CheckoutTemplateName, Description: builderactions.CheckoutTemplateDescription},
		{Name: builderactions.BuildAgentName, Description: builderactions.BuildAgentDescription},
		{Name: builderactions.PublishAgentName, Description: builderactions.PublishAgentDescription},
		{Name: builderactions.ReleaseAgentName, Description: builderactions.ReleaseAgentDescription},
	}
}

// ActionNames lists the catalog without opening workspace storage.
func ActionNames() []string {
	descriptions := ActionDescriptions()
	names := make([]string, len(descriptions))
	for i, description := range descriptions {
		names[i] = description.Name
	}
	return names
}
