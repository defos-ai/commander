# Commander UI

## Overview

React, TypeScript and Vite frontend with a small Go HTTP server. It follows the
platform's CSS theme, Grunt typography, Lucide icons and typed i18next translations.

- `frontend/`: React application, browser tests and Vite build.
- `backend/`: Go static server and health check; standard library only.
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
artifacts and files. Select a node to inspect its status and related work; zoom
and scroll to explore larger graphs. It refreshes every 10 seconds and shows
recorded work, including queued tasks, rather than a plan of future work.
Open artifacts from details or graph nodes to inspect metadata, preview images
and text, or download files. Previews are limited to 2 MiB and downloads to
100 MiB. Reports open from operation details and can be downloaded as JSON.
File content is fetched on demand using the connection's credentials.

Missions supports search and read-only details: objectives, starting and required
actions, automations, planning steps, and input/output schemas. Refresh loads the
current definitions from the selected API.

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

Open `http://localhost:4275` and connect to `http://localhost:4274`.
Local Compose credentials are `commander` / `commander-local-secret`; override
them with `API_USERNAME` and `API_PASSWORD`. Use `-f compose.full.yaml` for
MongoDB, RabbitMQ and MinIO. `COMMANDER_UI_PORT` and `COMMANDER_API_PORT` change
the published ports; enter the corresponding API URL when connecting.

For frontend development, with Node 24+, run from this directory:

```sh
cd frontend
npm ci
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
For a UI hosted elsewhere, set the API's `API_ALLOWED_ORIGINS` to its origin.

Names and URLs are saved in this browser. Credentials stay in memory for the
current tab; reload requires signing in again. Connection settings let you edit
the address, forget credentials or remove the saved connection. URLs preserve
the connection, selected page and record, including an operation's graph view.
Back/forward and reload return to that location; a reloaded tab asks you to sign
in before loading protected data. Links work in browsers with that connection
saved and contain no API credentials.

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

The two scenarios cover navigation/sign-in and operation questions, automation,
graphs and evidence downloads. They clean test data through the test-only API;
run them separately from API/MCP tests using the same stack. CI also installs
browser system dependencies and retains traces/screenshots when tests fail.

The [Dockerfile](docker/Dockerfile) builds the frontend and Go server, then runs
as a non-root user on port `4275` (override with `PORT`). `/healthz` checks the
server. API addresses and credentials are supplied through the UI, so the same
image works with different Commander deployments.

Within `frontend/`, keep pages in `src/pages`, API reads and response types in
`src/api`, shared controls in `src/components`, state hooks in `src/hooks`, and CSS
in `src/styles`. English strings live in `src/i18n`; use typed translation
selectors. Tests live in `frontend/tests/` and `backend/tests/`. No runtime imports
from sibling repositories are needed.

The COMMANDER wordmark uses the supplied Grunt Grotesk SemiBold, without a star.
Grunt Light and Geist Mono assets and theme colors match the platform frontend.
