# Working on the agent catalog

Read the [root guide](../AGENTS.md) and
[package contract](../docs/agent-packages.md#catalog-and-stable-references).

- [`agents.yaml`](agents.yaml) is the official curated discovery source. Keep
  `schema_version: 1` and an `agents` map keyed by aliases. Each entry contains
  required `description`, `package` and `visibility` fields only. Visibility is
  exactly `public` or `private`; all official entries are public. The shared
  [`framework/agent/packages`](../framework/agent/packages/) package owns strict
  parsing, remote loading and package-reference validation.
- Visibility describes the GitHub repository; package trust and Docker image
  visibility are separate. Commander client integration owns catalog lookup and
  passes the selected package and visibility unchanged to `spawn-agent`; never load the
  catalog in spawner. Private actions use environment `GITHUB_TOKEN` first,
  then a managed secret scoped to `spawner-agent` through the existing
  Playground/Admin exchange. Public or omitted visibility stays anonymous.
  The spawner owns public and private `stable` resolution and saves the exact pin
  for retries. Catalog loading and Docker image pulls remain anonymous.
- Describe implemented capabilities. Builder currently prepares source through
  `checkout-template`; do not advertise coding, builds or publication as complete.
  Catalog aliases are separate from registered agent types and action names.
- Keep package references explicit, including the builder's module path. Official
  entries use `@stable`; this means the repository's promoted root GitHub Latest
  stable semantic version, mapped to a module tag when applicable. Do not guess
  a tag, search for an older package or claim an unpublished release is available.
- This catalog supports discovery; it does not enforce an installation allowlist.
  Keep deployment settings, credentials, action schemas and readiness out of it.
  The spawner owns manifest and executable downloads. Shared metadata code must
  remain independent of the spawner and must not require this checkout at runtime.
- MCP exposes only `list_agent_packages` for package discovery; version resolution
  belongs to the spawner. `AGENT_CATALOG_URL` is deployment-owned on MCP; loading
  is lazy and catalog failure must not prevent direct installs. Spawner action
  and CLI inputs accept GitHub URLs, bare references, `@stable` or exact pins;
  aliases remain discovery names, not installation inputs.

## Validation

The framework package tests validate this catalog through the public parser:

```sh
go test -race -count=1 -timeout=3m ./framework/tests/agent/packages/...
```

Keep `catalog/**` selected by the framework filter in
[`.github/test-paths.yml`](../.github/test-paths.yml). For documentation changes,
check local links and keep this guide beside the README.
