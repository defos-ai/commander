# Working on Commander

Read this guide and the nested `AGENTS.md` files for the paths you change. Each
guide applies to its directory and descendants; nested guides add local rules.
The user's current instructions take precedence over repository guidance.

## Documentation and scope

- Keep a companion `AGENTS.md` beside every `README.md`. READMEs explain usage to
  people; agent guides describe code entry points, invariants, pitfalls and checks.
  Keep shared rules here and specific rules in the nearest relevant guide.
- Keep human docs short and actionable: lead with the next step, show working
  commands, and explain only the choices or limits a reader needs to act on.
  Put implementation contracts in the nearest agent guide or link to source;
  avoid repeating them across READMEs. Preserve essential setup, security and
  recovery instructions, without copying every removed detail into `AGENTS.md`.
- Prefer small, reviewable changes. Explain the scope before broad renames,
  dependency changes or refactors, and avoid unrelated cleanup.
- Preserve uncommitted work. Do not commit, publish, change repository visibility
  or discard runtime data unless the task authorizes it.
- The project is in development: do not add migrations, legacy formats or
  compatibility aliases unless the task needs them.
- Use clear names and short functions. Comment non-obvious constraints rather
  than repeating signatures or code. Prefer links to source over copied inventories.
- Maintain one root `.gitignore`. Keep environment files and generated/runtime
  output untracked. Ignore rules do not remove tracked files or secrets from history;
  never print credentials in diagnostics.

## Workspace

- Go modules are listed in [go.work](go.work); there is no root `go.mod`.
  Use its Go version and explicit module paths from the repository root:
  `go test ./framework/...`, not root `go test ./...`.
  `.github/tests` is a separate CI tooling module, outside the release workspace;
  run its configuration checks with `GOWORK=off` and `-run '^TestCI'`.
- Module paths match `github.com/defos-ai/commander/<module-directory>`. Keep
  checkout-local wiring in `go.work`, not sibling-directory `replace` directives
  in `go.mod`. The workspace maps internal module versions to local directories;
  update these mappings when changing internal dependency versions.
  Internal requirements target the prepared release version. Version pins do not
  mean a release is published; do not add public `go get` instructions before it is.
  External consumers use the [local setup](apps/agents/README.md#develop-an-agent-in-a-private-repository).
- Follow [releases](docs/releases.md) to prepare one version across all modules,
  update requirements, run validation and publish all tags together.
- Release publishing runs through `make-release.yml` after all suites in
  `test-release.yml` and native build checks succeed for the same commit.
  Keep `build-binaries.yml` and `push-*.yml` reusable-only; native assets must come
  from that run. `test.yml` calls reusable `test-unit.yml` before
  `test-integration.yml`; integration jobs must depend on passing basic checks.
  Maintain declarative dependency filters in `.github/test-paths.yml`; manual and
  release runs always select every suite. Docker suites share one runner and
  reuse images, with separate test state for each phase.
  Keep independently triggered workflows' concurrency groups distinct so nested
  calls cannot cancel their release parent. Retry failed publication jobs in the
  same run; never overwrite published release assets.
  Promote Docker `stable` tags and the GitHub `Latest` release only after the
  complete release publishes, using the image digests from that run. Keep the
  newer-release check and serialized release concurrency so maintenance releases
  cannot move these pointers backward. Native asset names omit the version;
  use `releases/latest/download` for quick start and version URLs for pinning.
  Web/browser images are released by their standalone repositories; keep them
  out of Commander's image publishing and `stable` promotion.
- `framework/` defines runtimes/contracts; `providers/` implements them; `apps/`
  hosts them. App `cli/factory.go` files select concrete providers and inject
  interfaces. Unsupported schemes must fail explicitly.
- [compose.yaml](compose.yaml) uses RabbitMQ, MongoDB and MinIO. Spawner is the
  only agent started by default; builder is opt-in through the `builder` profile.
  Other agents launch on demand through chat/MCP as sibling containers.
  Keep execution checkpoints while children exist and scope
  teardown by deployment network plus the spawn ownership label.
  Its `secrets-key` initializer persists the encryption key in a dedicated volume;
  only API and controller mount it read-only and export it before exec. Retain
  saved keys across restarts and reject invalid state or conflicting overrides.
  Keep this volume separate from `api-credentials`, `mcp-credentials` and database
  storage; restoring encrypted data requires its original key. API and MCP login
  initializers retain their separate credentials across restarts. API,
  Playground and the Admin mission initializer read the local API password from
  a read-only mount; do not reuse it for a custom API destination or automatically
  sign in browser Admin. Admin seeds bundled missions through the API, preserving
  saved definitions; controllers do not load mission files.
  Playground's separate `playground-state` volume retains client history/settings
  and locally encrypted provider keys; never mix it with the bootstrap volumes.
  Do not distribute the key through the shared agent environment.
  Test stacks in [.github/composes](.github/composes/)
  have separate projects and state.
  See [setup](docs/deployment.md) and the relevant component README for usage.

## Cross-component boundaries

- **Commander → Controller → Agent**: adapters submit operation commands; the
  controller selects agents; agents execute and propose follow-up work. Mission
  definitions own rules. Valid nonempty proposals run automatically unless a
  matching rule requires a model decision. Aggregation covers the saved direct
  children of one execution, never recursive descendants. Do not bypass the
  controller from a client.
- Framework code must not import apps or providers. Keep driver dependencies,
  storage locks, transactions and storage revisions within providers.
- Only one active controller per database is supported. Registration and backend
  support do not imply distributed ownership or global capacity reservations.
- Coordinate domain, persistence, consumers and recovery changes. Mutate saved
  operation state atomically and reconcile uncertain writes before new dispatch.
- Agents upload artifact bytes; controllers persist accepted metadata. A broker
  acknowledgement is distinct from a result-acceptance receipt after persistence.
  Preserve these boundaries and never claim exactly-once execution.

## Validation

- Run relevant checks from the scoped guide and [.github/workflows](.github/workflows).
  Shared-contract changes need affected downstream checks. Report anything unverified.
- Prefer public API/MCP scenarios for cross-host behavior and unit tests for
  complex invariants, recovery and failures. Use deterministic fixtures and bounded
  waits; avoid implementation-mirroring tests or paid model calls.
- Follow the shared [test rules](framework/tests/AGENTS.md) throughout the repo:
  `tests/fixtures` owns setup/data, `tests/fakes` owns substitutes, and test files
  own Arrange/Act/Assert scenarios. Keep synchronization private and remove
  duplicate coverage only after identifying the retained covering scenario.
  Keep Docker lifecycle in setup/CI, outside Go test fixtures.
- Documentation-only edits need local link/anchor checks, plus README/AGENTS
  pairing checks when adding or moving files. Ignore changes need `git check-ignore`
  checks. Application test runs are unnecessary for those edits.
