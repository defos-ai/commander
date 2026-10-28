# Agent sources

Find reusable agents for workflows such as building software, browsing websites
and security operations. Agent sources pair capability descriptions with release
references, so operators and model clients can select specialist agents to install.
Browse [`agents.yaml`](agents.yaml), or ask Playground:

> Install the browser agent.

API and MCP read the deployment's saved sources. For MCP clients, use
`get_agent_sources` and use the selected source reference and
visibility to the [installation workflow](../apps/agents/spawner/README.md#during-a-mission).
You can also install any compatible GitHub package directly.

## Source references

- `https://github.com/owner/repository` selects its promoted **Latest** release.
- `github.com/owner/repository@stable` does the same.
- `github.com/owner/repository@v1.2.3` selects that exact version.
- Include a module path for agents in a monorepo, such as
  `github.com/defos-ai/commander/apps/agents/builder@stable`.

Source aliases help discovery; installation uses the source reference.
Only install packages from authors you trust. Check live action readiness in
Admin after installation.

## Private installations

Pass `"visibility":"private"`. Supply `GITHUB_TOKEN` on the spawner or through
its Playground/Admin secret form. Private GHCR images also require package access.
See [private package setup](../apps/agents/spawner/README.md#private-github-packages).

## Maintenance

To add an agent, add an entry with its capabilities, source and repository visibility:

```yaml
schema_version: 1
agents:
  example:
    description: Demonstrates custom Commander actions.
    source: github.com/defos-ai/commander-example-agent@stable
    visibility: public
```

Add deployment entries through `POST /api/v1/agent-sources` with `name`,
`description`, a full canonical `source` selector and `visibility`. New entries
are untrusted. Read them through `GET /api/v1/agent-sources` or MCP's
`get_agent_sources`. Default seeding from `agents.yaml` is deferred, so a new
deployment's saved source list starts empty.

To approve or revoke an exact release, use
`PATCH /api/v1/agent-sources/{source}` with the current `version` and
`trusted: true` or `false`. Encode the full source reference as one path segment. Approval
requires an exact version; `@stable` remains a moving discovery reference.
Read the entry before retrying an uncertain write. Saved trust does not yet
enforce installation or secret access.

See [package publishing](../docs/agent-packages.md) and [AGENTS.md](AGENTS.md)
for validation and source rules.
