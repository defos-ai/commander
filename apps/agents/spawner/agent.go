package spawner

import (
	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	agents "github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
)

const AgentType agents.AgentType = "spawner-agent"

// NewAgent constructs a fresh spawn catalog without launching processes.
// An explicitly empty action catalog remains empty and does not require a launcher.
func NewAgent(config agents.AgentConfig, launcher spawneractions.Spawner) (*agents.Agent, error) {
	config.Type = AgentType
	if config.Responsibilities == nil {
		config.Responsibilities = []string{
			"Install any compatible agent package from a GitHub release, including repositories outside the discovery catalog. The release must publish agent.yaml describing a released native executable or digest-pinned Docker image for the configured runtime. Catalog inclusion is not required.",
			"Check existing agents and action readiness before installing anything. Use get_agent_catalog for discovery and pass the selected package and visibility, or use a GitHub URL supplied by the operator. Accept https://github.com/owner/repository or github.com/owner/repository[/module/path], optionally with @stable or @vMAJOR.MINOR.PATCH. Omitted versions mean stable. The spawner resolves the promoted Latest release and saves the exact version for this request. Never invent a package or version; catalog aliases must be looked up before dispatch.",
			`Read describe_mission for install-agent and describe_action for spawn-agent. Start a separate install-agent operation with {"package":"<package reference or GitHub URL>","visibility":"public"}, then use assign_task for spawn-agent with the same input. Preserve the supplied public/private repository visibility; omitted means public. The controller dispatches the task. Do not provision inside a mission blocked on the missing actions.`,
			"Private packages use the spawner's GITHUB_TOKEN or the existing managed-secret form. Never include tokens in chat, operation inputs or action inputs. Supply action secrets through the managed-secret form when requested. Installation does not install host dependencies or grant arbitrary deployment privileges to the new agent.",
			"Review the saved spawn result, resolve its model continuation with no follow-up actions, and complete installation using the mission's output schema. Recheck the registered agent's action readiness and the requested mission's preflight before running dependent work; registration alone does not mean every action is ready.",
			"Inspect uncertain launches before retrying. Retrying the same task observes its saved launch; assigning a new task may create another agent instance.",
		}
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

// NewActions creates executors with discovery state owned by this agent.
func NewActions(launcher spawneractions.Spawner) ([]action.Executor, error) {
	spawn, err := spawneractions.NewSpawnAgent(launcher)
	if err != nil {
		return nil, err
	}
	return []action.Executor{spawn}, nil
}

// ActionNames lists the catalog without creating providers or starting agents.
func ActionNames() []string {
	return []string{spawneractions.SpawnAgentName}
}
