# Working on framework tests

## Choosing coverage

- These tests exercise framework behavior through public packages. Keep runtime
  scenarios in [`agent`](agent/), [`controller`](controller/), [`runner`](runner/)
  and [`comms`](comms/), grouped by feature.
- Prefer public HTTP/MCP scenarios in the adapter modules for cross-host behavior.
  Use framework unit tests for complex invariants, automation, recovery and
  failure paths; avoid tests that mirror implementation or duplicate coverage.
- Use deterministic local fixtures and scripted models. Tests must need no paid
  model requests or public websites. Keep waits bounded and failures diagnostic.
- Reuse and extend nearby fixtures, especially the runner's
  [`fixture_test.go`](runner/fixture_test.go). Fake stores must preserve contract
  behavior: independent snapshots, atomic mutations and explicit uncertain writes.
- For lifecycle changes, cover late outcomes, cancellation and recovery where
  relevant. For concurrency changes, preserve race-detector coverage and verify
  ownership/settlement behavior rather than relying on timing sleeps.
- Cancel and await background work in cleanup. A test must not leave consumers,
  runners or goroutines writing into the next test's state.

## Shared stack and cleanup

- Stack-backed API and MCP packages share state. Run them sequentially (`-p 1`
  when combined), and run browser suites separately against the same stack.
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

Run from the repository root:

```sh
go test -race -count=1 -timeout=3m ./framework/...
go vet ./framework/...
```

For cross-host changes, start the appropriate test stack as described in the
[testing guide](README.md), then run:

```sh
go test -race -count=1 -failfast -p 1 -timeout=5m -v ./apps/adapters/api/... ./apps/adapters/mcp/...
```

Use `compose.test.yaml` for FS or `compose.full.test.yaml` for full providers;
run one stack at a time because their ports overlap. Always run the corresponding
`docker compose down --volumes` afterward, including on failure. Match affected
[CI workflows](../../.github/workflows/) and report suites not verified.
