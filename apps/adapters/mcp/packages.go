package missionsmcp

import (
	"context"
	"errors"
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

type resolveAgentPackageRequest struct {
	Package    string              `json:"package"`
	Visibility packages.Visibility `json:"visibility"`
}

func (r resolveAgentPackageRequest) Validate() error {
	if r.Visibility != packages.Public && r.Visibility != packages.Private {
		return errors.New("visibility must be public or private")
	}
	_, err := packages.ParseSelector(r.Package)
	return err
}

func addPackageTools(server *mcp.Server, config Config) {
	addTool(server, "list_agent_packages", "Discover agent packages from the configured catalog, including name, description, GitHub reference and repository visibility. Use resolve_agent_package to pin a selected reference before installing. Catalog membership is optional and does not verify release availability. Use list_agents for running agents and current readiness.", emptySchema,
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
	addTool(server, "resolve_agent_package", "Resolve a catalog entry's GitHub package reference, or any direct GitHub reference, to an exact version. Pass package and visibility unchanged from the catalog or operator. Public @stable selects the repository's promoted Latest release; private repositories currently require an exact version. Returns package and visibility to save as installation input. Reuse that pin on retries. This only resolves metadata: the spawner checks the release manifest, assets and runtime compatibility during installation. Never pass tokens here.", resolveAgentPackageSchema,
		func(ctx context.Context, request resolveAgentPackageRequest) (resolveAgentPackageRequest, error) {
			ref, err := packages.ParseSelector(request.Package)
			if err != nil {
				return resolveAgentPackageRequest{}, err
			}
			// MCP never acquires the spawner's token or tries private metadata anonymously.
			if request.Visibility == packages.Private && ref.Version == "stable" {
				return resolveAgentPackageRequest{}, errors.New("private @stable resolution requires a pinned package version; supply an exact GitHub release reference and keep visibility private")
			}
			ref, err = config.PackageClient.Resolve(ctx, request.Package)
			if err != nil {
				return resolveAgentPackageRequest{}, err
			}
			return resolveAgentPackageRequest{Package: ref.String(), Visibility: request.Visibility}, nil
		})
}

const resolveAgentPackageSchema = `{
	"type":"object",
	"properties":{
		"package":{"type":"string","description":"GitHub reference from the catalog or operator: github.com/owner/repository[/module/path]@vMAJOR.MINOR.PATCH or @stable; not a catalog alias"},
		"visibility":{"type":"string","enum":["public","private"],"description":"Repository visibility supplied by the catalog or operator; preserve it when installing"}
	},
	"required":["package","visibility"],
	"additionalProperties":false
}`
