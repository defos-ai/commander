# Working on the agent sources

Read the [root guide](../AGENTS.md) and
[package contract](../docs/agent-packages.md#sources-and-stable-references).

- [`agents.yaml`](agents.yaml) is the official curated discovery source. Keep
  `schema_version: 1` and an `agents` map keyed by aliases. Each entry contains
  required `description`, `source` and `visibility` fields only. Visibility is
  exactly `public` or `private`; builder, spawner, browser and example are public,
  and the other official entries are private. Group public entries first in the
  source file, keeping spawner immediately after builder. The shared
  [`framework/agent/packages`](../framework/agent/packages/) package owns strict
  parsing, remote loading and package-reference validation.
- Visibility describes the GitHub repository; package trust and Docker image
  visibility are separate. Commander client integration owns source lookup and
  passes the selected `source` reference as `spawn-agent`'s `package` input,
  preserving visibility. Never load the
  sources in spawner. Private installs use environment `GITHUB_TOKEN` first,
  then the shared managed `GITHUB_TOKEN` through the existing
  Playground/Admin exchange. Public or omitted visibility stays anonymous.
  The spawner owns public and private `stable` resolution and saves the exact pin
  for retries. Sources loading remains anonymous; private GHCR pulls may reuse
  the installation token. Other registries and public installs remain anonymous.
- Describe each agent's intended capabilities; sources metadata does not establish
  release availability or live action readiness. Builder prepares source through
  `checkout-template`, uses `build-agent` for Codex generation, Go validation
  and source artifacts, then optionally publishes a reviewed artifact through
  `publish-agent` within the build mission after operator agreement. Source
  publication does not create an installable release.
  Source aliases are separate from registered agent types and action names.
- `log-observer` observes external application, infrastructure and security logs
  and reports findings. Keep it separate from Commander's execution progress logs;
  vulnerability validation belongs to cybersec and code fixes belong to fixer.
- Keep package references explicit, including the builder's module path. Official
  entries use `@stable`; this means the repository's promoted root GitHub Latest
  stable semantic version, mapped to a module tag when applicable. Do not guess
  a tag, search for an older package or claim an unpublished release is available.
- This source list supports discovery; it does not enforce an installation allowlist.
  Spawner enforces its own trusted-owner policy independently; adding an entry
  here cannot authorize a different owner. External sources prompt an
  execution-scoped `action.Question` before download/launch; the existing
  operator-first window allows model fallback. No owner trust is saved.
  Keep deployment settings, credentials, action schemas and readiness out of it.
  The spawner owns manifest and executable downloads. Shared metadata code must
  remain independent of the spawner and must not require this checkout at runtime.
- MCP exposes `get_agent_sources` for discovery, returning public entries first
  and sorting aliases alphabetically within each visibility group, with package
  selectors breaking ties. It loads this document anonymously on demand and
  returns name, description, source and visibility only. Do not load during
  startup or introduce a source database, seed workflow or saved trust.
  Version resolution belongs to the spawner;
  sources failure must not prevent direct installs. Spawner action
  inputs accept GitHub URLs, bare references, `@stable` or exact pins;
  aliases remain discovery names, not installation inputs.

## Validation

Build the shared public parser before running installation integration checks:

```sh
go build ./framework/agent/packages/...
```

Keep `sources/**` selected by the framework filter in
[`.github/test-paths.yml`](../.github/test-paths.yml). For documentation changes,
check local links and keep this guide beside the README.
