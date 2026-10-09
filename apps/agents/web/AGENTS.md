# Working on the web agent

## Entry points

- [web_agent.go](web_agent.go) fixes the `web-agent` type and supplies a fresh
  catalog for nil `Actions`; an explicitly empty catalog stays empty. The
  constructor does not grant permissions.
- [actions.go](actions.go) exposes the catalog; [actions/actions.go](actions/actions.go)
  owns HTTP action names and input schemas. Keep CLI selection aligned with it.
- [cli/](cli/) configures the host and providers. HTTP actions need only
  `network-access`; keep browser drivers and executable dependencies out of this module.

## HTTP scope and proposals

- [actions/http.go](actions/http.go) owns bounded response parsing and hostname
  scope. Compare exact hostnames without case; schemes and ports may differ,
  while subdomains and `www` variants remain outside scope.
- Preserve page/link limits, excluded-link reporting and redirect scope decisions.
  Follow-up proposals must use resolved page URLs and fit the target action schema.
- `discover-pages` proposes HTTP checks plus actions owned by the browser agent.
  `discover-pages-no-browser` proposes only links, SEO and technology checks.
  Proposals do not require those executors locally: mission preflight and controller
  scheduling determine who runs them.
- [Bundled mission rules](../../controllers/default/data/default-missions.json)
  own automatic transitions. Never execute follow-up work directly from discovery
  or silently omit rendering from the with-browser mission.
- SEO evidence and metadata are bounded. Technology versions are unverified
  passive observations; retain redaction of URL credentials, queries and fragments.

## Validation

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/web/...
go vet ./apps/agents/web/...
```

[tests/actions_test.go](tests/actions_test.go) covers scope, redirects, proposal
coverage and observed SEO data without a browser or public site. CLI tests retain
provider startup failures and credential redaction. Use the
[MCP/API stack](../../../framework/tests/README.md#mcp-api-tests) for cross-agent
automation, browser evidence and reports.
