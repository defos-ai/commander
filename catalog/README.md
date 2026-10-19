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
Catalog lookup belongs to the Commander client. Installation calls pass a pinned
package reference and visibility to `spawn-agent`; the spawner does not read the
catalog. MCP exposes it through `list_agent_packages`.

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
checkout. MCP defaults to
`https://raw.githubusercontent.com/defos-ai/commander/main/catalog/agents.yaml`,
with `AGENT_CATALOG_URL` as a deployment override. It fetches metadata only when
a tool is called; startup needs no catalog access. The framework has no built-in
source URL.

Loading is limited to 64 KiB and 30 seconds. Source URLs cannot contain credentials,
a query or a fragment; redirects are rejected. The default client sends no
credentials and does not use environment proxies.

`stable` selects the repository's promoted GitHub **Latest** release. Its tag
must be a root stable semantic version such as `v0.2.0`. For builder, that version
maps to `apps/agents/builder/v0.2.0`; for a standalone agent, it maps to `v0.2.0`.
Resolution never searches for another version if the selected release lacks an
agent package. The existing spawner reports missing `agent.yaml` or native assets
when it loads the pinned package.

Use `list_agent_packages` to choose an entry, then pass its `package` and
`visibility` to `resolve_agent_package`. It accepts a public `@stable` reference
or any pinned GitHub reference, including packages outside the catalog. It
returns the pinned package and visibility for installation. Reuse that result
on retries. `spawn-agent` and startup flags still require exact versions; aliases
are names for discovery, not installation inputs. Catalog inclusion does not
prove that the selected release already publishes a runnable package. See the
[package contract](../docs/agent-packages.md#catalog-and-stable-references).

## Private installations

For a pinned private package, pass `"visibility":"private"` to the install
mission and `spawn-agent`. The spawner uses its environment `GITHUB_TOKEN` first,
then requests a managed secret scoped to `spawner-agent` through the existing
Playground/Admin flow. Public or omitted visibility stays token-free.
The metadata client remains anonymous; private `stable` resolution and private
Docker image pulls are not integrated yet.

## Maintenance

Edit the YAML to add or update a discovery entry. Keep aliases stable, descriptions
accurate and package paths explicit. Use [`AGENTS.md`](AGENTS.md) for schema
invariants and validation commands.
