# HTTP API adapter

Integrate Commander's control plane with internal applications and operational
tooling. Use authenticated HTTP endpoints to manage missions, submit work, record
operator decisions and review saved results, execution logs and artifacts.

## Quick start

From the repository root, with Go 1.26.7+:

```sh
export API_USERNAME=commander
export API_PASSWORD="$(openssl rand -hex 32)"
go run ./apps/adapters/api/cmd/commander-api
```

List missions; `curl` prompts for the password:

```sh
curl --user "$API_USERNAME" http://127.0.0.1:4274/api/v1/missions
```

Start the [controller](../../controllers/README.md#quick-start) and agents with
matching provider settings to execute work. For a complete stack, use
[Compose](../../../docs/deployment.md).

## API reference

Set `API_SWAGGER_ENABLED=true` or pass `--swagger` to enable authenticated
Swagger at `/docs/` and OpenAPI at `/openapi.json`. Use that reference for
routes, schemas, pagination and errors.

## Create and update missions

Save a mission from a JSON file:

```sh
curl --user "$API_USERNAME" \
  --header 'Content-Type: application/json' \
  --data-binary @mission.json \
  http://127.0.0.1:4274/api/v1/missions
```

Include `name`, an `objectives` array, `input_schema` and `output_schema`.
Add required/optional actions, starting actions and rules as needed.
Use `action_definitions` for capabilities an agent should implement later;
see [mission definitions](../../../docs/abstractions.md#mission-operation-and-task).

Saving does not require installed agents or start work. Names are unique:
an existing name returns `409 mission_exists`. After an uncertain save, read
the mission by name before retrying. Keep secret values out of definitions.

To edit an existing mission, send its complete definition with
`PUT /api/v1/missions/{mission_name}`. Encode the name as one path segment and keep
the JSON `name` unchanged. Objectives, action lists/contracts, rules and schemas
can change; existing operations keep their original mission snapshot. Read the
saved definition after an uncertain update before retrying.

Optional `completion.decision_by` accepts `model` or `operator` and records the
mission-wide policy in future operation snapshots. `operator` requires completion
through an operator channel, such as the authenticated HTTP API, with no model
fallback. Omission or `model` also allows MCP completion.

## Operation commands

Follow the [operation workflow](../README.md#operation-workflow): start an
operation, inspect preflight, assign actions, resolve questions and decisions,
then complete it with output matching the mission schema.

`POST /api/v1/operations/{operation_id}/complete` accepts `{"output": ...}` and
records `completed_by: operator`. The adapter assigns this origin; do not send it
in the body. It identifies the submission channel, not a verified human identity.

For operator-controlled missions, MCP `complete_operation` saves `completion_draft`
for review without creating a final report. Review or edit it in Admin and choose
**Complete operation**, which submits through this same HTTP completion route.

**Read saved state before retrying a timed-out command.** Accepted work may
continue; repeating an assignment creates another task. Cancellation stops queued
work, while dispatched tasks may finish.

## Execution logs

Read saved progress without a running controller:

```sh
curl --user "$API_USERNAME" \
  "http://127.0.0.1:4274/api/v1/operations/$OPERATION_ID/logs?order=desc&limit=100"
```

Optional `task_id`, `agent_id` and `action_name` filters match exactly and
intersect; the task must belong to this operation. `order=asc` (default) reads
oldest first; `order=desc` reads newest first. Pass `next_cursor` as `cursor`
with the same filters and order to continue that direction. `limit` defaults
to 100 and accepts 1–1000.

To follow live progress after a descending page, save its first/newest log ID
and request `order=asc&cursor=<that-id>` with the same filters. Ascending pages
advance that cursor to their last returned ID. Empty pages keep the input cursor.
Ordering follows durable appends, independently of source timestamps. Logs can
arrive after work ends.

## Secrets

Compose and all-in-one configure encryption automatically. For a standalone API,
set `SECRETS_ENCRYPTION_KEY` to a 32-byte key encoded as 64 hex characters.
Generate it once:

```sh
openssl rand -hex 32
```

Retain the key securely, separately from the database, and reuse it across
restarts and trusted hosts that encrypt/decrypt secrets. Replacing the key makes
existing encrypted values unreadable.

Manage secrets through [Admin](../../admin/ui/README.md#secrets) or the authenticated
`/api/v1/secrets` routes in Swagger. Each name identifies one shared value across
agents. Detail routes use `/api/v1/secrets/{name}`; `/api/v1/secret-requests` shows
waiting recipients. Values are write-only; responses contain metadata.

Without a key, secret routes return `503 secrets_unavailable`; other API features
remain available. After saving, wait for the agent's requirement to clear.
For conflicts or uncertain writes, refresh metadata before retrying.

## Documentation and configuration

Set `BUS_URL`, `DATABASE_URL` and `STORAGE_URL` to choose providers; defaults use
local `./state/` directories. Use writable mounts with filesystem providers in
containers. Run `--help` for flags, which override environment settings.

### Authentication and health

Standalone API requires `API_USERNAME` and `API_PASSWORD`. Compose generates and
saves a password, retaining it across restarts. Retrieve its login with:

```sh
docker compose exec api commander-api credentials
```

Add `--raw` for the password only. Set `API_PASSWORD` to supply your own; follow
[deployment](../../../docs/deployment.md#local-compose) to apply a rotation.
All-in-one Playground exposes its session login through `/credentials`.

Use HTTPS for hosted deployments. Authentication is required except for public
deployment metadata (`/aboutz`), health probes and approved CORS preflights.

- `/healthz`: check whether the API server is running.
- `/readyz`: check metadata database readability; inspect controllers and agents
  separately before running work.

### Hosts, origins and proxies

| Configure… | Environment variable |
| --- | --- |
| Listener (default `127.0.0.1:4274`) | `API_LISTEN_ADDRESS` |
| Accepted hostnames; add your public and internal upstream names | `API_ALLOWED_HOSTS` |
| Other browser clients; use exact origins such as `https://app.example.com` | `API_ALLOWED_ORIGINS` |
| Reverse proxy IPs or CIDRs | `API_TRUSTED_PROXIES` |
| Trusted headers: `forwarded`, `x-forwarded` or `x-forwarded-proto` | `API_PROXY_HEADERS` |

Configure proxies to overwrite the selected forwarding headers. Admin sends
credentials from its backend, so it needs no API CORS permission; its upstream
hostname must still be allowed.

## Development and testing

[Contributor guidance](AGENTS.md) ·
[Test setup](../../../framework/tests/README.md#mcp-api-tests) ·
[OpenAPI source](openapi.go)

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.
