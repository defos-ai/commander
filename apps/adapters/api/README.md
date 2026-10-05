# HTTP API adapter

## Overview

The API adapter lets HTTP clients inspect Commander missions, actions, agents,
saved operation state, reports and artifacts through domain stores, and send operation commands to
the controller over the bus.

`GET`/`HEAD /healthz` checks liveness; `/readyz` checks metadata database
readability. These probes are public on every host for load balancers. Readiness
does not check ongoing broker connectivity, file storage or controller availability.

## Quick start

From the repository root, with `API_USERNAME` and `API_PASSWORD` set:

```sh
go run ./apps/adapters/api/cmd/commander-api
```

In another terminal, query the catalog (`curl` prompts for the API password):

```sh
curl --user "$API_USERNAME" http://127.0.0.1:4274/api/v1/missions
```

| Catalog | List | Detail |
| --- | --- | --- |
| Missions | `GET /api/v1/missions` | `GET /api/v1/missions/{mission_name}` |
| Actions | `GET /api/v1/actions` | `GET /api/v1/actions/{action_name}` |
| Agents | `GET /api/v1/agents` | `GET /api/v1/agents/{agent_id}` |

Lists return `{"items":[...]}`, sorted by name or agent ID. Details return the
full record. Agent reads include current work and calculated lease freshness;
action readiness belongs to each agent, while the action catalog describes
available definitions. These endpoints read saved state without executing work.

## Operation queries

All operation routes require the same Basic authentication as the catalogs.

| Resource | Endpoint |
| --- | --- |
| Operations | `GET /api/v1/operations` |
| Operation | `GET /api/v1/operations/{operation_id}` |
| Execution graph | `GET /api/v1/operations/{operation_id}/graph` |
| Tasks | `GET /api/v1/operations/{operation_id}/tasks?cursor=&limit=100` |
| Task | `GET /api/v1/operations/{operation_id}/tasks/{task_id}` |
| Events | `GET /api/v1/operations/{operation_id}/events?cursor=&limit=100` |
| Questions | `GET /api/v1/operations/{operation_id}/questions` |
| Question | `GET /api/v1/operations/{operation_id}/questions/{question_id}` |
| Operation report | `GET /api/v1/operations/{operation_id}/report` |

The operation list is unpaginated and returns `{"items":[...]}` with full saved
records, newest first. Operation, task and question details return full records.
Reads never execute work or mutate saved state. Store-side list optimization is
deferred; Swagger documents exact ordering, validation and errors.

Task and event pages return `{"items":[...],"next_cursor":"..."}`. Task cursors
are exclusive task UUIDs in assignment order; event cursors are exclusive decimal
sequence strings in ascending order. Omit the cursor or use `""` to start.
`limit` defaults to 100, with a range of 1–1000. `next_cursor` identifies the last
returned item; an empty page preserves the request cursor, including initial `""`.

Question lists contain saved history in `items`, the newest pending question
that is not overdue in `last_question`, and the count of other eligible questions
in `remaining_questions`. The latter fields are `null` and `0` when none is
eligible or the operation is terminal. Reads never expire saved questions.

### Execution graph

The graph returns `operation_id`, `status`, `updated_at`, `nodes`, and `edges`.
Each node has a stable `id`, a `kind` (`task`, `artifact`, or `file`), and a matching
payload. Edges use `source`, `target`, and `kind` (`parent_child`, `produced`, or
`contains`). Task nodes include the action, status, assigned agent and timing;
file nodes include `artifact_id` and `id` for the existing file download route.

Poll every 2–5 seconds while work is active. Each request returns immediately.
After cancellation or failure, dispatched tasks can still finish; keep polling
until the operation is terminal and no task is queued, running or interrupted.
Node IDs remain stable across polls: `task:<id>`, `artifact:<id>`, and
`file:<artifact_id>:<file_id>`. Clients can key edges by kind/source/target.

The graph includes saved tasks only, with artifacts and files appearing when
their task result is saved. Proposed continuation actions are excluded. It reads
one operation snapshot without fetching file bytes or contacting agents. Use task
detail for full inputs/results and file download to retrieve and verify bytes.

## Report queries

`GET /api/v1/reports/{report_id}` returns the full saved report: `id`,
`operation_id`, `mission`, `output` and `created_at`. Output may be any JSON value,
including `null`. `GET /api/v1/operations/{operation_id}/report` reads a completed
operation's `report_id` and retrieves the same report through the stores without
contacting the controller. Both use the standard authentication, host checks,
request timeout and no-cache headers; reads never create or alter reports.

IDs must be canonical lowercase UUIDs; invalid IDs return `400 invalid_report_id`
or `400 invalid_operation_id`. Missing operations return `404 operation_not_found`.
Missing reports, absent report references and noncompleted operations return
`404 report_not_found`. Corrupt records or mismatched report operation/mission
return `500 internal_error`. Reports have no list or write endpoint.

## Artifact queries

| Resource | Endpoint |
| --- | --- |
| Metadata and file descriptors | `GET /api/v1/artifacts/{artifact_id}` |
| File download | `GET /api/v1/artifacts/{artifact_id}/files/{file_id}` |

These read existing stores without contacting the controller. Downloads return
raw bytes as an attachment with a sanitized filename and the stored media type
(invalid types use `application/octet-stream`). The file must belong to the
artifact and pass size/SHA-256 verification before any bytes are returned.
`HEAD` returns headers without the body. Files are read fully into memory;
range requests are not supported.

Both routes use the same authentication, timeout and security headers as other
queries. Invalid UUIDs return `400 invalid_artifact_id` or `invalid_file_id`;
missing metadata returns `404 artifact_not_found`. Unknown files or missing bytes
return `404 artifact_file_not_found`; corrupt metadata or file contents return
`500 internal_error`.

## Operation commands

Commands use the same authentication and origin checks as queries.

| Command | Endpoint | Body | Success |
| --- | --- | --- | --- |
| Start | `POST /api/v1/operations` | Required nonblank `mission_name` and `input` (any JSON value, including `null`); optional UUID `operation_id` | `201` full operation + `Location` |
| Assign task | `POST /api/v1/operations/{operation_id}/tasks` | Required nonblank `action_name`; optional object `input` (not `null`) and string `step_id` | `201` full task + `Location` |
| Cancel | `POST /api/v1/operations/{operation_id}/cancel` | Empty body or `{}` | `200` full operation |
| Complete | `POST /api/v1/operations/{operation_id}/complete` | Required `output` (any JSON value allowed by the mission output schema, including `null`) | `200` full operation with `report_id` |
| Fail | `POST /api/v1/operations/{operation_id}/fail` | Required nonblank string `reason` | `200` full operation |
| Ask question | `POST /api/v1/operations/{operation_id}/questions` | Required nonblank `text`; optional `options`, `timeout_seconds`, `step_id`, and linked `task_id` + `version` | `201` full question + `Location` |
| Answer question | `POST /api/v1/operations/{operation_id}/questions/{question_id}/answer` | Required nonblank `answer` | `200` full operation |
| Resolve continuation | `POST /api/v1/operations/{operation_id}/tasks/{task_id}/continuation/resolve` | Required positive `version` and explicit `actions` array | `200` full operation |

Nonempty bodies require `application/json` (charset parameters are accepted),
with a 1 MiB limit; unknown fields are rejected. Task assignment takes the
operation ID from the URL and does not accept `operation_id` or `agent_id` in its
body. The controller selects the agent; unknown actions may remain queued.
Invalid mission input or an unknown step returns `422 invalid_input`.
Cancellation stops queued work; already dispatched tasks may still finish.

Completion requires no queued or running tasks, pending questions, or unresolved
automatic/operator continuations; these return `409 operation_active_tasks`,
`409 question_pending`, or `409 operation_unresolved_work`. Pending model
continuations are resolved on completion. Invalid output returns
`422 invalid_output`. The report is saved before the operation is marked completed.
Failure cancels queued work, allows dispatched tasks to finish, and creates no
report. Both commands take a canonical lowercase UUID from the URL and reject
body `operation_id`; missing, finished, or conflicting operations return
`404 operation_not_found`, `409 operation_finished`, or `409 operation_conflict`.

Questions support at most four nonblank options. `timeout_seconds` accepts
1–86400; omission or `0` uses 120 seconds. To link a question to a pending task
continuation, supply its UUID `task_id` and current positive `version` together.
Answers may be free text; answered or expired questions return
`409 question_answered` or `409 question_expired`. An answer records guidance
and returns a linked continuation to model review; it does not queue selected
actions. Expiry also returns the decision to review without inventing an answer.

Continuation actions require nonblank `action_name`, with optional object
`input` (not `null`) and string `reason`; the controller selects agents and
`agent_id` is rejected. An explicit `actions: []` resolves without follow-up work.
Question and continuation commands reject body fields duplicating URL IDs.
A stale continuation version returns `409 continuation_conflict`; resolution can also return
`409 question_pending` or `409 task_limit_exceeded`.

Commands time out after 10 seconds without automatic retries. A timeout or
uncertain result can follow accepted work: query saved state before resubmitting.
Reusing a start ID returns `409 operation_conflict`; repeated assignments create
new tasks, and cancelling a finished operation returns `409 operation_finished`.
Swagger lists request schemas, response headers and error codes.

## Documentation and configuration

Set `API_SWAGGER_ENABLED=true` or pass `--swagger` to enable Swagger UI at
`/docs/` and the OpenAPI document at `/openapi.json`. Both require authentication.
Swagger assets are served locally; the UI and document are disabled by default.
An explicit `--swagger=false` overrides the environment.

Listener precedence: `--listen-address`, then `API_LISTEN_ADDRESS`, then
`0.0.0.0:$PORT`, then `127.0.0.1:4274`. Heroku supplies `PORT`; containers can
set `PORT=4274`. The same Dockerfile serves local and hosted deployments.
Container deployments must supply provider URLs and, when using filesystem
providers, writable mounts; the image does not prepare state directories.

Use `BUS_URL`, `DATABASE_URL`, and `STORAGE_URL` for providers; `LOG_LEVEL` and
`LOG_FORMAT` for logging. Their corresponding CLI flags override environment
values. Defaults use local `./state/` directories. Credentials are environment
only and must both be nonblank. Run with `--help` for options.

All other routes require HTTP Basic authentication, including unknown routes.
Use HTTPS at the reverse proxy for hosted deployments. Responses carry
no-cache, content-type, framing and referrer protections; HTTPS responses also
set HSTS. Browser requests must come from the same origin. Native clients may
omit `Origin`.

| Environment variable | CLI flag | Default |
| --- | --- | --- |
| `API_ALLOWED_HOSTS` | `--allowed-hosts` | `localhost,127.0.0.1,::1` |
| `API_TRUSTED_PROXIES` | `--trusted-proxies` | None |
| `API_PROXY_HEADERS` | `--proxy-headers` | `none` |

Allowed hosts are exact hostnames/IPs without ports or wildcards. Add your public
hostname when deploying. Trusted proxies are comma-separated IPs or CIDRs,
matched against the directly connected peer.

Select `forwarded` for `Forwarded`, `x-forwarded` for `X-Forwarded-Host` and
`X-Forwarded-Proto`, or `x-forwarded-proto` to preserve the original `Host`.
Forwarding requires a trusted proxy; configure it to overwrite the selected
headers. Each header must contain a single value; chains are rejected. Missing
headers use the direct request's host and scheme. Other forwarding headers are
ignored. The Dockerfile stays the same for each deployment.

## Development and testing

```sh
export COMPOSE_FILE=compose.test.yaml # or compose.full.test.yaml
docker compose up --build --wait
go test -race -count=1 -failfast -timeout=5m -v ./apps/adapters/api/...
docker compose down --volumes
```

The same HTTP scenarios run against FS or MongoDB/RabbitMQ/MinIO. Small local
host tests cover CLI configuration and security. See the
[shared test setup](../../../framework/tests/README.md#mcp-api-tests) for
credentials, isolation and running API and MCP together.

Only `ENV=tests` enables authenticated `POST /api/v1/testing/cleanup` (empty body
or `{}`). It returns `204` after clearing settled operations, reports and artifacts,
or `409 test_state_busy` while work is active. Catalogs, delivery records, bus and
host memory remain intact. This endpoint is absent from normal hosts and Swagger.

Keep concrete providers in `cli/factory.go` and inject their interfaces into
the adapter. Use plural names for endpoint groups, such as `endpoints/actions.go`.
Operation routes, schemas and OpenAPI definitions live in `endpoints/operations/`,
with `routes.go` registering its handlers. Shared HTTP helpers stay in `endpoints/`; `api.go`
wires routes and middleware, and root `openapi.go` assembles the document.
Workflow tests live in `tests/` and use the public HTTP endpoints.

HTTP models live in flat `models/*_model.go` files grouped by entity.
`mappings/*_mapping.go` maps requests and URL IDs to controller commands, and
domain records to response models, including nested operation state and results.
Handlers validate commands before sending them. Keep API models independent of
framework types and derive their Swagger schemas from the models.
