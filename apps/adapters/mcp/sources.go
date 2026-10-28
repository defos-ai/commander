package missionsmcp

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentSources struct {
	Sources []agents.AgentSource `json:"sources"`
}

func addSourceTools(server *mcp.Server, config Config) {
	addTool(server, "get_agent_sources", "Discover saved agent sources, including name, description, GitHub source reference, repository visibility, trust, version and timestamps. Trust is saved metadata, not proof of current readiness or release availability. Pass source as the package input and preserve visibility in the installation workflow described by the relevant agent's responsibilities. The spawner resolves the release during installation. Direct GitHub URLs and references also work without a saved source. Use list_agents for running agents and current readiness.", emptySchema,
		func(ctx context.Context, _ struct{}) (agentSources, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			sources, err := config.AgentSourceStore.List(ctx)
			if err != nil {
				switch {
				case errors.Is(err, context.DeadlineExceeded):
					return agentSources{}, errors.New("agent sources read timed out")
				case errors.Is(err, context.Canceled):
					return agentSources{}, errors.New("agent sources read canceled")
				default:
					return agentSources{}, errors.New("agent sources read failed")
				}
			}
			entries := append(make([]agents.AgentSource, 0, len(sources)), sources...)
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].Visibility != entries[j].Visibility {
					return entries[i].Visibility == packages.Public
				}
				if entries[i].Name != entries[j].Name {
					return entries[i].Name < entries[j].Name
				}
				return entries[i].Source < entries[j].Source
			})
			return agentSources{Sources: entries}, nil
		})
}
