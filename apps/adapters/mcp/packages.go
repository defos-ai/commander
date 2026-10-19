package missionsmcp

import (
	"context"
	"sort"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const DefaultAgentCatalogURL = "https://raw.githubusercontent.com/defos-ai/commander/main/catalog/agents.yaml"

type agentPackageSummary struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Package     string              `json:"package"`
	Visibility  packages.Visibility `json:"visibility"`
}

type agentPackageList struct {
	Packages []agentPackageSummary `json:"packages"`
}

func addPackageTools(server *mcp.Server, config Config) {
	addTool(server, "list_agent_packages", "Discover agent packages from the configured catalog, including name, description, GitHub reference and repository visibility. Pass package and visibility unchanged to the installation workflow described by the relevant agent's responsibilities. The spawner resolves the release during installation. Direct GitHub URLs and references also work without catalog membership. Discovery does not verify release availability. Use list_agents for running agents and current readiness.", emptySchema,
		func(ctx context.Context, _ struct{}) (agentPackageList, error) {
			catalog, err := config.PackageClient.LoadCatalog(ctx, config.CatalogURL)
			if err != nil {
				return agentPackageList{}, err
			}
			summaries := make([]agentPackageSummary, 0, len(catalog.Agents))
			for name, entry := range catalog.Agents {
				summaries = append(summaries, agentPackageSummary{
					Name: name, Description: entry.Description, Package: entry.Package, Visibility: entry.Visibility,
				})
			}
			sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
			return agentPackageList{Packages: summaries}, nil
		})
}
