# Agent catalog

[`agents.yaml`](agents.yaml) is Commander's curated discovery catalog. Each alias
has a short capability description and a public GitHub package reference. The
catalog helps identify agents; it is not an enforced allowlist for installation.
Operators remain responsible for trusting package authors.

```yaml
schema_version: 1
agents:
  builder:
    description: Prepares source workspaces by checking out the Commander example agent template.
    package: github.com/defos-ai/commander/apps/agents/builder@stable
```

Descriptions cover implemented capabilities. In particular, the builder currently
checks out a template; build, publication and deployment actions are future work.
The catalog does not contain deployment settings, credentials, action schemas or
live readiness. Agents advertise their actions and requirements when they register.

## Package references

References use
`github.com/<owner>/<repository>[/<module-path>]@<version>`, where `<version>` is
an explicit `vMAJOR.MINOR.PATCH` or `stable`. The shared
[`framework/agent/packages`](../framework/agent/packages/) package validates the
catalog and resolves `stable` to a pinned reference. Its `Client.LoadCatalog`
accepts a configured public HTTPS source URL without requiring a Commander source
checkout. The official source URL for application integration is
`https://raw.githubusercontent.com/defos-ai/commander/main/catalog/agents.yaml`,
available once this catalog is published. The framework has no built-in source URL.

Loading is limited to 64 KiB and 30 seconds. Source URLs cannot contain credentials,
a query or a fragment; redirects are rejected. The default client sends no
credentials and does not use environment proxies.

`stable` selects the repository's promoted GitHub **Latest** release. Its tag
must be a root stable semantic version such as `v0.2.0`. For builder, that version
maps to `apps/agents/builder/v0.2.0`; for a standalone agent, it maps to `v0.2.0`.
Resolution never searches for another version if the selected release lacks an
agent package. The existing spawner reports missing `agent.yaml` or native assets
when it loads the pinned package.

The catalog and shared metadata APIs are the first integration step. MCP, CLI and
`spawn-agent` do not yet accept catalog aliases or `@stable`; continue supplying
explicit pinned package references there. Catalog inclusion does not prove that
the selected release already publishes a runnable package. See the
[package contract](../docs/agent-packages.md#catalog-and-stable-references).

## Maintenance

Edit the YAML to add or update a discovery entry. Keep aliases stable, descriptions
accurate and package paths explicit. Use [`AGENTS.md`](AGENTS.md) for schema
invariants and validation commands.
