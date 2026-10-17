# Working on the admin UI

## Code map

- `frontend/` uses React, Vite and strict TypeScript with Node 24+ and npm.
  Keep the npm lockfile; no runtime imports from neighboring repositories.
- Under `frontend/src/`: pages in `pages/`, API calls/types in `api/`, reusable
  controls in `components/`, state hooks in `hooks/`, routes in `navigation/`,
  connection/session handling in `connections/`, styles in `styles/`.
- Put English copy in `frontend/src/i18n/locales/en/` using typed translation selectors.
- `backend/cli/` configures the Go host; `backend/static/` serves built assets;
  `backend/connections/` handles sessions, upstream mapping and the API proxy.
- [docker/Dockerfile](docker/Dockerfile) embeds the built frontend in the Go server
  with `-tags embed_ui`; it needs no separate asset directory at runtime.

## Behavior to preserve

- Packaged builds embed generated `backend/static/dist/` with the `embed_ui` tag;
  missing assets must fail the build. Keep generated files ignored. Ordinary Go
  builds/tests require no frontend build and serve `./frontend/dist` by default.
  Explicit `--static-dir` / `UI_STATIC_DIR` override bundled assets; flags win,
  and explicitly blank directories are errors. Directory serving must retain
  `os.Root` confinement; use the same route, cache and header rules for embedded
  files. `static.NewHandlerFS` accepts trusted filesystems with seekable files.
- Support multiple independent API connections. Names/URLs can live in browser
  storage; API passwords stay in backend session memory. Disconnecting one API
  must not sign out another or remove its saved connection.
- Browser requests use the same-origin `/ui` backend. Preserve origin checks,
  HttpOnly/SameSite cookies, Secure cookies for HTTPS, session expiry and rejected
  upstream redirects. Do not send API credentials through URLs or browser storage.
- `UI_API_UPSTREAMS` maps host/port internally while the user sees the public URL.
  Preserve URL scheme/base path. Unmapped public APIs connect directly; private
  APIs require an exact mapping, except loopback on the fixed local port 4274.
  Keep public and mapped connection pools separate, validate every DNS address
  and dial the checked IP. Always block metadata/link-local addresses and never
  use environment proxies. Keep this mapping in the backend. Discovery checks
  the fixed local endpoint. Manual Add defaults
  to `New connection` and username `commander`, with URL/password blank. Discovered
  local APIs retain their existing non-secret defaults. Re-login defaults to
  `commander`; editing an active session retains its supplied username.
  Focus the first empty field when the connection dialog opens.
- `#/connect?api=...&username=...` accepts only validated, non-secret prefill data.
  Optional `operation=...` opens that operation after connection/sign-in;
  `#/operations/<encoded-id>` asks the user to choose or add the API connection.
  Never guess an API for remote operation links. Keep the intended destination
  through password entry, cancellation, reload and browser history navigation.
  Prefill `Local connection`, the API URL and username; leave the password empty
  and focus the first empty field. Reuse a saved connection for the same normalized
  API URL. Links must not create or authenticate connections automatically, alter
  unrelated connections, relax backend upstream policy, or accept credentials in
  the API URL. Keep manual Add and local discovery behavior intact.
- The password hint reads public `/aboutz` through the existing proxy before
  sign-in. Keep commands predefined in the frontend; never use commands returned
  by an API. Cancel discovery on URL changes and keep failed hints from blocking
  connection. Public discovery must never forward session credentials.
- Validate URLs before writing saved connection metadata. Persist only IDs,
  names and normalized URLs without credentials, query strings or fragments.
- Do not automatically retry mutating commands after timeouts. Query the operation
  to inspect its outcome. Update TypeScript response validation with API changes.
- Open the dedicated Secrets page from Agents via **Manage secrets**, with
  **All agents** returning to the catalog. Keep Secrets out of the sidebar and
  highlight Agents while it is open. Preserve its route and operation shortcuts.
  The Secrets page uses authenticated `/api/v1/secrets` metadata and
  `/api/v1/secrets/requests`. Group refresh requests by agent type/name; their
  progress is not agent readiness or proof of installation. Inventory requires an
  explicit agent type, including types with no live agent. Provide-value actions
  read current metadata before choosing create or replace; only a confirmed 404
  permits create. Replacement/deletion use the saved ID and version.
  Keep values in temporary form/request memory; mask by default and require an
  explicit switch for visible multiline input. Clear fields on submission/close,
  never write values to browser storage, URLs, diagnostics or result views.
  Writes must not retry automatically;
  conflicts and uncertain outcomes require a fresh metadata read. Clear dialogs
  when their connection/session changes and ignore stale responses. The proxy
  supports PUT and DELETE with the same session/origin checks as POST; public
  probes and `/aboutz` remain read-only.
- Operation secret notices use preflight's explicit `agent_type` and
  `missing_requirements` with `kind: secret` on blocked actions, only while the
  operation is active. The `sensitive` boolean marks sensitive local configuration;
  never infer managed secrets from it or associate agent lease requests with an
  operation. Preflight includes requirements only for live candidates. Hide notices
  once a ready candidate covers the action. Navigation opens Secrets on the current
  connection without submitting values or opening a write dialog automatically.
- Preserve routes across reload/back/forward, stale-request cancellation and
  per-connection state. Keep expired questions unanswerable and handle conflicts.
- Execution graphs show saved work. Reuse `GraphCanvas`, graph layout and panning
  hooks; keep node details in dialogs and preserve mouse, keyboard, zoom and focus
  behavior. Mission rules use a compact table of rules, `for_actions` and settings;
  they describe policies, not execution dependencies.
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

For ordinary source builds, build `frontend/dist/index.html` before starting
the server. For hot reload, run `go run ./backend/cmd/commander-ui --listen-address 127.0.0.1:4276`
from `apps/admin/ui`, then `npm run dev` from `frontend`. Vite serves `4275` and
proxies `/ui` to `4276`; start a Commander API separately.

From the repository root, check the backend:

```sh
go test -race -count=1 -timeout=1m ./apps/admin/ui/backend/...
go vet ./apps/admin/ui/backend/...
```

For packaging, build assets using the [portable binary instructions](README.md#native-server),
then run `go test -tags embed_ui -race -count=1 -timeout=1m ./apps/admin/ui/backend/...`
and `go vet -tags embed_ui ./apps/admin/ui/backend/...` from the repository root.
The tagged tests start the real server from an unrelated working directory and
verify bundled assets, override precedence, proxy routing and shutdown. Keep the
frontend/package CI job selected for backend changes as well as frontend changes.

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
