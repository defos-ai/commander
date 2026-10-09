# Adapters

Adapters connect clients to Commander. The [MCP adapter](mcp/) lets an LLM choose
missions, assign work and review evidence; the [HTTP API](api/README.md) provides
application access. Commands go to the controller, which selects agents and runs
automation. Queries read saved state.

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
export MCP_TOKEN="$(openssl rand -hex 32)"
go run ./apps/adapters/mcp/cmd/commander-mcp
```

Start the [controller](../controllers/README.md#quick-start) and agents with matching
connection settings. Connect your MCP client to `http://localhost:4273/mcp` with
`Authorization: Bearer <MCP_TOKEN>` on every request. Standalone MCP refuses to
start without a valid token; `/healthz` and `/aboutz` stay public. `/aboutz` reports
the runtime and application so supporting clients can suggest a token lookup command.
Examples: [VS Code](../../.vscode/mcp.json), [Codex](../../.codex/config.toml),
[Cursor](../../.cursor/mcp.json). See [deployment](../../docs/deployment.md) for
Compose, providers and host configuration.

Codex and Cursor read `MCP_TOKEN` from their environment; VS Code prompts for it.
Playground accepts a hidden token input or `COMMANDER_MCP_TOKEN`. In all-in-one mode, it generates and
shares the token internally; `/credentials` shows it on request.

To look up a Compose server's token:

```sh
docker compose exec mcp commander-mcp credentials
```

Compose generates the token on each MCP container start. Set `MCP_TOKEN` before
starting Compose to preserve your own token. Reconnect clients with the new token
after a restart. Add `--raw` to print only the token for shell commands;
[Playground setup](../playground/README.md#docker-recommended) shows an environment one-liner.

Native `commander-mcp credentials` reads the current environment. Tokens are never
written to logs or credential files. This is shared-token authentication: clients
need custom-header support, and remote access needs HTTPS. OAuth sign-in and
per-user permissions are not provided.

### Operation workflow

1. Use `list_missions` and `describe_mission` to choose a mission and read its
   actions, automation and schemas. `describe_action` gives an action's schema;
   optional `agent_id` selects an instance's definition.
2. Call `start_operation`, inspect preflight, then `assign_task` with `action_name`
   and `input`. The controller chooses an available agent; missing required-action
   coverage keeps tasks queued. Omit `step_id` unless the mission declares it.
3. Follow `get_operation`. Resolve pending continuations with
   `resolve_continuation`, supplying the current task/version and selected actions;
   an empty array finishes the branch.
4. Use `ask_question` for operator decisions and `answer_question` for the actual
   response. Questions allow four options or free text and default to a two-minute
   deadline; expiry returns the decision to the model.
5. After reviewing evidence and resolving work, call `complete_operation` with
   output matching the mission schema, then `get_report`. Use `fail_operation` or
   `cancel_operation` when appropriate; dispatched tasks may still finish.

See [preflight](../../docs/abstractions.md#discovery-and-preflight) and
[continuations](../../docs/abstractions.md#next-actions-and-continuations) for the
decision rules. Finished operations are read-only; new work needs a new operation.

### Queries and artifacts

Use `list_agents` / `list_actions` for discovery, `describe_agent` for readiness
and current work, and `get_task` for full input, result and artifacts. Agent
administration belongs to the controller.

`operation_tasks` and `operation_events` take `operation_id`, optional `cursor`,
and `limit` (default 100, maximum 1,000). Pass `next_cursor` unchanged to the next
call for the same tool and operation. Events can wait up to 30 seconds with
`wait_seconds`. Tool definitions describe the complete inputs and outputs.

Read `missions://artifacts/{id}` for metadata and
`missions://artifacts/{id}/files/{file_id}` for file content. Successful results
contain JSON in a text `content` block; files add resource-link blocks.

## Development and testing

[Contributor guidance](AGENTS.md) ·
[Test setup](../../framework/tests/README.md#mcp-api-tests) · [MCP source](mcp/)
