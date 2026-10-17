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

## Secrets

Open **Agents → Manage secrets** to supply values requested by agents, or add
secrets for an agent type. You can replace or delete saved secrets and set an
optional expiration.
Operations waiting for declared secrets show **Manage secrets** in their details
and execution graph. Work resumes when all requirements are ready.
Values are write-only and are never saved in browser storage.

Secret management requires `SECRETS_ENCRYPTION_KEY` on the API and participating
agents; see [setup and encryption](../../adapters/api/README.md#secrets).
Saving confirms storage; delivery and agent installation happen asynchronously.
After an uncertain write, refresh metadata before trying again.

## Quick start

From the repository root:

```sh
docker compose --profile admin up --build -d
```

Open `http://localhost:4275` and choose **Connect** under **Local Commander detected**.
The API URL and username (`commander`) are prefilled. Compose generates a random
password. The dialog's **Need a password?** hint offers the command to copy:

```sh
docker compose exec api commander-api credentials
```

Generated passwords change when the API container restarts;
retrieve the new password and reconnect. For a stable password, set `API_PASSWORD`
before starting Compose. `API_USERNAME` overrides the username; see
[API authentication](../../adapters/api/README.md#authentication-and-health).

`COMMANDER_UI_PORT` and `COMMANDER_API_PORT` change the published ports. Discovery
checks only `http://localhost:4274`; use **Add connection** for another port or
API, then enter a name, URL and credentials. The UI checks before saving.

### All-in-one

[Playground's all-in-one image](../../playground/README.md#docker-recommended)
includes Admin. Publish `127.0.0.1:4275:4275`; Playground defaults its link to
`http://localhost:4275`. For another published port or host, pass `--admin-url`
with the browser address after the image name. Type `/admin` in Playground for a
link with the local API URL and username prefilled. Existing saved connections
are reused; enter the password to sign in. The API port need not be published.
Use `/credentials` in Playground or `docker exec commander commander credentials api`
for the API login.

Native `--all-in-one` also includes Admin; `/admin` uses its listening address
automatically. Each completed operation in Playground has a review link that
opens its details after sign-in. Remote links ask you to choose the API connection.

### Native server

With Node 24+ and Go 1.26.7+, run from `apps/admin/ui`:

```sh
npm --prefix frontend ci
npm --prefix frontend run build
go run ./backend/cmd/commander-ui
```

Start the [API adapter](../../adapters/api/README.md) separately. Source builds serve
`frontend/dist/`; packaged builds include the frontend in the Go binary. Override
the files with `--static-dir` (`UI_STATIC_DIR`). Set the address with
`--listen-address` (`UI_LISTEN_ADDRESS`); precedence is flag, environment, `PORT`,
then `4275`. `/healthz` checks the UI server. The
[Dockerfile](docker/Dockerfile) builds a self-contained server.

To build a portable Admin binary after installing frontend dependencies:

```sh
npm --prefix frontend run build -- --outDir ../backend/static/dist --emptyOutDir
go build -tags embed_ui -o bin/commander-ui ./backend/cmd/commander-ui
```

Run `./bin/commander-ui`, or copy the binary anywhere. On Windows, build with
`-o bin/commander-ui.exe` and run `.\bin\commander-ui.exe`.

### Connections and sessions

Browser API requests use the UI backend on the same origin. Compose displays and
saves `http://localhost:4274` while routing it internally to `http://api:4274`.
Optional `UI_API_UPSTREAMS` (`--api-upstreams`) maps public hosts and ports:

```sh
UI_API_UPSTREAMS=localhost:4274=api:4274,127.0.0.1:4274=api:4274
```

Only the matching host and port change; scheme, path and query remain intact.
Public APIs connect directly. Internal APIs require an explicit mapping; Compose
supplies its own automatically. For another internal API, map its host and port
to itself, for example `commander.internal:4274=commander.internal:4274`.
Native local discovery allows loopback on port `4274`. Other local ports need a
mapping. A hosted UI cannot discover an API on a visitor's laptop through
`localhost`. Base paths are supported; redirects, cloud metadata addresses and
environment HTTP proxies are blocked.

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
