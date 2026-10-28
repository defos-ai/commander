package spawner

import (
	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	agents "github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/manifest"
)

const AgentType agents.AgentType = "spawner-agent"

func Responsibilities() []string {
	return []string{
		"Install any compatible agent package from a GitHub release, including repositories outside the source list. The release must publish agent.yaml describing a released native executable or digest-pinned Docker image for the configured runtime. A source-list entry is not required.",
		"Check existing agents and action readiness before installing anything. Use get_agent_sources for discovery and pass its selected source reference as the package input, preserving visibility, or use a GitHub URL supplied by the operator. Accept https://github.com/owner/repository or github.com/owner/repository[/module/path], optionally with @stable or @vMAJOR.MINOR.PATCH. Omitted versions mean stable. The spawner resolves the promoted Latest release and saves the exact version for this request. Never invent a package or version; source aliases must be looked up before dispatch.",
		`Read describe_mission for install-agent and describe_action for spawn-agent. Start a separate install-agent operation with {"package":"<package reference or GitHub URL>","visibility":"public"}, then use assign_task for spawn-agent with the same input. Preserve the supplied public/private repository visibility; omitted means public. The controller dispatches the task. Do not provision inside a mission blocked on the missing actions.`,
		"Private packages use the spawner's GITHUB_TOKEN or the existing managed-secret form. Never include tokens in chat, operation inputs or action inputs. Supply action secrets through the managed-secret form when requested. Installation does not install host dependencies or grant arbitrary deployment privileges to the new agent.",
		"Review the saved spawn result, resolve its model continuation with no follow-up actions, and complete installation using the mission's output schema. Recheck the registered agent's action readiness and the requested mission's preflight before running dependent work; registration alone does not mean every action is ready.",
		"Inspect uncertain launches before retrying. Retrying the same task observes its saved launch; assigning a new task may create another agent instance.",
	}
}

func NewAgent(config agents.AgentConfig, launcher spawneractions.Spawner) (*agents.Agent, error) {
	config.Type = AgentType
	if config.Responsibilities == nil {
		config.Responsibilities = Responsibilities()
	}
	if config.Actions == nil {
		var err error
		config.Actions, err = NewActions(launcher)
		if err != nil {
			return nil, err
		}
	}
	return agents.NewAgent(config)
}

func NewActions(launcher spawneractions.Spawner) ([]action.Executor, error) {
	spawn, err := spawneractions.NewSpawnAgent(launcher)
	if err != nil {
		return nil, err
	}
	return []action.Executor{spawn}, nil
}

func ActionDescriptions() []manifest.Action {
	return []manifest.Action{
		{Name: spawneractions.SpawnAgentName, Description: spawneractions.SpawnAgentDescription},
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
