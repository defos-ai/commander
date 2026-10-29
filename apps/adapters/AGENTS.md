# Working on adapters

## Boundaries and entrypoints

- Adapters query saved state through stores and send typed operation commands to
  the controller. They must not select agents, dispatch agent commands, or mutate
  operation records directly. Queries should remain usable without a live controller.
- Operation/task/report/event loading is shared through
  [`queries.Operations`](../../framework/queries/operations.go). Map its derived
  views into each adapter's wire format; keep pagination validation and bounded
  wait deadlines here. Event waits end on saved-work settlement, not terminal
  operation status alone, and reread events after observing settlement.
- Every operation response includes a required `progress` projection: sparse
  nonnull `task_counts`, `total_tasks`, `finished_tasks`, `pending_decisions`,
  `pending_questions` and `settled`. Use the query view's assessment or assess the
  returned command snapshot once; never query again to enrich a command response.
  Only succeeded, failed and cancelled tasks count as finished. Interrupted work
  remains unsettled, including after the operation ends. Terminal operations
  expose zero actionable decisions/questions without deleting their history.
- MCP registration and input decoding start in [mcp/adapter.go](mcp/adapter.go);
  operation tools live in [mcp/operations.go](mcp/operations.go), and file resources
  in [mcp/artifacts.go](mcp/artifacts.go). HTTP-specific guidance is in
  [api/AGENTS.md](api/AGENTS.md).
- [MCP `operation_logs`](mcp/logs.go) uses `queries.Operations.ListLogs` to check
  operation and optional `task_id` membership before reading the required `ExecutionStore`. Keep saved
  progress readable without a controller and after terminal status. Entries expose
  flat operation/task provenance; logs do not change lifecycle state.
  Use log IDs as cursors and preserve the input cursor on empty pages. Exact
  task/agent/action filters intersect; reject malformed or explicitly empty/null
  filters before store reads. `order` is `asc` by default or `desc`, both over
  committed append order. Cursor continuation is exclusive in that direction;
  descending `next_cursor` loads older history. Follow new entries with `asc`
  from the first/newest descending entry ID. Reject supplied empty/null/invalid
  order values before store reads.
  CLI factories select execution storage through `DATABASE_URL`
  alongside the operation store and share the cached Mongo client.
- Each host owns its `cli/config.go`, `cli/factory.go` and `cli/cli.go`. Keep concrete
  providers in those factories, propagate startup failures, and reject unsupported
  schemes explicitly. Close listeners and command clients before provider clients.
- A client disconnect or adapter shutdown does not cancel accepted controller
  work. Do not add automatic command retries; query saved state after an uncertain
  response because repeating an assignment can create another task.

## MCP contracts

- Runtime-session tools query retained records through `queries.Sessions` using
  optional intersecting scope/exact-owner filters and send
  controls through the controller. Preserve `request_id`, abandonment's retained
  lost state and affinity, and `session_mode` in action summaries/contracts.
  See [the session contract](../../docs/runtime-sessions.md#queries-and-controls).
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
  log them. Compose alone may bootstrap the API password and MCP token from
  separate Docker volumes; hosts still read process environment credentials.
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
- `create_mission` accepts a flat `missions.Mission` and shares its validation
  with the API and stores. Root `objectives` is a required ordered array of one
  or more nonblank strings.
  Optional `action_definitions` contains up to 100 desired contracts, unique by
  name and referenced by required/optional actions. Advertise
  strict contract fields and explicit root object-schema types; shared mission
  validation resolves schemas locally. Definitions are metadata only, never
  installed actions, secret values or readiness evidence.
  Invalid definitions are invalid-params errors;
  duplicate names are tool errors, including identical submissions. Call
  `MissionStore.Create` directly with a bounded context, never `Upsert` or an
  operation command. Creation does not check agent availability or start work.
  Sanitize backend errors and direct callers to `describe_mission` before retrying
  uncertain writes. Keep the advertised schema aligned with strict decoding,
  including nullable optional collections and open JSON Schema documents.
  Optional `completion` is a mission-wide setting with required `decision_by`
  of `model` or `operator` when present; omitted/null stays absent. Preserve it
  in mission reads and operation snapshots. Operator policy rejects MCP completion;
  omitted/model policy retains ordinary model completion. HTTP completion stamps
  `operator`, MCP stamps `model`; keep `completed_by` out of public inputs and
  expose its saved value in operation reads. It identifies the submission path,
  not proof of human identity. Rejections leave the operation/report unchanged;
  direct operators to Admin's **Complete operation** or the authenticated HTTP
  completion API without inventing URLs. Question answers and fallback do not approve completion.
- `propose_completion` accepts final output for review and stamps model origin;
  the HTTP proposal route stamps operator origin. Public bodies cannot choose
  proposal identity, timestamp or origin. Retain the saved proposal in command and
  detail responses without creating a report, resolving decisions or completing
  the operation. For operator-controlled missions, guide clients to save once,
  then ask for operator review in Admin. Proposals do not pause work or approve it;
  re-read after ambiguous writes rather than retrying automatically.
- `update_mission` accepts that same complete flat definition. Read
  `describe_mission` first and keep its unique name unchanged. Require the target
  through `MissionStore.Get` before `Upsert`; unknown names must not create a
  mission. Updates apply to future operations only. Bound the read/write to one
  10-second context, return a cloned definition and sanitize failures with
  read-back guidance. Never retry uncertain updates or dispatch work from editing.
- Preserve `missions://artifacts/{id}` and its `/files/{file_id}` resource shape.
  Validate record IDs and artifact membership before retrieving bytes. IDs are
  never filesystem paths. Empty files need explicit blob content.
- Keep summaries small and use detail tools for schemas, task input and results.
  Readiness reflects the selected agent's advertisement and current lease; source
  metadata alone does not establish an available executor.
  MCP instructions direct clients to registered agent responsibilities and action
  schemas. Keep installation guidance on the spawner's registered metadata and
  the `install-agent` mission, not in adapter constants or known-package lists.
  Preserve responsibilities in both `list_agents` and `describe_agent` so any
  client can discover an agent's usage guidance.
- MCP agent discovery lives in `mcp/sources.go`: `get_agent_sources` loads the
  official `sources/agents.yaml` through the caller-owned `SourceClient` and
  returns a `sources` list with public
  entries first, then aliases sorted within each visibility group and package
  selectors breaking ties. Entries expose only name, description, source and
  visibility. The factory constructs and closes the anonymous client; no source
  database or trust approvals are involved. Fetch only on tool calls, never
  during startup. Keep bounded loading/parse failures sanitized and tool-local.
  Direct GitHub URLs and references work without a listed source; aliases are
  discovery names only. Resolution and durable pinning belong to the spawner,
  including private `@stable` references. Keep MCP guidance generic and follow
  agent responsibilities; do not reintroduce a package-resolution tool or acquire
  spawner tokens in MCP. Readiness and manifest/asset validation remain in agent
  registration and spawning.
  Remote source clients remain anonymous, with no environment token forwarding.
  Existing managed-secret handoff uses explicit preflight `agent_type` and
  `missing_requirements` of kind `secret` for actions without ready candidates.
  Sensitive host configuration remains kind `configuration`, even with
  `sensitive: true`. MCP instructions direct operators to
  Admin Secrets; never solicit secret values through chat, tools or questions,
  or equate a saved secret with an agent becoming ready.
- Running task queries also project `agent_type` and `missing_requirements` from
  the assigned agent's live work. The shared query service applies
  `operations.AssessTaskRequirements` with one assessment time per response.
  Match the saved execution identity and retain its deadline; suppress waits for
  terminal operations. Inventory failures omit this optional observation while
  keeping saved results readable. Never infer a task wait from sync requests.
  Task summaries include `deadline_at` only alongside live missing requirements;
  detail queries retain the saved deadline even after that observation clears.
- Continuation decisions must retain the task and business version used to reject
  stale choices. Answer submissions carry adapter-assigned origin: HTTP uses
  `operator`, MCP uses `model`. Keep `answered_by` out of public answer inputs;
  expose it on saved questions. Origin records the submission path, not a proof
  of human identity. timeout_seconds sets the operator-first waiting window and
  fallback_at is the earliest model answer time. Operators may answer throughout;
  unanswered questions remain pending and blocking after this deadline. Passing
  the deadline does not supply an answer or approval.
- MCP `get_operation` and `list_operations` expose read-only `affinity` through
  `Operation.AssessAffinity`. Preserve `for_actions`, the saved owner and any
  history error even without live inventory. Queued tasks remain unassigned.
  Explain `same-agent` as placement with no fallback; saved preflight is not
  evidence of current capacity. Command responses retain their saved-state shape.
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
- The stacks use pinned source builds of the lightweight example agent. Standalone
  repositories run agent-specific suites. Always stop the Commander test stack
  after testing, including after failures.
