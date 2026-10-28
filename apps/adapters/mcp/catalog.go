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

type agentCatalog struct {
	Agents []agents.CatalogEntry `json:"agents"`
}

func addCatalogTools(server *mcp.Server, config Config) {
	addTool(server, "get_agent_catalog", "Discover agents in the saved deployment catalog, including name, description, GitHub package reference, repository visibility, trust, version and timestamps. Trust is saved metadata, not proof of current readiness or release availability. Pass package and visibility unchanged to the installation workflow described by the relevant agent's responsibilities. The spawner resolves the release during installation. Direct GitHub URLs and references also work without catalog membership. Use list_agents for running agents and current readiness.", emptySchema,
		func(ctx context.Context, _ struct{}) (agentCatalog, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			catalog, err := config.CatalogStore.List(ctx)
			if err != nil {
				switch {
				case errors.Is(err, context.DeadlineExceeded):
					return agentCatalog{}, errors.New("agent catalog read timed out")
				case errors.Is(err, context.Canceled):
					return agentCatalog{}, errors.New("agent catalog read canceled")
				default:
					return agentCatalog{}, errors.New("agent catalog read failed")
				}
			}
			entries := append(make([]agents.CatalogEntry, 0, len(catalog)), catalog...)
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].Visibility != entries[j].Visibility {
					return entries[i].Visibility == packages.Public
				}
				if entries[i].Name != entries[j].Name {
					return entries[i].Name < entries[j].Name
				}
				return entries[i].Package < entries[j].Package
			})
			return agentCatalog{Agents: entries}, nil
		})
}
