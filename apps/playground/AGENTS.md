# Working on Playground

## Code map

- [`cli/`](cli/) parses flags/environment and starts the terminal program.
- [`internal/local/session.go`](internal/local/session.go) composes the existing
  MCP, API, controller, web and browser agent hosts for `--all-in-one`.
- [`internal/llm/`](internal/llm/) owns model discovery, provider protocols,
  streaming, tool-call conversion and redacted errors.
- [`internal/mcp/client.go`](internal/mcp/client.go) owns MCP transport, tool
  replies and operation snapshots. Keep the chat engine behind its interfaces.
- [`internal/chat/`](internal/chat/) owns conversation turns, queued input,
  watched operations, continuation review and operator commands.
- [`internal/ui/`](internal/ui/) owns Bubble Tea state, selectors and rendering.
- [`docker/Dockerfile`](docker/Dockerfile) is the lightweight client image used by
  Compose. [`docker/Dockerfile.aio`](docker/Dockerfile.aio) starts `--all-in-one`,
  includes Chromium/fonts and uses a real non-root user's home plus an init process
  for browser children. Keep browser packages aligned with the browser image.

## Behavior to preserve

- Chat goes through MCP even in all-in-one mode. Reuse app hosts rather than
  duplicating their lifecycle or calling the runner directly from chat.
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
- The all-in-one image binds the API to `0.0.0.0:4274` inside the container; document
  host publishing on `127.0.0.1` by default. MCP stays internal. Container browser
  runs use `CHROMIUM_NO_SANDBOX=true`; keep the process non-root. State remains
  temporary; adding image dependencies must not change session persistence.
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

Use the scripted-model and local HTTP/MCP fixtures in [`tests/`](tests/) for
behavior changes. The suite includes all-in-one coverage without a paid model
or host browser. For transport or workflow changes, also use the
[adapter integration tests](../../framework/tests/README.md#mcp-api-tests).

For image changes, build both Dockerfiles from the repository root. Verify the
all-in-one image starts without a model key, serves `/healthz` through its published
API port, runs headless Chromium against a local fixture and exits cleanly on
SIGTERM. Keep Docker lifecycle in shell/CI rather than Go test fixtures.

[Native release builds](../../.github/workflows/build-binaries.yml) run environment
discovery tests and `TestAllInOne` on Linux, macOS and Windows for amd64 and arm64.
Keep the embedded missions, local host lifecycle and test fixtures portable; native
binaries must run without a checkout or Go installation.
