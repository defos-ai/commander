# Adapters

## Overview

Adapters connect external clients to Commander. They send commands through the
message bus and query saved data through domain stores. The bundled
[MCP adapter](mcp/) lets AI clients run missions and inspect results.

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/adapters/mcp/cmd/commander-mcp
```

Start the [controller](../controllers/README.md#quick-start) in another terminal.
Connect your MCP client to `http://localhost:4273/mcp`; repository configurations
are provided for [VS Code](../../.vscode/mcp.json),
[Codex](../../.codex/config.toml), and [Cursor](../../.cursor/mcp.json).
See [local setup](../../docs/deployment.md) for Compose and connection settings.

`list_agents`, `list_actions` and `list_operations` return compact summaries.
Use `describe_action` for an action's schemas (optionally with `agent_id` for that
agent's version), `describe_agent` for full agent details, and `get_operation`
for task results and questions. `list_missions` includes mission schemas and
automation rules; `get_report` returns a completed operation's report.

Inspect `tasks[].continuation` through `get_operation`. Use `resolve_continuation`
with its task ID, current version, and selected actions (an empty array finishes
the branch). Omit `agent_id` for automatic scheduling. When operator guidance is
needed, use a linked `ask_question`, present it to the user, and submit their
actual answer with `answer_question`; the model then resumes the decision.
`get_operation` exposes `last_question` and `remaining_questions` (excluding the
displayed question). Questions have at most four options and a two-minute default
deadline, configurable with `timeout_seconds`. Expiry returns the decision to the
model without an operator answer; full history stays available.
See [continuations](../../docs/abstractions.md#next-actions-and-continuations).

## Development and testing

- Add MCP tools in [`mcp/`](mcp/); keep startup and configuration in [`mcp/cli/`](mcp/cli/).
- For another adapter, add `<adapter-type>/` with its own module, CLI, entrypoint,
  and tests. Use domain stores for queries and comms clients for commands.
- Cover external behavior in the adapter's `tests/` folder.

From the repository root:

```sh
go test -race -count=1 ./apps/adapters/mcp/...
```

See the [framework guide](../../framework/README.md) for shared contracts and
[testing conventions](../../framework/tests/README.md#writing-tests) for coverage.
