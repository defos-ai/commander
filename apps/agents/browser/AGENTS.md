# Working on the browser agent

## Entry points

- [browser_agent.go](browser_agent.go) fixes the `browser-agent` type and supplies
  fresh rendering actions for nil `Actions`; an explicitly empty catalog stays
  empty. The constructor does not grant permissions.
- [actions.go](actions.go) exposes the two-action catalog.
  [cli/](cli/) configures providers and grants `network-access` and `execute-command`.
- HTTP crawling and static checks belong to the [web agent](../web/README.md).
  Mission rules, not either agent, own automatic transitions.

## Rendering and dependencies

- [actions/url.go](actions/url.go) validates HTTP(S) URLs and exact hostname scope.
  Schemes and ports may differ; subdomains and `www` variants are different hosts.
- Browser discovery in [actions/browser.go](actions/browser.go) uses the environment
  abstraction. `CHROMIUM_PATH` must be absolute; an explicit discovery scope wins.
  Rediscovery clears stale configuration. Missing or invalid requirements make
  browser actions unavailable without disabling the separate web agent.
- Each execution uses a fresh profile and bounded context. Keep GET/HEAD request
  restrictions and document navigation on the requested hostname, allowing external
  page resources. Never issue synchronous CDP commands to their own target inside
  blocked CDP listeners.
- Native sandboxing stays enabled by default. Honor explicit
  `CHROMIUM_NO_SANDBOX` for container deployment.
- Group desktop/mobile PNGs in one artifact with descriptive metadata. Accessibility
  findings remain bounded rendered checks, not complete audit claims.

## Validation

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/browser/...
go vet ./apps/agents/browser/...
```

[tests/actions_test.go](tests/actions_test.go) covers dependency discovery, explicit
scope and rediscovery without launching a host browser. Use the
[MCP/API stack](../../../framework/tests/README.md#mcp-api-tests) for real rendering,
artifact persistence and cross-agent mission automation. No model key or public
target is needed.
