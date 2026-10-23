# Agent catalog

Find agents in [`agents.yaml`](agents.yaml), or ask Playground:

> Install the web agent.

For MCP clients, use `get_agent_catalog` and pass the selected package and
visibility to the [installation workflow](../apps/agents/spawner/README.md#during-a-mission).
You can also install any compatible GitHub package directly.

## Package references

- `https://github.com/owner/repository` selects its promoted **Latest** release.
- `github.com/owner/repository@stable` does the same.
- `github.com/owner/repository@v1.2.3` selects that exact version.
- Include a module path for agents in a monorepo, such as
  `github.com/defos-ai/commander/apps/agents/builder@stable`.

Catalog aliases help discovery; installation uses the package reference.
Only install packages from authors you trust. Check live action readiness in
Admin after installation.

## Private installations

Pass `"visibility":"private"`. Supply `GITHUB_TOKEN` on the spawner or through
its Playground/Admin secret form. Private GHCR images also require package access.
See [private package setup](../apps/agents/spawner/README.md#private-github-packages).

## Maintenance

To add an agent, add an entry with its capabilities, package and repository visibility:

```yaml
schema_version: 1
agents:
  example:
    description: Demonstrates custom Commander actions.
    package: github.com/defos-ai/commander-example-agent@stable
    visibility: public
```

Set `AGENT_CATALOG_URL` on MCP to use another public HTTPS catalog.
See [package publishing](../docs/agent-packages.md) and [AGENTS.md](AGENTS.md)
for validation and catalog rules.
