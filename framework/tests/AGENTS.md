# Working on repository tests

This guide applies to test changes throughout the repository. Component guides
add their service prerequisites and checks.

## Layout and ownership

- Keep scenarios and assertions in test files (`*_test.go`, frontend `*.test.ts(x)`
  and browser specs). Use Arrange, Act, Assert: prepare
  inputs/dependencies, perform the behavior under test, then check its observable
  outcome. Separate phases with whitespace and clear names; do not add comments
  that merely label those phases. Workflows may repeat these stages when later
  actions depend on earlier outcomes.
- Put test data, temporary files, server/client setup and cleanup in each suite's
  `tests/fixtures/`. Put substitute implementations, including HTTP handlers,
  readers, executors and in-memory stores, in `tests/fakes/`. Large suites may use
  named subpackages below those roots. Keep existing executable fixture paths
  when deployment tooling depends on them.
- Keep assertion helpers beside scenarios. A handler that asserts a particular
  transport or failure behavior can remain with that scenario. Extract reusable
  behavior without adding generic callback frameworks or hiding the Act/Assert
  phases inside setup helpers.
- Fixtures may compose fakes. Fakes must not import fixtures or their parent test
  package. Go support packages use ordinary `.go` files; keep them in the existing
  module and out of production imports. Tests requiring access to
  package internals may keep the minimum support beside that package.
- Each test owns fresh state. Configure dependencies before starting workers;
  register cleanup immediately and cancel/join workers before inspecting final
  state or releasing resources. Data factories return independent values.
- Keep mutable fake state and synchronization private. Expose controlled changes
  and independent snapshots, copying nested slices, maps, bytes and pointers.
  Prefer immutable setup, explicit completion boundaries and existing owner loops
  over shared mutable test state. Keep mutexes where callers genuinely overlap;
  do not replace a mutex with an equivalent channel gate solely to remove it.
- Remove duplicated paths when a retained test already proves the same behavior
  at the appropriate boundary, including E2E coverage. Identify the covering test
  in the change summary. Preserve distinct protocol contracts, provider guarantees,
  fault injection, recovery and cancellation cases that broader E2Es do not prove.

## Choosing coverage

- Product coverage belongs in integration and end-to-end scenarios through public
  HTTP/MCP hosts or real provider services. Do not add unit test suites.
- Use deterministic local fixtures and scripted models. Tests must need no paid
  model requests or public websites. Keep waits bounded and failures diagnostic.
- Reuse and extend nearby fixtures and fakes. Fake stores must preserve contract
  behavior: independent snapshots, atomic mutations and explicit uncertain writes.
- For lifecycle changes, cover late outcomes, cancellation and recovery where
  relevant. For concurrency changes, preserve race-detector coverage and verify
  ownership/settlement behavior rather than relying on timing sleeps.
- Cancel and await background work in cleanup. A test must not leave consumers,
  runners or goroutines writing into the next test's state.

## Shared stack and cleanup

- Stack-backed API and MCP packages share state. Run them sequentially (`-p 1`
  when combined). Use two lightweight example-agent instances from the same pinned
  source commit in both test Compose files. Exercise Commander contracts through
  `analyze-text` and its proposed `write-summary` artifact; action behavior belongs
  in the standalone agent repositories. Do not add web/browser-agent hosts or Go
  imports to Commander tests. Chromium in integration CI serves Admin UI tests only.
- The API/MCP stacks include a spawner and local HTTPS package fixture. FS uses
  native child processes; full-provider tests use a loopback registry and Docker
  sibling containers. Keep the fixture under `apps/agents/spawner/tests/e2e/`;
  do not introduce resolver injection or deployment flags solely for these tests.
  Fixture missions are merged with bundled missions at fixture startup. Fixture
  agents launch only through controller-dispatched actions in the API/MCP scenarios,
  never during spawner startup. Catalog checks must permit fixture agents left
  running by the preceding suite without requiring one before the install scenario.
- Affinity scenarios use the spawned fixture's bounded `resume_url` readiness
  pause. Its only allowed target is the release fixture's `/gates/<UUID>` route;
  both test stacks expose controls on loopback `14276`. Release gates in cleanup
  before settling operations. Keep controls inside test fixtures; never relax
  spawner container privileges or mutate registrations to simulate an outage.
- Fixtures must settle active work before calling test-only cleanup (`ENV=tests`).
  Terminal operation status alone is insufficient if tasks or agent work remain.
- Preserve [`TestCleaner`](../testing/test_cleaner.go) and
  [`StateCleaner`](../testing/state_cleaner.go) boundaries: clear public operation,
  artifact and report data, retaining catalogs, registrations, execution/delivery
  records, the bus and host memory. Late results must not restore removed artifacts.
- Cleanup runs serially after callers stop submitting work; the saved-state check
  does not isolate concurrent processes. Do not bypass `ErrBusy` by deleting state.
- Tests must not manipulate Docker or shared volumes. Setup commands and CI own
  stack lifecycle, and must stop the test stack even when a test fails.

## Validation

- CI selection is declarative in [test-paths.yml](../../.github/test-paths.yml).
  Keep owner and downstream paths aligned with module imports, embedded hosts
  and stack dependencies. Reuse YAML anchors; do not add a custom selector.
- [CI checks](../../.github/tests/ci_test.go) discover first-party manifests from
  Git, including untracked, non-ignored files. New Go modules need a workspace
  entry; every component needs a literal owner path and a selected build, vet or integration command.
  Tooling under `.github/tests` is intentionally outside that release inventory.
- Keep test commands as direct `go build`, `go vet`, `go test` or `npm run test:e2e` lines
  with literal paths and `working-directory`. Owner gates use positive OR chains
  of `inputs.all` and `contains(fromJSON(inputs.changes), 'owner')`. The guard
  validates this wiring; it does not interpret arbitrary shell or GitHub expressions.
- Path filters use `some-with-excludes` so Markdown exclusions apply to every
  included dependency. Check additions, deletions, cross-owner renames and mixed
  documentation/code changes when changing these rules.
- Selected build/static checks gate the single Docker integration job. Reuse images, but
  stop each Compose project and remove its volumes before the next phase. Do not
  run shared-stack suites concurrently. Manual/release calls must select all suites.
- Keep the final `Tests passed` check: required suites that fail or get skipped
  must fail the gate; documentation-only skips may pass. Failed change detection
  must fall back to all suites.

Run from the repository root:

```sh
go build ./framework/...
go vet ./framework/...
```

For cross-host changes, start the appropriate test stack as described in the
[testing guide](README.md), then run:

```sh
go test -race -count=1 -failfast -p 1 -timeout=5m -v ./apps/adapters/api/... ./apps/adapters/mcp/...
```

Use `.github/composes/compose.test.yaml` for FS or
`.github/composes/compose.full.test.yaml` for full providers;
run one stack at a time because their ports overlap. Always run the corresponding
`docker compose down --volumes` afterward, including on failure. Match affected
[CI workflows](../../.github/workflows/) and report suites not verified.
For the full stack, set `COMMANDER_TEST_SPAWNER_RUNTIME=docker` and remove spawned
containers before teardown, scoped by both the test network and ownership label;
see the testing guide for registry setup and cleanup commands.

The shared spawn scenarios recognize Kubernetes results when
`COMMANDER_TEST_SPAWNER_RUNTIME=kubernetes`. This selects the expected output; it
does not provision a cluster or change the fixture inventory requirements. Keep
cluster/RBAC/Pod lifecycle verification in external setup, outside Go fixtures.
