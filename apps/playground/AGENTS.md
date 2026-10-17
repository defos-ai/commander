# Working on Playground

## Code map

- [`cli/`](cli/) parses flags/environment and starts the terminal program.
- [`internal/local/session.go`](internal/local/session.go) composes the existing
  Admin, MCP, API, controller, web and browser agent hosts for `--all-in-one`.
- [`internal/llm/`](internal/llm/) owns model discovery, provider protocols,
  streaming, tool-call conversion and redacted errors.
- [`internal/mcp/client.go`](internal/mcp/client.go) owns MCP transport, tool
  replies and operation snapshots. Keep the chat engine behind its interfaces.
- [`internal/chat/`](internal/chat/) owns conversation turns, queued input,
  watched operations, continuation review and operator commands.
- [`internal/ui/`](internal/ui/) owns Bubble Tea state, selectors and rendering.
- [`docker/Dockerfile`](docker/Dockerfile) is the lightweight client image used by
  Compose. [`docker/Dockerfile.aio`](docker/Dockerfile.aio) starts `--all-in-one`,
  embeds Admin, includes Chromium/fonts and uses a real non-root user's home plus
  an init process for browser children. Keep browser packages aligned with the
  browser image.

## Behavior to preserve

- Chat goes through MCP even in all-in-one mode. Reuse app hosts rather than
  duplicating their lifecycle or calling the runner directly from chat.
- All-in-one starts Admin through its existing CLI host after API and agent
  readiness. Default to `127.0.0.1:4275`, honor `UI_LISTEN_ADDRESS`, and pass the
  listen flag explicitly so `PORT` cannot expose it. `--no-admin` is all-in-one
  only. Admin startup failures must leave MCP, API and agents running; retain a
  short notice and host-log details. Optional Admin failures must not fail cleanup.
  Stop and await Admin before removing session state. Show its ready address in
  `/status`; keep the startup banner short and never put credentials in a URL.
- `/admin` works during setup and chat without forwarding input to the model.
  Add a short Admin review link to each completed, settled operation's progress
  row; do not add a separate chat notice. Account for wrapped progress rows when
  sizing the chat viewport. Native
  all-in-one uses the ready Admin address; Docker all-in-one defaults the link to
  `http://localhost:4275`. This is a conventional default, not detection of a
  published port. `--admin-url` / `COMMANDER_ADMIN_URL` optionally overrides the
  browser address; it does not change listeners or port mappings. Never infer
  Admin's address from a remote MCP endpoint. Do not advertise a disabled or
  failed local Admin. Remote mode without a configured URL gives short `/admin`
  setup guidance and no operation review link.
  Local links use `#/connect?api=...&username=...` with URL-encoded non-secret
  values; operation review links add `operation=...`. Preserve encoded query
  values when adding the destination. Never include passwords or tokens, log
  credentials, or send the link through model messages. Keep links out of the
  startup banner.
  Render terminal links with a short label and the full prefill target; retain
  plain URLs for no-color/non-terminal output.
  Compose explicitly supplies the public Admin URL from its known published port,
  with `COMMANDER_ADMIN_URL` overriding it. Remote review links use
  `#/operations/<encoded-id>` so Admin can ask which connection to use;
  remote mode has no local API information to prefill a connection.
- Model guidance directs screenshot/file viewing to Admin's operation artifacts,
  using a displayed review link or `/admin`. Keep `missions://` URIs for MCP
  resource access; never present them as browser/download links. Playground has
  no image preview. Do not assume Admin is running or invent its address.
- Active blocked preflight may identify managed secrets with `agent_type` and
  `missing_secrets`. Show `secrets needed` only for an action without a ready
  candidate and a valid scoped name; never infer managed secrets from sensitive
  environment requirements or pending delivery requests. Reuse that operation's
  Admin review link, or show `/admin` setup guidance without a configured URL.
  Model guidance sends users to Admin Secrets and must not solicit values in
  chat, tool inputs or `ask_question`. Saving is not installation/readiness;
  preserve existing preflight review scheduling and wait for fresh readiness.
- Native release binaries build Admin assets into `apps/admin/ui/backend/static/dist`
  and compile with `-tags embed_ui`. Untagged Go builds still work without Node;
  use `UI_STATIC_DIR` for an existing frontend or `--no-admin` for a terminal-only
  run. Keep `UI_STATIC_DIR`, `UI_PUBLIC_ORIGIN` and `UI_API_UPSTREAMS` semantics in
  the Admin host. When no upstream mapping is supplied, map the session's exact
  bound API address to itself so custom ports work without relaxing network
  policy. Use loopback URLs for wildcard listeners. Native tests must exercise
  both bundled and directory assets.
- The embedded API identifies itself as `application: commander` at `/aboutz`;
  `API_RUNTIME` defaults to `native`, and the all-in-one image sets `docker`.
  Embedded MCP uses the same application and reads `MCP_RUNTIME` (default native).
  Keep this public metadata limited to deployment enums; it retains API Host,
  Origin and proxy validation and must never expose credentials or paths.
- All-in-one starts temporary state and loopback services, waits for readiness,
  then shuts down hosts before deleting that session's files. It must not use or
  delete the user's configured database/storage or existing Compose state.
- All-in-one defaults an unset or empty `API_USERNAME` to `commander` and
  generates a cryptographically random password when `API_PASSWORD` is unset or
  empty. Preserve explicit nonempty overrides and pass session credentials to
  the embedded API through its run options, without modifying process environment.
  Reuse `host.BasicCredentials` and `host.GeneratePassword`; never write credentials
  to files or logs, include them in model messages, or expose them through `/status`.
  Local `/credentials` displays both credentials during setup or chat; it accepts
  no arguments. Handle the command and invalid arguments before forwarding input
  to the model. List it inside help, without a separate pre-help notice.
  Keep generated credentials scoped to the session. Standalone API startup still
  requires environment credentials.
  The default API listen address is `127.0.0.1:4274`; `API_LISTEN_ADDRESS` overrides
  it, including port `0` for fixtures. Set the API listen flag explicitly so
  generic `PORT` cannot change it. Expose the bound API and MCP addresses through
  `/status` after readiness, keeping credentials out of that command.
- All-in-one also generates `host.BearerCredentials` from an unset/empty
  `MCP_TOKEN`, preserving explicit nonempty overrides. Validate it before starting
  hosts and pass the same credentials to MCP and Playground without changing the
  environment. Local `/credentials` displays the token only on request.
- Native `commander credentials api|mcp` reads only its own environment; generated
  native credentials remain in the running Playground and use `/credentials`.
  `--raw` prints only the selected password or token on explicit CLI lookup.
  In the all-in-one image, [`docker/entrypoint.aio.sh`](docker/entrypoint.aio.sh) exports credentials
  before starting Tini, then Commander. Preserve explicit nonempty overrides;
  generated values change on restart. Docker lookup reads PID 1's startup
  environment only after identifying Commander or its exact Tini invocation.
  Once identified, lookup failures must not fall back to another environment.
  Do not add credential files, sockets or session-discovery directories.
- Remote MCP uses `COMMANDER_MCP_TOKEN` or a masked setup input; all-in-one ignores
  this remote credential. Validate entered tokens with `host.BearerCredentials`;
  keep them in session memory, outside the environment, files and model history.
  The MCP transport clones requests before attaching Bearer
  authorization, blocks redirects, and never exposes HTTP error bodies. After
  `401`, reject subsequent requests locally, including the SDK's initialization
  fallback. During setup, reconnect only when the operator submits a token with
  Enter; never retry commands on authentication failure.
- Connection authentication errors may query the endpoint's sibling `/aboutz`
  without credentials, cookies or redirects, using a bounded wait/body. Only
  validated deployment enums select predefined token lookup hints; do not display
  server-provided commands or response bodies. Return a typed authentication error
  with an optional predefined lookup command; missing metadata still allows token
  entry. Successful connections need no metadata probe. Displayed token hints use
  `--raw` and `docker compose exec -T`; the UI must never execute those commands.
- Compose's `mcp-credentials` one-shot initializer persists the shared token in a
  dedicated named volume. MCP and Playground mount it read-only; their wrappers
  export credentials to process environments before exec. Reuse the saved token
  across restarts, replacing it when an explicit nonempty `MCP_TOKEN` is configured.
  Preserve `COMMANDER_MCP_TOKEN` as a client override. This Compose bootstrap file
  is the only exception to native/all-in-one credential-file rules. Fail before
  starting the client if its required token cannot be read. Use `run --no-deps`
  after stack readiness so launch cannot recreate MCP. Do not add Docker socket
  mounts or an unauthenticated token endpoint.
- The all-in-one image builds Admin assets with Node, copies them into
  `apps/admin/ui/backend/static/dist`, and compiles Commander with `-tags embed_ui`.
  Do not copy a loose frontend directory into the runtime image. It binds Admin
  to `0.0.0.0:4275` and the API to `0.0.0.0:4274`; document host publishing on
  `127.0.0.1` by default. Admin proxies to the API inside the same container, so
  the quick start publishes only `4275`; API publishing stays optional. MCP stays
  internal. The default advertised link assumes host port `4275`; use optional
  `--admin-url` after the image name for another published port or host.
  Container browser runs use `CHROMIUM_NO_SANDBOX=true`; keep the process
  non-root. State remains temporary; adding image dependencies must not change
  session persistence. Container ports cannot be published after creation.
- Keep agent dependency discovery in the agent. The Playground renders readiness
  and questions; it does not install dependencies or invent fallback work.
  Embed both website missions: native quick start uses `review-website-no-browser`;
  `review-website-with-browser` requires discovered browser coverage. Do not create
  partial completion or browser fallback policy in the client.
  Wait for both agent types to register. The web agent owns HTTP work and remains
  ready without Chromium; the browser agent advertises rendering requirements.
- Preserve the chat engine's single-owner state and responsive queued input,
  cancellation, slash commands and background operation updates. Respect contexts
  throughout streams and tool calls; do not block the Bubble Tea update loop.
- `/stop` ends model work, not the server operation. Watching an operation can
  resume model review. Do not automatically retry failed model or mutating MCP
  requests: their execution outcome may be unknown.
- Only explicit operator input answers a question. An expired question returns
  the decision for review; it never supplies approval. Keep task/continuation
  revisions when deciding whether a review is new or stale.
- Show readable errors by default and redacted provider details with `--debug`.
  Keep keys/history in memory and avoid repeating full schemas/results in prompts.
- Model discovery authentication failures return to an empty, masked key prompt;
  preserve the provider and base URL, and retry only after explicit submission.
  Use typed provider errors, including Gemini's structured invalid-key response.
  Other discovery failures retain manual URL/model setup. Provider key links are
  predefined preset metadata; never render URLs or recovery commands from errors.
  Setup errors are transient prompt feedback: show only the current failure and
  clear it on retry, success or back; never carry it into the chat history.
- Preserve the DEFOS palette, star loaders, Markdown rendering, keyboard selectors
  and `NO_COLOR`/non-terminal behavior. Use [README.md](README.md) for user commands.
  Keep mouse reporting disabled so plain drag selects through the terminal.
  For real terminal output, save, enable and restore alternate-scroll mode
  (DEC 1007); supported terminals translate wheel/trackpad events into Up/Down.
  Use Up/Down for provider/model selection and focused questions, not chat
  scrolling; leave the chat keys available for future message history. Chat
  scrolling uses PgUp/PgDown. Wheel events translated into arrow keys do not
  scroll chat. Up/Down must never submit input or answers. Preserve drafts and
  question focus. Do not emit terminal mode controls to files.
  Leave selection and copying to the terminal. Do not draw an application-owned
  selection, intercept copy shortcuts or send clipboard control sequences.
  Ctrl+C retains its stop/quit behavior. Keep selection/copy instructions in
  `/help`, out of the footer. Explain plain drag and the terminal's normal copy
  command in help. Live output can change selected cells; do not promise a
  frozen screen. Do not add a mouse mode command.
  Keep chat scrolling hints in `/help`; show the footer's stop shortcut only
  while a reply is active.
  All-in-one startup shows the provider/model, readiness with two connected agents,
  one review prompt and the temporary-state note. Keep URLs, host inventories and
  credential instructions behind `/status`, model queries and `/credentials`.

## Checks

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/playground/...
go vet ./apps/playground/...
go build -o commander ./apps/playground/cmd/commander
```

For native packaging, build the frontend as shown in [README.md](README.md#native-one-executable),
then run `go test -tags embed_ui -race -count=1 -timeout=3m ./apps/playground/...`
and `go build -tags embed_ui -o commander ./apps/playground/cmd/commander`.

Use the scripted-model and local HTTP/MCP fixtures in [`tests/`](tests/) for
behavior changes. The suite includes all-in-one coverage without a paid model
or host browser. For transport or workflow changes, also use the
[adapter integration tests](../../framework/tests/README.md#mcp-api-tests).

For image changes, build both Dockerfiles from the repository root. Verify the
all-in-one image starts without a model key, serves Admin assets and `/healthz`
through published port `4275`, and authenticates/proxies to its API without
publishing `4274`. Check the default Admin link, explicit `--admin-url`,
`--no-admin`, explicit credential overrides, headless
Chromium against a local fixture and clean SIGTERM shutdown. Keep Docker lifecycle
in shell/CI rather than Go test fixtures.

[Native release builds](../../.github/workflows/build-binaries.yml) run environment
discovery tests and `TestAllInOne` on Linux, macOS and Windows for amd64 and arm64.
Keep the embedded missions, local host lifecycle and test fixtures portable; native
binaries must run without a checkout or Go installation.
