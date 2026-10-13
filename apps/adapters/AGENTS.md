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

- Standalone MCP requires `MCP_TOKEN`; embedded hosts inject `host.BearerCredentials`
  through CLI options. Validate before opening listeners or providers. Protect
  every `/mcp` method, including notifications/resources, and keep `/healthz` public.
  Preserve Host/Origin checks alongside bearer verification.
- MCP `/aboutz` exposes only `runtime` (`native`, `docker`, `docker-compose`) and
  `application` (`commander-mcp`, `commander`). Configure `MCP_RUNTIME`/`--runtime`
  explicitly; embedded hosts set the application through run options. Only exact
  `GET`/`HEAD` are public and retain Host/Origin checks. Keep tokens, paths and
  shell commands out of metadata responses.
- Secrets belong in environment variables or explicit in-memory overrides. Never
  log them. Compose alone may bootstrap the shared MCP token from its dedicated
  Docker volume; hosts still read process environment credentials.
  Use `crypto/subtle` to compare supplied credentials with configured in-memory
  values. Basic authentication must evaluate both username and password matches.
  These adapters do not store password hashes or need a password-storage KDF.
  `commander-mcp credentials` is an explicit local lookup; on Linux it
  reads PID 1 only when that process uses the same executable. Lookup failures
  for a matching process must not fall back to a different environment token.
  `credentials --raw` prints only the token; it must not alter startup behavior.
- Compose's `mcp-credentials` one-shot initializer persists a shared token in a
  dedicated named volume, reusing it across restarts unless an explicit nonempty
  `MCP_TOKEN` replaces it. MCP and Playground mount this volume read-only and export
  the token to their environments; preserve an explicit `COMMANDER_MCP_TOKEN`
  client override. MCP stays PID 1 (`init: false`) for credential lookup. Keep
  OpenSSL explicit in the MCP image; API credential generation remains separate.
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
- [Integration CI](../../.github/workflows/test-integration.yml) runs API and MCP
  suites sequentially against each FS/full-provider stack.
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
