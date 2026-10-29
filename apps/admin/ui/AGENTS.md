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
- [../docker/Dockerfile](../docker/Dockerfile) embeds the built frontend in the Go server
  with `-tags embed_ui`; it needs no separate asset directory at runtime.
- `../cmd/commander-admin` is the shared binary entry point. Its `serve` command
  delegates to `backend/cli`; do not add a second UI executable.

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
- Proxy mission detail paths retain the name as one escaped parameter, including
  encoded slashes, percent signs and backslashes. Preserve its exact escaped path
  upstream; reject structural traversal and keep other route checks intact.
- Do not automatically retry mutating commands after timeouts. Query the operation
  to inspect its outcome. Update TypeScript response validation with API changes.
- **Sessions** lists retained runtime resources through authenticated `/api/v1/sessions`
  and ID detail reads. Keep scope/owner filters in the route across history and
  reload. Link agent details by exact owner and task details by typed session
  metadata, never arbitrary action input/output. Show observed state, lifecycle
  disposition and activity separately, including placement blockers when overall
  preflight is ready. Session metadata never grants readiness or ownership.
  Controls use the existing same-origin proxy and exact saved ID/scope/owner, with
  one request UUID per explicit submission. Never retry controls automatically;
  preserve uncertain request IDs after dialog dismissal and require explicit
  saved-state readback before a new control. Polling does not clear that guard. `inspect` reads
  saved state; `verify` asks the owner. Busy resources can still accept controls.
  Require explicit close/abandon confirmation, keep closing distinct from confirmed
  closure, and retain abandonment's unconfirmed-closure explanation. Released
  records and unassigned reservations are read-only. Abort requests and discard
  dialogs on connection/session/selection changes; ignore stale responses.
- Mission creation uses authenticated `POST /api/v1/missions`, with API validation
  and create-only name conflicts. The dedicated `create-mission` workspace route
  highlights Missions and returns to the catalog via **All missions**. Keep it
  separate from mission-name routes so names such as `new` and `create` still work.
  Creation opens in **Form** with a name, ordered objectives and a searchable live
  action picker. Starting actions must be required; changing their role removes
  their starting selection. Retain selected names when agents disappear or catalog
  reads fail, and allow saving without installed actions. Inspect uses independent
  `/agents` snapshots for per-agent readiness, labels stale reports, and never infers
  operation preflight or reserved capacity. Report failures must not block authoring.
  **Advanced settings** exposes schemas, rules and action definitions as raw
  JSON; **JSON** edits the full document. Patch only changed top-level values when
  moving from Form to JSON or submitting. Preserve untouched fields, numeric tokens
  and string escapes; reject unrepresentable JSON-to-Form transitions without
  altering the source. Keep invalid advanced drafts editable and block submission.
  **Define action** opens a local draft dialog for desired contracts. It must use
  a portal and stop submit propagation so adding a definition cannot submit the
  mission form. Add/edit/remove never calls the API or installs actions. New
  definitions become required unless already referenced; edits preserve roles.
  Keep names read-only when editing so rules retain their references. Definitions
  stay referenced by required/optional actions in the picker; editing preserves
  their role. Removing a definition keeps action selections and rules intact;
  remove its selection separately if no longer needed.
  Preserve raw schema tokens, untouched definitions and unknown properties when
  editing the array. Reject duplicates and invalid structure without overwriting
  it; malformed advanced JSON remains repairable there. Discard unsaved dialogs
  on cancellation, mode/route or connection/session changes and restore focus.
  Keep the JSON editor's text unchanged when
  submitting so large schema numbers retain precision. Validate syntax and the
  required fields locally; the shared API validator owns mission semantics.
  Mission `objectives` is a nonempty array of nonblank text. Preserve its order,
  search every entry and display entries with the same `catalog-tags` list as
  agent responsibilities. Do not accept the old root `objective` field as an alias.
  Optional `action_definitions` contains static contracts with object schemas and
  secret names, separate from live action status. Preserve definitions through
  response validation and JSON readback; the shared API owns schema compilation,
  name uniqueness and mission references. Defining a capability does not install it.
  Saving does not start work or require installed actions. Open the saved detail
  after success; offer readback for duplicates and uncertain outcomes without
  retrying writes. Discard drafts on page exit, reload or connection/session changes,
  abort pending requests and ignore stale responses. Never persist drafts in
  browser storage or include secret values in definitions.
- Mission **Edit** opens `/missions/<name>/edit` through its separate
  `EditMissionPage.tsx`, loading the saved raw definition. Keep creation in
  `CreateMissionPage.tsx`; the flows may diverge, so do not merge them into a
  generic editor page. Reuse form fields and draft helpers. Names identify catalog records: keep the Form
  name read-only and reject JSON renames before `PUT /api/v1/missions/<name>`.
  Objectives, action roles/definitions, schemas and rules may change; saved
  operation snapshots remain unchanged. Cancel returns to details, while a
  confirmed save or explicit uncertain-write readback reloads the saved detail.
  Keep edits scoped to the connection/session/name and discard them on leaving
  the route. Do not overwrite an uncertain update with an automatic retry.
- Mission forms and details expose **Completion decision** as Model/Operator.
  Omitted/null policy displays Model; untouched form edits retain its exact
  representation. Only a changed decision writes `completion.decision_by`.
  Reject malformed or unsupported completion policy when switching JSON to Form,
  retaining the raw source for repair. Mission edits affect future operations;
  the controller still enforces each operation's saved policy.
- Mission details offer **Details / JSON** views. The `/missions/<name>/json`
  route survives reload and browser history. Retain validated response text from
  the same detail request; never reconstruct exported JSON from parsed JavaScript
  numbers. Format whitespace only, preserving number tokens and string escapes.
  Keep the JSON read-only and selectable, with Copy feedback and a manual-copy
  fallback. Clear clipboard feedback when leaving the view or changing its source;
  ignore copy results after unmount. Never refetch or mutate a mission to copy it.
- Operation responses require the server's `progress` projection. Display its
  finished/total task counts without recounting task statuses. Interrupted work
  remains unfinished even when the operation is terminal; keep refreshing details
  to receive late results. Settlement is separate from active status: it does not
  enable commands or prove agent idleness for cleanup. Keep unanswered questions
  answerable before and after `fallback_at`, while preserving active-only
  secret/command controls.
  `QuestionAnswerStatus` shares countdown and answer-origin rendering between
  question history and the open answer dialog. Countdown ticks stay local and
  stop at the cutoff, after an answer, on terminal status or unmount; do not
  announce every tick to screen readers. Missing origins remain unknown.
  Polling can reveal another accepted answer while a form is open: retain the
  draft read-only, show the saved answer/origin and offer saved-state inspection.
  A status refresh must not abort an in-flight POST; it may reflect that request's
  accepted result. Continue requiring readback after conflicts or uncertain writes.
- `CompleteOperationDialog` submits final output through the existing authenticated
  completion API. Review the operation's retained objectives, server progress and
  output schema; require explicit operator confirmation. Preserve raw output JSON
  on the wire, including number tokens and escapes. Active tasks, pending questions
  and unresolved non-model continuations block submission; interrupted outcomes
  may be accepted with documented limitations. The server owns final eligibility.
  Keep drafts in memory, isolated by connection, credentials and operation; polling
  may disable submission but must not discard a draft or abort an in-flight POST.
  Guard one submission at a time and require explicit successful saved-state
  readback after conflict, unknown outcome or dismissal while submitting, including
  before reopening. Polling alone does not clear this guard. Invalid output may
  be corrected explicitly after known rejection. Display saved `completed_by`,
  leaving absent provenance unknown; policy does not establish who completed it.
  Optional `completion_proposal` is proposed output, never acceptance or a final
  report. Detail queries retain its exact output JSON as client-only `outputText`
  from the response source; never seed the editor by reserializing parsed numbers.
  Load it when the dialog opens, retaining operator edits on subsequent polls.
  A replacement proposal revokes approval and offers explicit **Load latest
  proposal**, without silently replacing the draft. Showing proposer/time does
  not establish consent. Keep the manual completion path when no proposal exists.
- Operation **Logs** uses the single operation log API with route-preserved
  task/agent/action filters. Validate flat response provenance and cursors; keep
  server append order rather than sorting timestamps or UUIDs. Read recent pages
  with `desc`, reverse for display, then follow with `asc` from the newest entry.
  Keep one active request, cancel and ignore stale reads on source changes, and
  poll every two seconds only while following and visible, including after
  terminal operation status. Pause aborts pending reads; history and scrolling up
  pause following too. Keep at most 1,000 entries and resume after the newest
  retained row when history trimming drops newer entries. Render messages as
  plain text and keep controls and filter drafts local to the current source.
- Open the dedicated Secrets page from Agents via **Manage secrets**, with
  **All agents** returning to the catalog. Keep Secrets out of the sidebar and
  highlight Agents while it is open. Preserve its route and operation shortcuts.
  The Secrets page uses authenticated `/api/v1/secrets` metadata and
  `/api/v1/secret-requests`. Group refresh requests by shared secret name; their
  progress is not agent readiness or proof of installation. Inventory lists all
  saved names without an agent-type selector. Provide-value actions
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
  Merge execution-scoped task `agent_type`/`missing_requirements` into the same
  notice for running tasks with future deadlines. These are live API projections,
  not persisted task state; refresh even when the operation timestamp is unchanged.
  Ignore terminal operations and interrupted/expired tasks. Deduplicate secret names
  across preflight and runtime waits; installation, not storage, clears a wait.
- Preserve routes across reload/back/forward, stale-request cancellation and
  per-connection state. Keep answered questions read-only and handle conflicts;
  the operator waiting deadline never disables operator answers.
- Execution graphs show saved work. Reuse `GraphCanvas`, graph layout and panning
  hooks; keep node details in dialogs and preserve mouse, keyboard, zoom and focus
  behavior. Mission rules use a compact table of rules, `for_actions` and settings;
  they describe policies, not execution dependencies.
  List required and optional mission actions separately; optional declarations
  alone do not indicate blocked readiness before those actions are queued.
- Accept and display the `same-agent` mission rule. Operation `affinity` is a
  read-only placement view: show its owner or first-dispatch binding note, and
  give history errors precedence over unbound wording. Show queued-for-owner
  hints only on matching queued tasks in active operations. Retain terminal
  ownership as history, and use saved preflight reasons without inferring
  current capacity or promising fallback.
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
the server. For hot reload, run `go run ../cmd/commander-admin serve --listen-address 127.0.0.1:4276`
from `apps/admin/ui`, then `npm run dev` from `frontend`. Vite serves `4275` and
proxies `/ui` to `4276`; start a Commander API separately.

From the repository root, check the backend:

```sh
go test -race -count=1 -timeout=1m ./apps/admin/...
go vet ./apps/admin/...
```

For packaging, build assets using the [portable binary instructions](README.md#native-server),
then run `go test -tags embed_ui -race -count=1 -timeout=1m ./apps/admin/...`
and `go vet -tags embed_ui ./apps/admin/...` from the repository root.
The tagged tests start the real server from an unrelated working directory and
verify bundled assets, override precedence, proxy routing and shutdown. Keep the
frontend/package CI job selected for backend changes as well as frontend changes.

For browser changes, after installing frontend dependencies, use the isolated stack:

```sh
docker compose -f .github/composes/compose.test.yaml --profile ui up --build --wait
(cd apps/admin/ui/frontend && npx playwright install --only-shell chromium && npm run test:e2e)
docker compose -f .github/composes/compose.test.yaml --profile ui down --volumes
```

Always stop this test stack, including after failures. Admin UI browser tests
use the lightweight example-agent workflow; never require web/browser-agent images.
Chromium tests the Admin UI itself. Use one worker and the test-only API cleanup;
run them separately from HTTP/MCP tests sharing that stack. Keep traces/screenshots for failures. See
[integration CI](../../../.github/workflows/test-integration.yml) for browser dependencies
and [test guidance](../../../framework/tests/AGENTS.md) for isolation rules.
