package spawner

import (
	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	agents "github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/manifest"
)

const AgentType agents.AgentType = "spawner-agent"

func Responsibilities() []string {
	return []string{
		"Install compatible agents from GitHub releases, including repositories outside the source list. defos-ai sources proceed automatically; for other owners spawn-agent asks an installation question before token access or launch. The operator answers first, and the model may answer after fallback_at. Answer the saved question: Yes resumes installation and No prevents it. No saved owner trust is created. The release must publish agent.yaml describing a released native executable or digest-pinned Docker image for the configured runtime.",
		"Check existing agents and action readiness before installing anything. Use get_agent_sources for discovery and pass its selected source reference as the package input, preserving visibility, or use a GitHub URL supplied by the operator. Accept https://github.com/owner/repository or github.com/owner/repository[/module/path], optionally with @stable or @vMAJOR.MINOR.PATCH. Omitted versions mean stable. The spawner resolves the promoted Latest release and saves the exact version for this request. Never invent a package or version; source aliases must be looked up before dispatch.",
		"Use inspect-agent before launch when native dependencies or platform support need preparation. It reads release metadata without launching, downloading executables, pulling images or granting installation approval. Private inspection uses the normal GitHub credential flow. Review the declared dependencies, native platforms and optional Docker image, then pass the inspection's exact package reference to spawn-agent. The reported native_platform applies only to this spawner's native execution environment, including inside all-in-one containers; Docker and Kubernetes target platforms are not inferred. Metadata does not verify installed software, image access or action readiness. Prepare the actual execution environment only with its operator's authorization and access.",
		`Read describe_mission for install-agent and describe_action for the required package actions. Start a separate install-agent operation with {"package":"<package reference or GitHub URL>","visibility":"public"}. Assign spawn-agent using the inspected exact package when available, preserving visibility and the requested count. Count defaults to 1 and accepts up to 100. For larger counts the controller dispatches proposed single-instance tasks; the batch result alone does not mean agents were installed. Omitted visibility means public. Use an exact version when all instances must run the same release. Do not provision inside a mission blocked on the missing actions.`,
		"Private packages use the spawner's GITHUB_TOKEN or the existing managed-secret form. Never include tokens in chat, operation inputs or action inputs. Supply action secrets through the managed-secret form when requested. Installation does not install host dependencies or grant arbitrary deployment privileges to the new agent.",
		"While spawn-agent is running, answer any saved execution question rather than asking another question, resolving its continuation or dispatching replacement work. Its task deadline continues while waiting, and it resumes automatically after answer delivery. Review every saved launch result; install-agent aggregates a batch's direct children into one model review. Resolve the model continuation with no follow-up actions and complete installation using agent_id for one launch or agent_ids for a batch. Recheck every registered agent's action readiness and the requested mission's preflight before running dependent work; registration alone does not mean every action is ready.",
		"Inspect uncertain launches before retrying. Retrying the same task observes its saved launch; assigning a new task may create another agent instance.",
	}
}

func NewAgent(config agents.AgentConfig, launcher spawneractions.Spawner, resolver *packages.Resolver, runtimeKind runtimes.RuntimeKind) (*agents.Agent, error) {
	config.Type = AgentType
	if config.Responsibilities == nil {
		config.Responsibilities = Responsibilities()
	}
	if config.Actions == nil {
		var err error
		config.Actions, err = NewActions(launcher, resolver, runtimeKind)
		if err != nil {
			return nil, err
		}
	}
	return agents.NewAgent(config)
}

func NewActions(launcher spawneractions.Spawner, resolver *packages.Resolver, runtimeKind runtimes.RuntimeKind) ([]action.Executor, error) {
	spawn, err := spawneractions.NewSpawnAgent(launcher)
	if err != nil {
		return nil, err
	}
	inspect, err := spawneractions.NewInspectAgent(resolver, runtimeKind)
	if err != nil {
		return nil, err
	}
	return []action.Executor{spawn, inspect}, nil
}

func ActionDescriptions() []manifest.Action {
	return []manifest.Action{
		{Name: spawneractions.SpawnAgentName, Description: spawneractions.SpawnAgentDescription},
		{Name: spawneractions.InspectAgentName, Description: spawneractions.InspectAgentDescription},
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
