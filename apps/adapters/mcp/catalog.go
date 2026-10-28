package missionsmcp

import (
	"context"
	"sort"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const DefaultAgentCatalogURL = "https://raw.githubusercontent.com/defos-ai/commander/main/catalog/agents.yaml"

type agentCatalogEntry struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Package     string              `json:"package"`
	Visibility  packages.Visibility `json:"visibility"`
}

type agentCatalog struct {
	Agents []agentCatalogEntry `json:"agents"`
}

func addCatalogTools(server *mcp.Server, config Config) {
	addTool(server, "get_agent_catalog", "Discover agents available to install from the configured catalog, including name, description, GitHub reference and repository visibility. Pass package and visibility unchanged to the installation workflow described by the relevant agent's responsibilities. The spawner resolves the release during installation. Direct GitHub URLs and references also work without catalog membership. Discovery does not verify release availability. Use list_agents for running agents and current readiness.", emptySchema,
		func(ctx context.Context, _ struct{}) (agentCatalog, error) {
			catalog, err := config.CatalogClient.LoadCatalog(ctx, config.CatalogURL)
			if err != nil {
				return agentCatalog{}, err
			}
			entries := make([]agentCatalogEntry, 0, len(catalog.Agents))
			for name, entry := range catalog.Agents {
				entries = append(entries, agentCatalogEntry{
					Name: name, Description: entry.Description, Package: entry.Package, Visibility: entry.Visibility,
				})
			}
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].Visibility != entries[j].Visibility {
					return entries[i].Visibility == packages.Public
				}
				return entries[i].Name < entries[j].Name
			})
			return agentCatalog{Agents: entries}, nil
		})
}
