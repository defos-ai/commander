# Commander UI

## Overview

React, TypeScript and Vite frontend for Commander. It follows the platform's CSS
theme, Grunt typography, Lucide icons and typed i18next translations.

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

With Node 24+, run from this directory:

```sh
npm ci
npm run dev
```

Open `http://localhost:4275`. `npm run build` writes a standalone build to `dist/`;
`npm run preview` serves that build locally on the same port.

Start the [API adapter](../../adapters/api/README.md) with `API_USERNAME` and
`API_PASSWORD`. The UI looks for `http://localhost:4274` automatically. Choose
**Connect** when detected, or **Add connection** for another address. Enter a
name, API URL and credentials; the UI checks the connection before saving it.
For a UI hosted elsewhere, set the API's `API_ALLOWED_ORIGINS` to its origin.

Names and URLs are saved in this browser. Credentials stay in memory for the
current tab; reload requires signing in again. Connection settings let you edit
the address, forget credentials or remove the saved connection.

Saved connections refresh every 30 seconds. API liveness (`/healthz`), database
readiness (`/readyz`) and authenticated catalog access are checked separately,
with a five-second timeout. These checks do not assert controller, agent, broker
or file-storage health. Base-path URLs are supported; redirects are rejected.

## Development and testing

```sh
npm run typecheck
npm test
npm run build
```

Keep pages in `src/pages`, API reads and response types in `src/api`, shared
controls in `src/components`, state hooks in `src/hooks`, and CSS in `src/styles`.
English strings live in `src/i18n`; use typed translation selectors. Behavior tests
live in `tests/`. No runtime imports from sibling repositories are needed.

The COMMANDER wordmark uses the supplied Grunt Grotesk SemiBold, without a star.
Grunt Light and Geist Mono assets and theme colors match the platform frontend.
