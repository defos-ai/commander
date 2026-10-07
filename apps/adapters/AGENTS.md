# Working on adapters

## Boundaries and entrypoints

- Adapters query saved state through stores and send typed operation commands to
  the controller. They must not select agents, dispatch agent commands, or mutate
  operation records directly. Queries should remain usable without a live controller.
- MCP registration and input decoding start in [mcp/adapter.go](mcp/adapter.go);
  operation tools live in [mcp/operations.go](mcp/operations.go), and file resources
  in [mcp/artifacts.go](mcp/artifacts.go). HTTP-specific guidance is in
  [api/AGENTS.md](api/AGENTS.md).
- Each host owns its `cli/config.go`, `cli/factory.go` and `cli/cli.go`. Keep concrete
  providers in those factories, propagate startup failures, and reject unsupported
  schemes explicitly. Close listeners and command clients before provider clients.
- A client disconnect or adapter shutdown does not cancel accepted controller
  work. Do not add automatic command retries; query saved state after an uncertain
  response because repeating an assignment can create another task.

## MCP contracts

- Keep input schemas and decoded request validation consistent. `addTool` strictly
  decodes arguments; malformed inputs use JSON-RPC invalid-params errors, while
  tool execution failures use text content with `IsError`.
- Successful tool results carry JSON in text `content`. File references use
  resource-link blocks; do not duplicate result payloads in `structuredContent`.
- Preserve `missions://artifacts/{id}` and its `/files/{file_id}` resource shape.
  Validate record IDs and artifact membership before retrieving bytes. IDs are
  never filesystem paths. Empty files need explicit blob content.
- Keep summaries small and use detail tools for schemas, task input and results.
  Readiness reflects the selected agent's advertisement and current lease; catalog
  metadata alone does not establish an available executor.
- Continuation decisions must retain the task and business version used to reject
  stale choices. Questions record actual operator answers; expiry is not approval.
- Keep pagination cursors bound to the resource and operation. Bound event waits
  and honor cancellation without changing saved operation state.

## Validation

- Host tests live under each adapter's `tests/host/`; public HTTP/MCP scenarios
  live in `tests/` and use the running test stack. Group behavior by feature and
  exercise commands, saved queries and resulting evidence through public routes.
- [API CI](../../.github/workflows/test-api.yml) and
  [MCP CI](../../.github/workflows/test-mcp.yml) cover FS and full-provider stacks.
  Follow the [stack setup](../../framework/tests/README.md#mcp-api-tests), then run
  affected modules from the repository root; when checking both, keep `-p 1`:

  ```sh
  go test -race -count=1 -failfast -p 1 -timeout=5m ./apps/adapters/api/... ./apps/adapters/mcp/...
  go vet ./apps/adapters/api/... ./apps/adapters/mcp/...
  ```

- Settle active work before fixture cleanup. Expose cleanup only with `ENV=tests`;
  preserve catalogs, registrations, deliveries, bus state and host memory. Late
  results must not recreate removed artifacts. Tests do not manage Docker or volumes.
- Run browser suites separately from adapter suites sharing the same stack, and
  always stop the test stack after testing, including after failures.
