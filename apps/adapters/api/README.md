# HTTP API adapter

Create missions, submit operation commands and read results from your application.

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

## Application catalog reads

Use this HTTP adapter for application-driven browsing; keep MCP for tools chosen
by a model. Configure the HTTP root (including `/api/v1`) and its Basic username
and password independently of the MCP endpoint and bearer token.

Read `GET /missions`, `/missions/{mission_name}`, `/operations`,
`/operations/{operation_id}` and `/operations/{operation_id}/report`. Lists return
an `items` array; details return one record. Encode a mission name as one path
segment, including any slashes or dot segments. Missing records return the typed
404 errors in OpenAPI; an upstream proxy's generic 404 is not a missing record.
These saved-state queries remain available without MCP or a live controller.

## Create missions

Save a mission from a JSON file:

```sh
curl --user "$API_USERNAME" \
  --header 'Content-Type: application/json' \
  --data-binary @mission.json \
  http://127.0.0.1:4274/api/v1/missions
```

Include `name`, an `objectives` array, `input_schema` and `output_schema`.
Add required/optional actions, starting actions, steps and rules as needed.
Use `action_definitions` for capabilities an agent should implement later;
see [mission definitions](../../../docs/abstractions.md#mission-operation-and-task).

Saving does not require installed agents or start work. Names are unique:
an existing name returns `409 mission_exists`. After an uncertain save, read
the mission by name before retrying. Keep secret values out of definitions.

## Operation commands

Follow the [operation workflow](../README.md#operation-workflow): start an
operation, inspect preflight, assign actions, resolve questions and decisions,
then complete it with output matching the mission schema.

**Read saved state before retrying a timed-out command.** Accepted work may
continue; repeating an assignment creates another task. Cancellation stops queued
work, while dispatched tasks may finish.

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
`/api/v1/secrets` routes in Swagger. Scope each value by agent type and name.
Values are write-only; responses contain metadata.

Without a key, secret routes return `503 secrets_unavailable`; other API features
remain available. After saving, wait for the agent's requirement to clear.
For conflicts or uncertain writes, refresh metadata before retrying.

## Documentation and configuration

Set `BUS_URL`, `DATABASE_URL` and `STORAGE_URL` to choose providers; defaults use
local `./state/` directories. Use writable mounts with filesystem providers in
containers. Run `--help` for flags, which override environment settings.

### Authentication and health

Standalone API requires `API_USERNAME` and `API_PASSWORD`. Compose generates a
password; retrieve it with:

```sh
docker compose exec api commander-api credentials
```

Retrieve it again after an API restart, or set `API_PASSWORD` before launch to
keep it stable. Add `--raw` for the password only. All-in-one Playground exposes
its session login through `/credentials`.

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
