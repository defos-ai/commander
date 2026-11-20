# Release notes v0.7.1

## Commander v0.7.1

Configure MCP's accepted HTTP hosts through `MCP_ALLOWED_HOSTS`, following the
API's existing `API_ALLOWED_HOSTS` convention.

- Use the same precedence in both adapters: explicit `--allowed-hosts`, then the
  service's nonempty environment variable, then `localhost,127.0.0.1,::1`.
- Configure MCP hosts through the environment in Compose, retaining loopback
  addresses and the internal `mcp` hostname by default.
- Preserve Host and Origin validation, authentication and existing client URLs.
  Custom host lists replace the defaults; include every required public,
  loopback and internal hostname.

Upgrade or rebuild the MCP image when adopting the updated Compose configuration;
v0.7.0 does not read `MCP_ALLOWED_HOSTS`. Existing `--allowed-hosts` flags continue
to take precedence. To use environment configuration in a deployment wrapper,
update the MCP image first, then remove its `--allowed-hosts` override.
Admin and MCP clients need no changes, and this update requires no saved-state
migration or external-agent rebuild.

[MCP configuration](https://github.com/defos-ai/commander/blob/v0.7.1/apps/adapters/mcp/README.md) ·
[API configuration](https://github.com/defos-ai/commander/blob/v0.7.1/apps/adapters/api/README.md#hosts-origins-and-proxies) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.7.0...v0.7.1)

## Commander builder agent v0.7.1

- Align Commander module requirements with v0.7.1.
- Keep existing workspace data and managed credentials. Builder behavior is
  unchanged from v0.7.0.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.7.1`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.7.1/apps/agents/builder/README.md)

## Commander spawner agent v0.7.1

- Align Commander module requirements with v0.7.1.
- Keep existing installation records and child-lifecycle handling. Spawner
  behavior is unchanged from v0.7.0.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.7.1`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.7.1/apps/agents/spawner/README.md)
