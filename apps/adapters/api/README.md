# HTTP API adapter

## Overview

The API adapter lets HTTP clients inspect Commander missions, actions, agents
and saved operation state through domain stores. Command endpoints are planned;
commands will go to the controller over the bus.

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
| Tasks | `GET /api/v1/operations/{operation_id}/tasks?cursor=&limit=100` |
| Task | `GET /api/v1/operations/{operation_id}/tasks/{task_id}` |
| Events | `GET /api/v1/operations/{operation_id}/events?cursor=&limit=100` |
| Questions | `GET /api/v1/operations/{operation_id}/questions` |
| Question | `GET /api/v1/operations/{operation_id}/questions/{question_id}` |

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
go test -race -count=1 ./apps/adapters/api/...
go vet ./apps/adapters/api/...
```

Keep concrete providers in `cli/factory.go` and inject their interfaces into
the adapter. Keep resource routes in `endpoints/`. Operation routes, schemas
and OpenAPI definitions live together in `endpoints/operation/`, with `routes.go`
registering its handlers. Shared HTTP helpers stay in `endpoints/`; `api.go`
wires routes and middleware, and root `openapi.go` assembles the document.
Boundary tests live in `tests/` and use temporary filesystem state.
