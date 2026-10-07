# Working on Commander

Read this guide and the nested `AGENTS.md` files for the paths you change. Each
guide applies to its directory and descendants; nested guides add local rules.
The user's current instructions take precedence over repository guidance.

## Documentation and scope

- Keep a companion `AGENTS.md` beside every `README.md`. READMEs explain usage to
  people; agent guides describe code entry points, invariants, pitfalls and checks.
  Keep shared rules here and specific rules in the nearest relevant guide.
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
- Modules currently use `commander/...` with local replacements. Do not invent
  published versions or public `go get` instructions before the release work.
- `framework/` defines runtimes/contracts; `providers/` implements them; `apps/`
  hosts them. App `cli/factory.go` files select concrete providers and inject
  interfaces. Unsupported schemes must fail explicitly.
- [compose.yaml](compose.yaml) uses FS; [compose.full.yaml](compose.full.yaml)
  uses RabbitMQ, MongoDB and MinIO. Test stacks have separate projects and state.
  See [setup](docs/deployment.md) and the relevant component README for usage.

## Cross-component boundaries

- **Commander → Controller → Agent**: adapters submit operation commands; the
  controller selects agents; agents execute and propose follow-up work. Mission
  definitions own automation. Do not bypass the controller from a client.
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
- Read the [test guidance](framework/tests/AGENTS.md) when changing tests or
  fixtures; it covers shared-stack isolation and cleanup. Keep Docker lifecycle in
  setup/CI, outside Go test fixtures.
- Documentation-only edits need local link/anchor checks, plus README/AGENTS
  pairing checks when adding or moving files. Ignore changes need `git check-ignore`
  checks. Application test runs are unnecessary for those edits.
