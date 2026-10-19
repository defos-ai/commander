# Agent catalog

[`agents.yaml`](agents.yaml) is Commander's curated discovery catalog. Each alias
has a short capability description, a GitHub package reference and a required
`visibility` value of `public` or `private`. The catalog helps identify agents;
it is not an enforced allowlist for installation.
Operators remain responsible for trusting package authors.

```yaml
schema_version: 1
agents:
  builder:
    description: Prepares source workspaces by checking out the Commander example agent template.
    package: github.com/defos-ai/commander/apps/agents/builder@stable
    visibility: public
```

Visibility describes the GitHub repository. Package trust remains the operator's
decision, and Docker image visibility is separate. All official entries are public.
Private entries pass schema validation, but the current metadata client and
spawner make anonymous requests; authenticated private installation is not yet
implemented.

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

## Planned private installations

The next implementation step will resolve private `stable` references and
download private packages on the spawner. It will use `GITHUB_TOKEN` from the
spawner's environment first, then a managed secret scoped to `spawner-agent`.
A missing token will use the existing Admin secret exchange. Public installations
will remain token-free.

## Maintenance

Edit the YAML to add or update a discovery entry. Keep aliases stable, descriptions
accurate and package paths explicit. Use [`AGENTS.md`](AGENTS.md) for schema
invariants and validation commands.
