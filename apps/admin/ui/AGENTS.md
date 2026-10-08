# Working on the admin UI

## Code map

- `frontend/` uses React, Vite and strict TypeScript with Node 24+ and npm.
  Keep the npm lockfile; no runtime imports from neighboring repositories.
- Under `frontend/src/`: pages in `pages/`, API calls/types in `api/`, reusable
  controls in `components/`, state hooks in `hooks/`, routes in `navigation/`,
  connection/session handling in `connections/`, styles in `styles/`.
- Put English copy in `frontend/src/i18n/` using typed translation selectors.
- `backend/cli/` configures the Go host; `backend/static/` serves built assets;
  `backend/connections/` handles sessions, upstream mapping and the API proxy.
- [docker/Dockerfile](docker/Dockerfile) packages both frontend and Go server.

## Behavior to preserve

- Support multiple independent API connections. Names/URLs can live in browser
  storage; API passwords stay in backend session memory. Disconnecting one API
  must not sign out another or remove its saved connection.
- Browser requests use the same-origin `/ui` backend. Preserve origin checks,
  HttpOnly/SameSite cookies, Secure cookies for HTTPS, session expiry and rejected
  upstream redirects. Do not send API credentials through URLs or browser storage.
- `UI_API_UPSTREAMS` maps host/port internally while the user sees the public URL.
  Preserve URL scheme/base path; unmapped URLs connect directly. Keep this mapping
  in the backend. Discovery checks the fixed local endpoint; adding a connection
  stays blank, while connecting a discovered local API prefills non-secret fields.
- Do not automatically retry mutating commands after timeouts. Query the operation
  to inspect its outcome. Update TypeScript response validation with API changes.
- Preserve routes across reload/back/forward, stale-request cancellation and
  per-connection state. Keep expired questions unanswerable and handle conflicts.
- Reuse `GraphCanvas`, graph layout and panning hooks. Execution graphs show saved
  work; automation graphs show mission rules. Keep node details in dialogs and
  preserve mouse, keyboard, zoom and focus behavior.
- Use existing theme tokens/fonts. The COMMANDER wordmark uses Grunt Grotesk
  SemiBold without a star. Keep dialog labels and focus behavior accessible.

## Development and checks

From `apps/admin/ui/frontend`:

```sh
npm ci
npm run typecheck
npm test
npm run build
```

The Go server requires `frontend/dist/index.html`, so build once before starting
it. For hot reload, run `go run ./backend/cmd/commander-ui --listen-address 127.0.0.1:4276`
from `apps/admin/ui`, then `npm run dev` from `frontend`. Vite serves `4275` and
proxies `/ui` to `4276`; start a Commander API separately.

From the repository root, check the backend:

```sh
go test -race -count=1 -timeout=1m ./apps/admin/ui/backend/...
go vet ./apps/admin/ui/backend/...
```

For browser changes, after installing frontend dependencies, use the isolated stack:

```sh
docker compose -f .github/composes/compose.test.yaml --profile ui up --build --wait
(cd apps/admin/ui/frontend && npx playwright install chromium && npm run test:e2e)
docker compose -f .github/composes/compose.test.yaml --profile ui down --volumes
```

Always stop this test stack, including after failures. Browser tests use one
worker and the test-only API cleanup; run them separately from HTTP/MCP tests
sharing that stack. Keep traces/screenshots for failures. See
[integration CI](../../../.github/workflows/test-integration.yml) for browser dependencies
and [test guidance](../../../framework/tests/AGENTS.md) for isolation rules.
