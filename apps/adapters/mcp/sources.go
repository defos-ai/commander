package missionsmcp

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const DefaultAgentSourcesURL = "https://raw.githubusercontent.com/defos-ai/commander/main/sources/agents.yaml"

type agentSource struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Source      string              `json:"source"`
	Visibility  packages.Visibility `json:"visibility"`
}

type agentSources struct {
	Sources []agentSource `json:"sources"`
}

func addSourceTools(server *mcp.Server, config Config) {
	addTool(server, "get_agent_sources", "Discover agents from Commander's public source list, including name, description, GitHub source reference and repository visibility. Pass source as the package input and preserve visibility in the installation workflow described by the relevant agent's responsibilities. The spawner resolves the release during installation. Direct GitHub URLs and references also work without source-list membership. Discovery does not verify release availability or grant permissions. Use list_agents for running agents and current readiness.", emptySchema,
		func(ctx context.Context, _ struct{}) (agentSources, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			sources, err := config.SourceClient.LoadSources(ctx, DefaultAgentSourcesURL)
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
			entries := make([]agentSource, 0, len(sources.Agents))
			for name, entry := range sources.Agents {
				entries = append(entries, agentSource{Name: name, Description: entry.Description, Source: entry.Source, Visibility: entry.Visibility})
			}
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
