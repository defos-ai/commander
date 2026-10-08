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
  Reuse `host.Credentials` and `host.GeneratePassword`; never write credentials to
  files or logs, include them in model messages, or expose them through `/status`.
  Only the local `/credentials` command displays them, during setup or chat.
  Keep generated credentials scoped to the session. Standalone API startup still
  requires environment credentials.
  The default API listen address is `127.0.0.1:4274`; `API_LISTEN_ADDRESS` overrides
  it, including port `0` for fixtures. Set the API listen flag explicitly so
  generic `PORT` cannot change it. Report the bound address after readiness.
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

[Native release builds](../../.github/workflows/build-binaries.yml) run environment
discovery tests and `TestAllInOne` on Linux, macOS and Windows for amd64 and arm64.
Keep the embedded mission, local host lifecycle and test fixtures portable; native
archives must run without a checkout or Go installation.
