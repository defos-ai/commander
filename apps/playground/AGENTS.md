# Working on Playground

## Code map

- [`cli/`](cli/) parses flags/environment and starts the terminal program.
- [`internal/local/session.go`](internal/local/session.go) composes the existing
  MCP, API, controller and agent hosts for `--all-in-one`.
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
  for browser children. Keep browser packages aligned with the web-quality image.

## Behavior to preserve

- Chat goes through MCP even in all-in-one mode. Reuse app hosts rather than
  duplicating their lifecycle or calling the runner directly from chat.
- All-in-one starts temporary state and loopback services, waits for readiness,
  then shuts down hosts before deleting that session's files. It must not use or
  delete the user's configured database/storage or existing Compose state.
- All-in-one defaults an unset or empty `API_USERNAME` to `commander` and
  generates a cryptographically random password when `API_PASSWORD` is unset or
  empty. Preserve explicit nonempty overrides and pass session credentials to
  the embedded API through its run options, without modifying process environment.
  Reuse `host.BasicCredentials` and `host.GeneratePassword`; never write credentials
  to files or logs, include them in model messages, or expose them through `/status`.
  Local `/credentials api` and `/credentials mcp` display only the selected
  credentials during setup or chat; `/credentials` displays both. Handle these
  commands, including invalid arguments, before forwarding input to the model.
  Keep generated credentials scoped to the session. Standalone API startup still
  requires environment credentials.
  The default API listen address is `127.0.0.1:4274`; `API_LISTEN_ADDRESS` overrides
  it, including port `0` for fixtures. Set the API listen flag explicitly so
  generic `PORT` cannot change it. Report the bound address after readiness.
- All-in-one also generates `host.BearerCredentials` from an unset/empty
  `MCP_TOKEN`, preserving explicit nonempty overrides. Validate it before starting
  hosts and pass the same credentials to MCP and Playground without changing the
  environment. Local `/credentials` displays the token only on request.
- Native `commander credentials api|mcp` reads only its own environment; generated
  native credentials remain in the running Playground and use `/credentials`.
  In the all-in-one image, [`docker/entrypoint.aio.sh`](docker/entrypoint.aio.sh) exports credentials
  before starting Tini, then Commander. Preserve explicit nonempty overrides;
  generated values change on restart. Docker lookup reads PID 1's startup
  environment only after identifying Commander or its exact Tini invocation.
  Once identified, lookup failures must not fall back to another environment.
  Do not add credential files, sockets or session-discovery directories.
- Remote MCP uses environment-only `COMMANDER_MCP_TOKEN`; all-in-one ignores this
  remote credential. The MCP transport clones requests before attaching Bearer
  authorization, blocks redirects, and never exposes HTTP error bodies. After
  `401`, reject subsequent requests locally, including the SDK's initialization
  fallback. Reconnection is required; never retry commands on authentication failure.
- Compose Playground receives MCP's running token through host-side credential
  lookup and `COMMANDER_MCP_TOKEN`. Keep lookup failures from launching the client
  and use `run --no-deps` after stack readiness so launch cannot recreate MCP.
  Do not add credential files, Docker socket mounts or an unauthenticated token endpoint.
- The all-in-one image binds the API to `0.0.0.0:4274` inside the container; document
  host publishing on `127.0.0.1` by default. MCP stays internal. Container browser
  runs use `CHROMIUM_NO_SANDBOX=true`; keep the process non-root. State remains
  temporary; adding image dependencies must not change session persistence.
- Keep agent dependency discovery in the agent. The Playground renders readiness
  and questions; it does not install dependencies or invent fallback work.
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
- Preserve the DEFOS palette, star loaders, Markdown rendering, keyboard selectors
  and `NO_COLOR`/non-terminal behavior. Use [README.md](README.md) for user commands.

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
Keep the embedded mission, local host lifecycle and test fixtures portable; native
archives must run without a checkout or Go installation.
