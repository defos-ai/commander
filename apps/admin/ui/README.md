# Commander UI

## Overview

React, TypeScript and Vite frontend with a small Go HTTP server. It follows the
platform's CSS theme, Grunt typography, Lucide icons and typed i18next translations.

- `frontend/`: React application, browser tests and Vite build.
- `backend/`: Go server, API proxy and browser sessions; standard library only.
- `docker/`: builds both into one reusable image.

Add Commander APIs, sign in with Basic Auth, and monitor their health. The home
page keeps a grid of saved connections. Inside a connection, the sidebar provides
Overview, Operations, Missions, Agents, Actions and connection settings.

Operations supports search, status filters and details: task progress,
agent readiness, decisions, questions, results, artifact files and event history.
Snapshots refresh every 10 seconds while open. Task completion is shown separately
from operation status. Start an operation by selecting a mission and supplying
its JSON input; Commander then plans the first task. You can cancel active
operations and answer pending questions using an option or your own text.

Commands go through the API to the controller. Cancellation stops new work;
already-dispatched work may finish. A timeout leaves the outcome uncertain, so
the UI offers to inspect the operation and never retries a command automatically.

Switch operation details to **Execution graph** to follow recorded tasks and their
artifacts and files. Select a node to inspect its status and related work in a popup; zoom
and drag to explore larger graphs. Mouse dragging pans the graph without opening
node details; clicking a node opens them. Touch scrolling and keyboard navigation
remain available. It refreshes every 10 seconds and shows
recorded work, including queued tasks, rather than a plan of future work.
Task node details include collapsible input and output, updated with the operation.
Open artifacts from details or graph nodes to inspect metadata, preview images
and text, or download files. Previews are limited to 2 MiB and downloads to
100 MiB. Reports open from operation details and can be downloaded as JSON.
File content is fetched on demand through the UI backend.

Missions supports search and read-only details: objectives, starting and required
actions, automations, planning steps, and input/output schemas. The automation
graph connects allowed follow-ups and shows model review and continuation rules.
Drag, zoom, or select a node to inspect its rule. Refresh loads the current
definitions from the selected API.

Agents shows current work, host details, heartbeat freshness and each agent's
action readiness and missing requirements. Actions shows catalog definitions,
permissions and schemas; readiness belongs to individual agents. Both pages
support search, details and a 30-second refresh while open. These pages are
read-only; work is assigned through operations.

## Quick start

From the repository root:

```sh
docker compose --profile admin up --build -d
```

Open `http://localhost:4275` and choose **Connect** under **Local Commander detected**.
The form prefills `Local connection`, `http://localhost:4274` and username
`commander`; enter your password to connect. All fields are editable.
Local Compose credentials are `commander` / `commander-local-secret`; override
them with `API_USERNAME` and `API_PASSWORD`. Use `-f compose.full.yaml` for
MongoDB, RabbitMQ and MinIO. `COMMANDER_UI_PORT` and `COMMANDER_API_PORT` change
the published ports. Discovery checks only `http://localhost:4274`; for another
API port, choose **Add connection**. This opens an empty form for the connection
name, API URL, username and password.

For frontend development, with Node 24+, prepare the assets and start the Go
backend from this directory:

```sh
npm --prefix frontend ci
npm --prefix frontend run build
go run ./backend/cmd/commander-ui --listen-address 127.0.0.1:4276
```

In another terminal, run from this directory:

```sh
cd frontend
npm run dev
```

Open `http://localhost:4275`. To build and serve with Go, run from `apps/admin/ui`:

```sh
npm --prefix frontend run build
go run ./backend/cmd/commander-ui
```

The server reads `frontend/dist/` and listens on `:4275`. Override with
`--static-dir` (`UI_STATIC_DIR`) or `--listen-address` (`UI_LISTEN_ADDRESS`).
Hosting platforms can set `PORT`; explicit flags take precedence.

Start the [API adapter](../../adapters/api/README.md) with `API_USERNAME` and
`API_PASSWORD`. The UI looks for `http://localhost:4274` automatically. Choose
**Connect** when detected, or **Add connection** for another address. Enter a
name, API URL and credentials; the UI checks the connection before saving it.
The Vite development server forwards `/ui/` to the Go backend on port `4276`.

### Connections and sessions

Add API URLs and credentials directly in the UI. All browser API requests go
through the UI backend on the same origin. Both native and Compose setups show
and save `http://localhost:4274`. Compose routes that address to `http://api:4274`
inside the backend for discovery, sign-in and API requests.

Optional `UI_API_UPSTREAMS` (`--api-upstreams`) maps public hosts and ports
to internal ones:

```sh
UI_API_UPSTREAMS=localhost:4274=api:4274,127.0.0.1:4274=api:4274
```

A matching host and port are replaced; the URL's scheme, path and query stay intact.
Unmapped URLs connect directly. Compose sets the mappings automatically.
Adding other APIs through the UI needs no routing configuration or restart.
A remotely hosted UI cannot discover an API on the visitor's laptop through `localhost`.

Each API must allow the upstream hostname in `API_ALLOWED_HOSTS`; browser CORS
configuration is unnecessary for this proxy.

Names and URLs are saved in the browser. API passwords stay in backend memory;
the browser receives an HttpOnly, SameSite=Strict session cookie. Sign-in survives
page reloads for up to eight hours. Each connection has separate credentials;
forgetting credentials or removing a connection signs out only that API.
Restarting the UI backend signs out every session. This initial implementation
uses one UI backend replica.

For hosting, serve the UI over HTTPS and set `UI_PUBLIC_ORIGIN`
(`--public-origin`), for example `https://admin.example.com`. HTTPS sessions use
Secure cookies. Plain HTTP is supported only for localhost development. Set this
origin when TLS terminates at a reverse proxy; forwarded headers are not trusted.

URLs preserve the connection, page and selected record, including graph views.
Back/forward and reload return to that location. Links require the connection to
be saved in the browser and contain no API credentials.

Saved connections refresh every 30 seconds. API liveness (`/healthz`), database
readiness (`/readyz`) and authenticated catalog access are checked separately,
with a five-second timeout. These checks do not assert controller, agent, broker
or file-storage health. Base-path URLs are supported; redirects are rejected.

## Development and testing

From `apps/admin/ui/frontend`:

```sh
npm run typecheck
npm test
npm run build
```

Test the Go server from the repository root:

```sh
go test -race ./apps/admin/ui/backend/...
```

Browser tests use the real API, controller and agents in the isolated test stack.
From the repository root:

```sh
docker compose -f compose.test.yaml --profile ui up --build --wait
cd apps/admin/ui/frontend
npm ci
npx playwright install chromium
npm run test:e2e
cd ../../../..
docker compose -f compose.test.yaml --profile ui down --volumes
```

The browser scenarios cover navigation, session persistence and operation questions, automation,
graphs and evidence downloads. They clean test data through the test-only API;
run them separately from API/MCP tests using the same stack. CI also installs
browser system dependencies and retains traces/screenshots when tests fail.

The [Dockerfile](docker/Dockerfile) builds the frontend and Go server, then runs
as a non-root user on port `4275` (override with `PORT`). `/healthz` checks the
server. Enter API addresses and credentials through the UI, so the same image
works with different Commander deployments.

Within `frontend/`, keep pages in `src/pages`, API reads and response types in
`src/api`, shared controls in `src/components`, state hooks in `src/hooks`, and CSS
in `src/styles`. English strings live in `src/i18n`; use typed translation
selectors. Tests live in `frontend/tests/` and `backend/tests/`. No runtime imports
from sibling repositories are needed.

The COMMANDER wordmark uses the supplied Grunt Grotesk SemiBold, without a star.
Grunt Light and Geist Mono assets and theme colors match the platform frontend.
