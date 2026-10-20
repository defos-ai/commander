# HTTP API adapter

The API exposes Commander catalogs, saved operations, reports and artifacts over
HTTP, and sends operation commands to the controller.

## Quick start

From the repository root:

```sh
export API_USERNAME=commander
export API_PASSWORD="$(openssl rand -hex 32)"
go run ./apps/adapters/api/cmd/commander-api
```

Query the catalog (`curl` prompts for the API password):

```sh
curl --user "$API_USERNAME" http://127.0.0.1:4274/api/v1/missions
```

Start the [controller](../../controllers/README.md#quick-start) and agents with
matching provider settings to execute work. See [deployment](../../../docs/deployment.md)
for provider settings and Compose.

## API reference

Enable `API_SWAGGER_ENABLED=true` or pass `--swagger` for authenticated Swagger UI
at `/docs/` and the OpenAPI document at `/openapi.json`. Both are disabled by
default; `--swagger=false` overrides the environment. Swagger assets are local.
The reference includes every route, request schema, response and error.

Catalog and operation queries read saved state without executing work. Tasks and
events use `cursor` and `limit` pagination (default 100, maximum 1,000); pass
`next_cursor` unchanged to the next request for the same resource. Execution
graphs show recorded tasks and their artifacts and files. Poll active graphs
every 2–5 seconds; dispatched tasks can still finish after cancellation or failure.

Operations with `same-agent` include a read-only `affinity` view: `for_actions`,
the bound `agent_id` after first dispatch, or `error` for inconsistent history.
The binding survives completion and an agent going offline. Queued tasks remain
unassigned until dispatch; preflight describes saved readiness, not capacity.

Reports are available after completion. Artifact downloads verify file size and
SHA-256 before returning bytes; `HEAD` returns headers only. Files are read fully
into memory, and range requests are unsupported.

## Operation commands

Start an operation from a mission, assign actions, resolve continuations and
questions, then complete it with output matching the mission schema. The
controller selects agents. See the [operation workflow](../README.md#operation-workflow)
and [decision rules](../../../docs/abstractions.md#next-actions-and-continuations).

Nonempty command bodies require `application/json`, have a 1 MiB limit, and
reject unknown fields. Commands time out after 10 seconds without automatic
retries. **Query saved state before retrying an uncertain result:** accepted work
may continue, and repeated task assignments create new tasks.

Cancellation and failure stop queued work; dispatched tasks may still finish.
Completion requires settled tasks, questions and automatic/operator continuations.
Finished operations are read-only; further work needs a new operation.

## Secrets

Compose configures a persistent encryption key automatically; Playground
all-in-one generates a temporary session key. See [deployment](../../../docs/deployment.md#secret-encryption).

For a standalone API, set `SECRETS_ENCRYPTION_KEY` to a randomly generated 32-byte
key encoded as 64 hex characters. Generate it once (`openssl rand -hex 32`),
keep it separate from the database, and reuse it across restarts and trusted
encrypting/decrypting hosts.
Without it, the API runs normally and secret routes return `503 secrets_unavailable`.
A malformed key prevents startup. Changing the key does not re-encrypt old records.

When embedding the API, pass a cipher created with `crypto.NewSecretCipher` through
`cli.Options.SecretCipher` to `cli.RunWithOptions`. It takes precedence over the
environment variable.

All secret routes require API authentication. Values are encrypted in the selected
FS or Mongo database; responses contain metadata and availability only. Each
secret belongs to an agent type and is identified by its name.

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/secrets?agent_type=builder-agent` | List one agent type's secrets. |
| `POST` | `/api/v1/secrets` | Create with `agent_type`, `name`, `value`, optional `expires_at`. |
| `GET` | `/api/v1/secrets/{agent_type}/{name}` | Read metadata. |
| `PUT` | Same detail route | Replace with current `id`, `version`, new `value`, optional `expires_at`. |
| `DELETE` | Same detail route with `?id=…&version=…` | Delete the exact version. |
| `GET` | `/api/v1/secrets/requests` | List pending requests; optionally filter by `agent_type` and/or `agent_id`. |

Pending requests show `needs_value`, `awaiting_delivery` or `awaiting_installation`.
The last means a delivery is reserved, not confirmed installed. Expired requests
are omitted. This view tracks requests, not agent readiness; lease refreshes can
overlap for the same agent and secret.

`value` is a required string, including when intentionally empty. Omitting
`expires_at` on replacement removes expiration. Stale writes return
`409 secret_conflict`; query metadata after `503 secret_write_uncertain` before
retrying. Configured agents receive declared secrets through controller
reconciliation. Saving confirms storage, not installation; see
[delivery and refresh](../../../framework/secrets/README.md#agent-requirements).
See [key lifecycle](../../../framework/crypto/README.md#key-lifecycle) for key retention.

## Documentation and configuration

Listener precedence is `--listen-address`, `API_LISTEN_ADDRESS`, `0.0.0.0:$PORT`,
then `127.0.0.1:4274`. Containers can set `PORT=4274`.

Set `BUS_URL`, `DATABASE_URL` and `STORAGE_URL` for providers, and `LOG_LEVEL` and
`LOG_FORMAT` for logging. Corresponding CLI flags override environment values;
run with `--help` for options. Defaults use local `./state/` directories. Container
deployments need provider URLs and writable mounts for filesystem providers;
the image does not prepare state directories.

### Authentication and health

The standalone API requires nonblank `API_USERNAME` and `API_PASSWORD` environment
variables before serving requests. Startup fails with copyable setup instructions
when either is missing. Compose supplies username `commander` and generates a
random password unless you set `API_PASSWORD`. Retrieve the running credentials:

```sh
docker compose exec api commander-api credentials
```

Generated passwords change whenever the container restarts. Set `API_PASSWORD`
before starting Compose for a stable password; `API_USERNAME` overrides the username.
Native hosts can run `commander-api credentials` with the same environment settings.
Add `--raw` to print only the password.
Passwords are never included in startup logs or errors.

[Playground's all-in-one mode](../../playground/README.md#native-one-executable)
generates a session password automatically; use `/credentials` there to view it.

Use HTTPS at the reverse proxy for hosted deployments. All routes, including
unknown routes and Swagger, require Basic authentication except approved CORS
preflights, `/aboutz`, and public `GET`/`HEAD` probes on every host:

- `/healthz`: server liveness.
- `/readyz`: metadata database readability; does not check broker connectivity,
  file storage or controller availability.

Public `GET`/`HEAD /aboutz` describes the deployment: `runtime` is `native`,
`docker` or `docker-compose`; `application` is `commander-api` or `commander`
(all-in-one). Host, Origin and trusted-proxy checks still apply. It returns no
credentials. Set `API_RUNTIME` or `--runtime` when deploying; the default is
`native`, and the Docker images and Compose set it automatically.

Responses include no-cache, content-type, framing and referrer protections;
HTTPS responses also set HSTS.

### Hosts, origins and proxies

| Environment variable | CLI flag | Default |
| --- | --- | --- |
| `API_ALLOWED_HOSTS` | `--allowed-hosts` | `localhost,127.0.0.1,::1` |
| `API_ALLOWED_ORIGINS` | `--allowed-origins` | `http://localhost:4275,http://127.0.0.1:4275` |
| `API_TRUSTED_PROXIES` | `--trusted-proxies` | None |
| `API_PROXY_HEADERS` | `--proxy-headers` | `none` |

Allowed hosts are exact hostnames/IPs without ports or wildcards; add your public
hostname when deploying. Trusted proxies are comma-separated IPs or CIDRs matched
against the directly connected peer. Flags override environment values.

Browser requests can use the same origin or an allowed origin. Setting allowed
origins replaces the defaults; `--allowed-origins=` restores same-origin-only
access. Use scheme and port without a path or trailing slash; wildcards, `null`,
credentials, queries and fragments are rejected. Origins never bypass host or
trusted-proxy checks. Native clients may omit `Origin`.

The [admin UI](../../admin/ui/README.md#connections-and-sessions) sends Basic
authentication from its backend and needs no API CORS permission; the upstream
hostname must still pass `API_ALLOWED_HOSTS`. Other browser clients should send
an explicit Basic `Authorization` header with `credentials: 'omit'`; cross-origin
cookies and browser-managed credentials are disabled. CORS permits `GET`, `HEAD`,
`POST`, `PUT`, `DELETE` and the `Authorization`, `Content-Type`, `Accept` headers. Approved
preflights return `204`; actual requests require authentication. Approved origins
can read `Location`, `Content-Disposition` and `WWW-Authenticate` headers.

Proxy modes are `forwarded` (`Forwarded`), `x-forwarded` (`X-Forwarded-Host` and
`X-Forwarded-Proto`), or `x-forwarded-proto` (preserves the original `Host`).
Forwarding requires a trusted proxy configured to overwrite the selected headers.
Each header must have one value; chains are rejected. Missing headers use the
direct request's host and scheme; other forwarding headers are ignored.

## Development and testing

[Contributor guidance](AGENTS.md) ·
[Test setup](../../../framework/tests/README.md#mcp-api-tests) ·
[OpenAPI source](openapi.go)
