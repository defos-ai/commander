# Commander UI

Connect to Commander APIs, run operations and review their results in a browser.
The UI shows missions, controllers, agents and actions, including readiness and
health. Operations support search, status filters, questions, event history and
an execution graph of recorded tasks, artifacts and files.

Start an operation with a mission and JSON input; Commander plans the first task.
Answer pending questions with an option or your own text. Cancelling stops new
work, but dispatched tasks may finish. After a command timeout, inspect the
operation before retrying; the UI never retries commands automatically.

Operation details refresh every 10 seconds. Select graph nodes to inspect work,
or drag and zoom to explore. Artifact details offer image/text previews (up to
2 MiB) and downloads (up to 100 MiB); completed reports download as JSON.

## Quick start

From the repository root:

```sh
docker compose --profile admin up --build -d
```

Open `http://localhost:4275` and choose **Connect** under **Local Commander detected**.
Compose defaults are `http://localhost:4274`, username `commander` and password
`commander-local-secret`; override credentials with `API_USERNAME` and
`API_PASSWORD`. Use `docker compose -f compose.full.yaml --profile admin up --build -d`
for MongoDB, RabbitMQ and MinIO.

`COMMANDER_UI_PORT` and `COMMANDER_API_PORT` change the published ports. Discovery
checks only `http://localhost:4274`; use **Add connection** for another port or
API, then enter a name, URL and credentials. The UI checks before saving.

### Native server

With Node 24+ and Go 1.26.7+, run from `apps/admin/ui`:

```sh
npm --prefix frontend ci
npm --prefix frontend run build
go run ./backend/cmd/commander-ui
```

Start the [API adapter](../../adapters/api/README.md) separately. The UI serves
`frontend/dist/` on `:4275`; override with `--static-dir` (`UI_STATIC_DIR`) or
`--listen-address` (`UI_LISTEN_ADDRESS`). Listener precedence is flag, environment,
`PORT`, then `4275`. `/healthz` checks the UI server. The
[Dockerfile](docker/Dockerfile) packages the frontend and server in one image.

### Connections and sessions

Browser API requests use the UI backend on the same origin. Compose displays and
saves `http://localhost:4274` while routing it internally to `http://api:4274`.
Optional `UI_API_UPSTREAMS` (`--api-upstreams`) maps public hosts and ports:

```sh
UI_API_UPSTREAMS=localhost:4274=api:4274,127.0.0.1:4274=api:4274
```

Only the matching host and port change; scheme, path and query remain intact.
Unmapped URLs connect directly. Compose supplies mappings automatically; adding
other APIs needs no routing changes or restart. A hosted UI cannot discover an
API on a visitor's laptop through `localhost`. Base paths are supported;
redirects are rejected.

Each API must allow its upstream hostname in `API_ALLOWED_HOSTS`. Browser CORS
configuration is unnecessary because the backend sends Basic authentication.

Names and URLs are saved in the browser; API passwords stay in backend memory.
The browser receives an HttpOnly, SameSite=Strict cookie. Sessions survive reloads
for up to eight hours. Forgetting credentials or removing a connection signs out
only that API; restarting the backend signs out all sessions. Use one UI backend
replica. Record URLs support reload and back/forward navigation, contain no
credentials, and require the connection to be saved in that browser.

For hosting, use HTTPS and set `UI_PUBLIC_ORIGIN` (`--public-origin`), for example
`https://admin.example.com`, including when TLS terminates at a reverse proxy.
Forwarded headers are not trusted. HTTPS sessions use Secure cookies; plain HTTP
is supported only for localhost development.

Saved connections refresh every 30 seconds. Liveness (`/healthz`), database
readiness (`/readyz`) and authenticated catalog access are checked separately with
a five-second timeout. They do not establish controller, agent, broker or file
storage health.

## Development and testing

[Contributor guidance](AGENTS.md) ·
[Frontend scripts](frontend/package.json) ·
[Test setup](../../../framework/tests/README.md)
