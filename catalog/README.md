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
    description: Builds Commander agents with Codex, validates and exports source, and publishes reviewed source artifacts to GitHub.
    package: github.com/defos-ai/commander/apps/agents/builder@stable
    visibility: public
```

Visibility describes the GitHub repository. Package trust remains the operator's
decision, and Docker image visibility is separate. All official entries are public.
Catalog lookup belongs to the Commander client. Installation calls pass the
package reference and visibility to `spawn-agent`; the spawner does not read the
catalog. MCP's `get_agent_catalog` returns its entries in an `agents` list.

Descriptions cover implemented capabilities. The builder checks out a template,
generates and validates source, and exports an artifact. A separate `publish-agent`
mission publishes reviewed source to GitHub. Source publication does not create
an installable release; packaging and deployment remain separate stages.
The catalog does not contain deployment settings, credentials, action schemas or
live readiness. Agents advertise their actions and requirements when they register.

## Package references

References use `github.com/<owner>/<repository>[/<module-path>]`, optionally
followed by `@vMAJOR.MINOR.PATCH` or `@stable`. GitHub HTTPS repository URLs also
work; an omitted version means `stable`. The shared
[`framework/agent/packages`](../framework/agent/packages/) package validates the
catalog and references. Its `Client.LoadCatalog`
accepts a configured public HTTPS source URL without requiring a Commander source
checkout. MCP defaults to
`https://raw.githubusercontent.com/defos-ai/commander/main/catalog/agents.yaml`,
with `AGENT_CATALOG_URL` as a deployment override. It fetches metadata only when
a tool is called; startup needs no catalog access. The framework has no built-in
source URL.

Loading is limited to 64 KiB and 30 seconds. Source URLs cannot contain credentials,
a query or a fragment; redirects are rejected. The default client sends no
credentials and does not use environment proxies.

The spawner resolves `stable` using the repository's promoted GitHub **Latest** release. Its tag
must be a root stable semantic version such as `v0.2.0`. For builder, that version
maps to `apps/agents/builder/v0.2.0`; for a standalone agent, it maps to `v0.2.0`.
Resolution never searches for another version if the selected release lacks an
agent package. The spawner saves the exact version before downloading the manifest
or assets or pulling an image. Retries of the same request reuse that version;
a new request resolves `stable` again.

Use `get_agent_catalog` to choose an agent, then pass its `package` and
`visibility` unchanged to the installation workflow described by the spawner's
responsibilities. Direct GitHub URLs and references work without catalog lookup.
Aliases are names for discovery, not installation inputs. Catalog inclusion does not
prove that the selected release already publishes a runnable package. See the
[package contract](../docs/agent-packages.md#catalog-and-stable-references).

## Private installations

For a private package, pass `"visibility":"private"` to the install
mission and `spawn-agent`. The spawner uses its environment `GITHUB_TOKEN` first,
then requests a managed secret scoped to `spawner-agent` through the existing
Playground/Admin flow. Public or omitted visibility stays token-free.
The token covers private `stable` resolution and release downloads. Catalog
loading remains anonymous, and Docker image pulls also remain anonymous.

## Maintenance

Edit the YAML to add or update a discovery entry. Keep aliases stable, descriptions
accurate and package paths explicit. Use [`AGENTS.md`](AGENTS.md) for schema
invariants and validation commands.
