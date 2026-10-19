# Working on the agent catalog

Read the [root guide](../AGENTS.md) and
[package contract](../docs/agent-packages.md#catalog-and-stable-references).

- [`agents.yaml`](agents.yaml) is the official curated discovery source. Keep
  `schema_version: 1` and an `agents` map keyed by aliases. Each entry contains
  only `description` and `package`; the shared
  [`framework/agent/packages`](../framework/agent/packages/) package owns strict
  parsing, remote loading and package-reference validation.
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
- MCP/CLI alias integration is a later step. Do not document aliases or `@stable`
  as accepted by current installation inputs.

## Validation

The framework package tests validate this catalog through the public parser:

```sh
go test -race -count=1 -timeout=3m ./framework/tests/agent/packages/...
```

Keep `catalog/**` selected by the framework filter in
[`.github/test-paths.yml`](../.github/test-paths.yml). For documentation changes,
check local links and keep this guide beside the README.
