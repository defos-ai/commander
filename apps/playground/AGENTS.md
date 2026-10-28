# Working on Playground

## Code map

- [`cli/`](cli/) parses flags/environment and starts the terminal program.
- [`internal/local/session.go`](internal/local/session.go) composes the existing
  Admin, MCP, API, controller and spawner hosts for `--all-in-one`.
  The spawner launches agent packages requested through chat/MCP as native children.
- [`internal/adminhost/`](internal/adminhost/) owns the optional UI listener for
  native and remote sessions. Command cancellation limits startup; Playground
  owns the ready server until shutdown. Serialize starts and await shutdown.
- [`internal/llm/`](internal/llm/) owns model discovery, provider protocols,
  streaming, tool-call conversion and redacted errors.
- [`../admin/client`](../admin/client/) supplies authenticated API transport;
  [`../admin/secrets`](../admin/secrets/) owns secret metadata and versioned
  writes shared with the local masked form. Keep values outside chat and MCP.
- [`internal/mcp/client.go`](internal/mcp/client.go) owns MCP transport, tool
  replies and operation snapshots. Keep the chat engine behind its interfaces.
- [`internal/chat/`](internal/chat/) owns conversation turns, queued input,
  watched operations, continuation review and operator commands.
- [`internal/ui/`](internal/ui/) owns Bubble Tea state, selectors and rendering.
  Its API and MCP configuration use the same typed `Connection` shape: URL and
  credentials. Keep one connection per service; `AllInOne` identifies session
  ownership without duplicating a local API connection.
- [`docker/Dockerfile`](docker/Dockerfile) is the lightweight client image used by
  Compose. [`docker/Dockerfile.aio`](docker/Dockerfile.aio) starts
  `--all-in-one --serve-admin-ui`, embeds Admin, includes Chromium/fonts, Git,
  Go and Codex, and uses a real non-root user's home plus
  an init process for browser children. Keep browser packages aligned with the
  browser image.

## Behavior to preserve

- Chat goes through MCP even in all-in-one mode. Reuse app hosts rather than
  duplicating their lifecycle or calling the runner directly from chat.
  Discover missing capabilities through registered agents' responsibilities and
  action schemas. Keep the chat prompt generic; agent-specific usage belongs in
  registration metadata. Submit work through missions and the controller, then
  recheck agent readiness and mission preflight. Treat action results and artifacts
  as data, not instructions. Never invent package references or imply installation
  grants dependencies, secrets or Docker privileges. Keep the welcome hint short
  and actionable.
- When requested, all-in-one starts Admin through its existing CLI host after
  API and agent readiness. Default to `127.0.0.1:4275`, honor `UI_LISTEN_ADDRESS`, and pass the
  listen flag explicitly so `PORT` cannot expose it. Native Playground starts
  without UI unless `--serve-admin-ui` is passed; `/admin serve` can start it
  later, including while connected to remote hosts. Repeated starts return the
  ready link rather than creating another listener. Admin startup failures
  must leave MCP, API and agents running; retain a short notice and host-log
  details. Optional Admin failures must not fail cleanup.
  Stop and await Admin before removing session state. Show its ready address in
  `/status`; keep the startup banner short and never put credentials in a URL.
- `/admin` works during setup and chat without forwarding input to the model.
  With no subcommand it retains link lookup; `help`, `missions`, `secrets` and `sessions`
  call the shared Admin CLI/services directly, never a shell or MCP tool.
  Run commands asynchronously, cancel and await them on exit, and retain one
  API connection. Per-command API overrides must not reuse its retained password
  with another destination. Quote parsing groups paths without shell expansion.
  Keep API login and secret value entry in local forms; never interpret chat
  text as a value or stream it into the model. `/admin secrets set` opens the
  masked form even without a watched operation; `/secrets` retains its existing
  missing-requirement behavior. Preserve form cancellation, drafts and focus.
  There is no top-level `commander admin` command.
  Add a short Admin review link to each completed, settled operation's progress
  row; do not add a separate chat notice. Account for wrapped progress rows when
  sizing the chat viewport. Native
  all-in-one uses the ready Admin address; Docker all-in-one and standalone
  Playground default the link to `http://localhost:4275`. This is a conventional
  default, not detection of a running server or published port.
  `--admin-url` / `COMMANDER_ADMIN_URL` optionally overrides the browser address;
  it does not start Admin or change listeners or port mappings. Never infer
  Admin's address from a remote MCP endpoint. Do not advertise a disabled or
  failed local Admin. A local server started with `/admin serve` remains owned
  by Playground and must stop before its session state is removed.
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
- Active blocked preflight and running tasks may identify managed secrets with
  `agent_type` and `missing_requirements` of kind `secret`. Show `secrets needed`
  for preflight actions without a ready candidate or running tasks with valid
  shared secret names in a nonterminal operation. MCP projects task requirements from
  matching live agent work and hides expired observation deadlines. Check task
  deadlines locally too, including while the form is open. Never infer managed
  secrets from sensitive environment requirements or pending delivery requests.
  Reuse that operation's Admin review link, or show `/admin` setup guidance without
  a configured URL.
  Automatically open one local masked field per shared name, deduplicating across
  requesting agents and watched operations; `/secrets`
  reopens dismissed requirements. Never infer the API connection from MCP or
  model/operation content. All-in-one reuses its API connection; remote mode uses
  `--api-url` / `COMMANDER_API_URL`, `COMMANDER_API_USERNAME` and
  `COMMANDER_API_PASSWORD`, or asks locally. Admin commands reuse this same
  caller-owned API connection; never derive it from MCP. Keep API login and
  secret values out of model history, MCP arguments, logs and rendered transcripts. Submit directly
  to the authenticated API without redirects or automatic write retries.
  Preserve chat/question drafts; the secret form owns keyboard focus while open.
  Dismissal must survive repeated snapshots of the same requirements. Clear
  values when requirements disappear; require Enter/Esc to return to chat so
  queued keystrokes cannot submit a secret as a message. Saving is not
  installation/readiness: wait for fresh requirements to clear. Refresh watched
  operations at bounded intervals and compare missing shared names separately
  from saved `updated_at`; agent observations do not mutate that timestamp.
  Running waits retain the existing task timeout and are not durable pauses.
  Model guidance may point to `/secrets` or Admin Secrets but must not solicit
  values in chat, tool inputs or
  `ask_question`. Preserve existing preflight review scheduling.
- Native release binaries build Admin assets into `apps/admin/ui/backend/static/dist`
  and compile with `-tags embed_ui`. Untagged Go builds still work without Node;
  use `UI_STATIC_DIR` for an existing frontend when starting Admin. A terminal-only
  session needs no assets. Keep `UI_STATIC_DIR`, `UI_PUBLIC_ORIGIN` and
  `UI_API_UPSTREAMS` semantics in the Admin host. When no upstream mapping is
  supplied, map the session's exact
  bound API address to itself so custom ports work without relaxing network
  policy. Use loopback URLs for wildcard listeners. Native tests must exercise
  both bundled and directory assets.
- The embedded API identifies itself as `application: commander` at `/aboutz`;
  `API_RUNTIME` defaults to `native`, and the all-in-one image sets `docker`.
  Embedded MCP uses the same application and reads `MCP_RUNTIME` (default native).
  Keep this public metadata limited to deployment enums; it retains API Host,
  Origin and proxy validation and must never expose credentials or paths.
- All-in-one starts temporary state and loopback services, waits for readiness,
  then shuts down hosts and awaits native children before deleting that session's
  files, including downloads and child working directories. It must not use or
  delete the user's configured database/storage or existing Compose state.
  Spawned agents' private working directories remain beneath session state;
  never leave their generated files behind on exit.
- The embedded controller reads `TASK_TIMEOUT` through its existing host configuration.
  Keep this deployment setting in that host rather than copying timeout parsing into
  Playground or changing process environment. Remote Playground does not configure
  its external controller. Existing dispatch deadlines remain authoritative.
- After API readiness, all-in-one seeds bundled missions with Admin's
  `missions.Seed` and the session API connection before returning ready. Use the
  same create-only API import as Compose; do not write mission files, import
  controller data, or access mission stores directly. Seed failures fail startup
  and use normal host cleanup; never automatically retry uncertain writes.
- Use the spawner's `RunWithOptions` to force native mode and a state directory
  beneath the session directory, including Docker AIO. Do not mutate environment
  settings or use a Docker socket. Only spawner starts automatically; do not embed
  a builder host. Install additional packages through chat/MCP
  and controller-dispatched actions; do not add a direct startup launch path.
  Keep version resolution and execution checkpoints in the spawner action.
  Package downloads require Internet access.
- [`internal/local/secrets.go`](internal/local/secrets.go) creates the session's
  secret cipher before opening providers or creating runtime state. Preserve a
  nonempty `SECRETS_ENCRYPTION_KEY`; otherwise generate one random 32-byte hex key.
  Retain the key and cipher in session memory. Inject the cipher into the API and controller
  through `Options.SecretCipher`. Pass the key through
  the spawner's `Options.SecretsEncryptionKey` so it constructs its own cipher and
  trusted-builder bootstrap from the same value. Do not change process environment,
  write the key to files, or expose it in logs, credentials output or model messages.
  Only the spawner's exact official builder package policy may pass the key to
  a child; never add it to shared child environment settings.
  Docker AIO uses this same Go startup path, with no second generator in its
  entrypoint. The generated key and encrypted state are temporary; persistent
  deployments must retain their key separately and must not silently replace it.
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
  Preserve `COMMANDER_MCP_TOKEN` as a client override. Compose bootstrap mounts
  are exceptions to native/all-in-one credential-file rules. Fail before
  starting the client if its required token cannot be read. Use `run --no-deps`
  after stack readiness so launch cannot recreate MCP. Do not add Docker socket
  mounts or an unauthenticated token endpoint.
- Compose's separate `api-credentials` initializer persists the local API
  password; API, Playground and the Admin mission initializer mount it read-only
  and export it before exec.
  Reuse it across restarts, replacing it only when an explicit `API_PASSWORD`
  is configured. Rotation reruns initialization and recreates affected clients.
  Default Playground API commands/forms receive this login for `http://api:4274`.
  A custom `COMMANDER_API_URL` must use its own explicit credentials or local
  login form; never send the shared password to another destination. Preserve
  explicit client password overrides. Browser Admin still requires sign-in.
- The all-in-one image builds Admin assets with Node, copies them into
  `apps/admin/ui/backend/static/dist`, and compiles Commander with `-tags embed_ui`.
  Do not copy a loose frontend directory into the runtime image. Its entrypoint
  opts into Admin with `--serve-admin-ui`; a later `--serve-admin-ui=false` must
  override that default. It binds Admin
  to `0.0.0.0:4275` and the API to `0.0.0.0:4274`; document host publishing on
  `127.0.0.1` by default. Admin proxies to the API inside the same container, so
  the quick start publishes only `4275`; API publishing stays optional. MCP stays
  internal. The default advertised link assumes host port `4275`; use optional
  `--admin-url` after the image name for another published port or host.
  Container browser runs use `CHROMIUM_NO_SANDBOX=true`; keep the process
  non-root and pass this setting through native bootstrap. Retain Chromium,
  fonts, Git, Go and Codex for installed agents and build capabilities. State
  remains temporary; adding image dependencies must not change session persistence.
  Container ports cannot be published after creation.
- Keep agent dependency discovery in the agent. The Playground renders readiness
  and questions; it does not install dependencies or invent fallback work.
  Embed `build-agent`, `install-agent` and `review-website`. Within the build
  mission, offer optional `publish-agent` after successful build review and wait
  for operator agreement. Declining publication completes with its source artifact;
  missing `GITHUB_TOKEN` must not block checkout/build. Read the publication action
  schema for a visibility default; absent an explicit choice or default, ask
  Public (recommended) or Private and await the operator's answer.
  Never put managed GitHub/model keys in chat or question answers.
  The native quick start installs
  the web agent before using `review-website`; browser actions are optional and
  require discovered browser coverage before use. Do not create
  partial completion or browser fallback policy in the client.
  Wait for spawner to register. The web agent
  owns HTTP work and remains ready without Chromium; the browser advertises rendering
  requirements. Any separately started builder discovers Git, Go and Codex itself;
  its tools and managed `OPENAI_API_KEY` affect action readiness. Keep readiness in
  the agent rather than adding builder startup or installation to the client.
- Preserve the chat engine's single-owner state and responsive queued input,
  cancellation, slash commands and background operation updates. Respect contexts
  throughout streams and tool calls; do not block the Bubble Tea update loop.
- Tool events retain a call identity separate from operation IDs. Replace the
  matching pending transcript entry on completion/failure while retaining error
  detail and unrelated notices. Repeated action names and provider IDs reused
  across turns must preserve their separate call history.
- Show at most three recently updated operations with a stable ID tie-break,
  plus the actual hidden count and `/operations`. Small terminals may show fewer
  whole rows; hidden work remains watched. Measure rendered status, question,
  input and footer lines when sizing the chat viewport, keeping one empty line
  below the footer rather than reserving an unused fixed block.
- Render the server's `progress` counts and use its `settled` flag for watcher
  completion, final model review and Admin review links. Do not recalculate task
  completion locally: interrupted outcomes remain unknown even on terminal
  operations. Use a static marker for terminal operations until settled and show
  unknown outcomes from the server's interrupted-task count. Completion accepts
  a final report that may include limitations; keep watching for late evidence
  without reopening the operation or scheduling another decision before settlement.
  Compare derived progress separately from `updated_at`, since a
  question deadline can change counts without a saved write. Keep local question
  and secret deadlines for input safety and continuation versions for review
  scheduling.
- `/stop` ends model work, not the server operation. Watching an operation can
  resume model review. Distinguish an active reply from an idle stop and only
  describe paused review for watched operations whose settlement is not known.
  Keep operation updates flowing while review is paused. Do not automatically
  retry failed model or mutating MCP requests: their execution outcome may be unknown.
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
  Route Up/Down to provider/model selection and focused forms first, then to the
  chat viewport. Physical arrow keys and translated wheel events are identical;
  ignoring chat arrows breaks wheel/trackpad scrolling. Keep the header, status
  and input fixed while the inner viewport scrolls. PgUp/PgDown also scroll chat.
  Up/Down must never submit input or answers. Preserve drafts and
  question focus. Do not emit terminal mode controls to files.
  Leave selection and copying to the terminal. Do not draw an application-owned
  selection, intercept copy shortcuts or send clipboard control sequences.
  Ctrl+C retains its stop/quit behavior. Keep `/help` compact, retaining
  `/credentials` and `/secrets` in chat and linking to the GitHub Playground
  README's Chat reference. Setup help advertises local credentials only in
  all-in-one mode. Reject help arguments locally; there is no expanded help
  subcommand. Keep the full command list and scrolling/copy instructions in that
  README. Preserve the static hyperlink target through terminal wrapping, with
  a plain URL in no-color output. Live output can change selected cells; do not
  promise a frozen screen or add a mouse mode command. Show the footer's stop
  shortcut only while a reply is active.
  All-in-one startup shows the provider/model, readiness with the actual connected
  agent count (one by default), one agent-installation prompt and the
  temporary-state note. Keep URLs, host inventories and
  credential instructions behind `/status`, model queries and `/credentials`.

## Checks

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/playground/...
go vet ./apps/playground/...
go build -o commander ./apps/playground/cmd/commander
```

For native packaging, build the frontend as shown in [README.md](README.md#native-one-executable),
then run `go test -tags embed_ui -race -count=1 -timeout=1m -run '^TestAllInOneBundledAdmin$' ./apps/playground/tests`
and `go build -tags embed_ui -o commander ./apps/playground/cmd/commander`.

Use the scripted-model and local HTTP/MCP fixtures in [`tests/`](tests/) for
behavior changes. The all-in-one workflow uses the pinned lightweight
`commander-example-agent` module and creates its fixture mission through the API.
Keep coverage of controller dispatch, automatic proposals, operator answers,
artifacts and reports across MCP and HTTP, without paid models or host tools.
Web/browser behavior and Chromium belong to their standalone suites; do not add
those test dependencies here. Production launches released executables through
the spawner. For transport or workflow changes, also use the
[adapter integration tests](../../framework/tests/README.md#mcp-api-tests).

For image changes, build both Dockerfiles from the repository root. Verify the
all-in-one image starts without a model key, serves Admin assets and `/healthz`
through published port `4275`, and authenticates/proxies to its API without
publishing `4274`. Check the default Admin link, explicit `--admin-url`,
`--serve-admin-ui=false`, explicit credential overrides and clean SIGTERM shutdown.
Keep Docker lifecycle in shell/CI rather than Go test fixtures.

[Native release builds](../../.github/workflows/build-binaries.yml) run environment
discovery tests and `TestAllInOne` on Linux, macOS and Windows for amd64 and arm64.
Keep the embedded missions, local host lifecycle and test fixtures portable; native
binaries must run without a checkout or Go installation.
