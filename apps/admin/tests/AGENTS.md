# Working on Admin tests

- Keep scenarios and assertions in the parent `*_test.go` files. Small helpers
  that run commands and assert their output can stay beside those scenarios.
- [`fixtures/`](fixtures/) owns test data, temporary files, clients and server
  setup/cleanup. [`fixtures/api.go`](fixtures/api.go) starts authenticated API
  servers; register cleanup immediately when acquiring a resource.
- [`fakes/`](fakes/) owns substitute implementations, including stateful HTTP
  handlers and readers. Keep mutable state and synchronization private. Expose
  controlled changes and independent snapshots, copying nested slices/pointers.
- Fixtures may compose fakes. Fakes must not import fixtures or the parent test
  package. Use ordinary `.go` files in these imported support packages; keep them
  inside the existing Admin module and out of production imports.
- Each test creates its own state. Data factories return fresh values; constants
  may be shared. Preserve request identity/order, exact JSON bytes and uncertain
  outcomes when moving support code.
- A handler that asserts a scenario's specific transport or failure behavior can
  stay with that scenario. Extract reusable behavior without adding callback
  frameworks or moving assertions away from the behavior they describe.

Use the parent [Admin checks](../AGENTS.md) and shared
[test guidance](../../../framework/tests/AGENTS.md). To check package wiring
without executing scenarios, run from the repository root:

```sh
go test -run '^$' ./apps/admin/tests/...
```
