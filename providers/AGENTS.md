# Working on providers

Applies to this tree; inherit the [repository guidance](../AGENTS.md). Each
backend adds its own `AGENTS.md`. Human setup and backend selection belong in
[README.md](README.md).

## Boundaries and contracts

- Each backend is an independent Go module. There is no `commander/providers`
  module; keep imports and dependencies within the chosen implementation.
- Implement framework interfaces in domain packages. Keep concrete drivers,
  storage formats, locks, transactions and storage revisions inside providers;
  hosts construct them in their factories. Do not move concrete dependencies
  into framework contracts or import app hosts here.
- Validate connection configuration and unsupported modes explicitly. Constructor
  errors must reach the host; preserve cancellation and deadlines during I/O.
- Operation writes must implement the framework
  [store contract](../framework/domain/operations/operation_store.go): mutate the
  latest saved state and commit related records atomically. Keep task and event
  records separate, and preserve uncertain-write errors for caller reconciliation.
- Retryable transaction/read callbacks use their supplied context, keep results
  local to each attempt and perform no external effects. Storage revisions must
  remain separate from continuation versions used for business decisions.
- Preserve immutable executions and reports. File stores write bytes; artifact
  stores accept metadata. Never publish metadata merely because an upload succeeds.
  Verify artifact size and SHA-256 when reading, and reject conflicting reports
  while accepting identical repeated report writes.
- Bus settlement belongs to each delivery, even when message IDs match. Preserve
  the distinction between transport acknowledgement and persisted result receipts.
  Backend durability does not provide execution deduplication or controller ownership.

## Validation and cleanup

- Use the affected backend's test/vet commands and service prerequisites below
  this directory. Run commands from the repository root with explicit module paths.
- For changed contracts, exercise affected providers and downstream consumers;
  follow the [test guide](../framework/tests/README.md) for cross-host scenarios.
- Integration-test environment variables opt into real services. A passing run
  with integration tests skipped is not service coverage; report skipped checks.
- Keep service setup/teardown outside Go tests. Use dedicated test state, bounded
  waits and the backend's fixtures; do not clear another host's data.
- Test cleaners must compose with `framework/testing.StateCleaner` so work settles
  first. Preserve catalogs, registrations, executions, bus state and artifact
  deletion markers that prevent late results from restoring removed metadata.
