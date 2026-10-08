# Working on the web quality agent

## Source entrypoints

- [web_quality_agent.go](web_quality_agent.go) supplies the fixed agent type and
  default responsibilities. A nil action catalog gets fresh defaults; an explicitly
  empty catalog stays empty. The constructor does not grant permissions.
- [actions.go](actions.go) exposes the catalog; [actions/actions.go](actions/actions.go)
  owns action names, constructors and common input schemas. Keep CLI action-name
  selection consistent with this catalog when adding actions.
- [cli/](cli/) owns host configuration and providers. The bundled mission lives in
  [default-missions.json](../../controllers/default/data/default-missions.json);
  change automatic transitions there, not in action dispatch code.

## Scope and browser behavior

- Preserve the bounded, observational checks. Use shared URL parsing, hostname
  rules and HTTP helpers in [actions/http.go](actions/http.go). Scope compares exact
  hostnames without case; different ports or HTTP/HTTPS are allowed, but subdomains
  and `www` variants are different hosts.
- Discovery and link checks must retain explicit page/link limits, excluded-link
  reporting and redirect scope decisions. Follow-up proposals must use the resolved
  page URL and fit the mission's action schemas.
- Keep browser discovery in [actions/browser.go](actions/browser.go) through the
  environment abstraction. `CHROMIUM_PATH` must be absolute; an explicit discovery
  scope wins over configured paths. Rediscovery clears stale browser configuration.
- Missing or invalid Chromium configuration makes browser actions unavailable;
  HTTP-only actions remain usable. Browser actions require `network-access` and
  `execute-command`; permission grants belong to the host.
- Each execution uses a fresh browser profile and bounded context. Keep GET/HEAD
  restrictions and document navigation on the requested hostname, while allowing
  external resources needed to render pages. Do not block CDP listeners with
  synchronous commands to their own target.
- Browser sandboxing stays enabled by default for native runs; honor the explicit
  `CHROMIUM_NO_SANDBOX` setting used by container deployment.
- Keep screenshots grouped as desktop/mobile PNG files in one artifact, with
  descriptive metadata. Accessibility findings are bounded rendered checks;
  technology versions remain unverified hints from passive observations.

## Validation

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/web-quality/...
go vet ./apps/agents/web-quality/...
```

- [tests/actions_test.go](tests/actions_test.go) covers hostname/redirect scope,
  observed SEO data and browser discovery with deterministic fixtures. Add coverage
  for changed limits, scope or readiness without requiring a host browser.
- [Unit CI](../../../.github/workflows/test-unit.yml) runs this module. For actual
  browser execution, artifact persistence and the full review mission, use the
  [MCP/API test stack](../../../framework/tests/README.md#mcp-api-tests), which
  supplies browsers and local sites. No model key or public target is needed.
- When changing action output or proposals, check the bundled mission and adapter
  scenarios together so automatic checks still converge on a model continuation.
