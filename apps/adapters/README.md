# Adapters

Connect AI clients and internal applications to Commander to automate multi-step
operational workflows. Submit work and review saved results through either interface:

- **MCP:** let a model choose missions, assign work and review results.
- **[HTTP API](api/README.md):** create or update missions and manage operations from your app.

## Quick start

From the repository root, with Go 1.26.7+:

```sh
export MCP_TOKEN="$(openssl rand -hex 32)"
go run ./apps/adapters/mcp/cmd/commander-mcp
```

Start the [controller](../controllers/README.md#quick-start) and agents with the
same provider settings. For a ready-to-use stack, follow [deployment](../../docs/deployment.md).

Connect your MCP client to `http://localhost:4273/mcp` and send
`Authorization: Bearer <MCP_TOKEN>` on every request. Client examples:
[VS Code](../../.vscode/mcp.json), [Codex](../../.codex/config.toml),
[Cursor](../../.cursor/mcp.json). Remote connections need HTTPS and custom-header support.

For a Compose token:

```sh
docker compose exec mcp commander-mcp credentials
```

Add `--raw` to print only the token. Compose reuses its token across restarts;
set `MCP_TOKEN` before launch to replace it. Playground connects automatically.
In all-in-one Playground, use `/credentials`.

### Discover agent sources

Call `get_agent_sources`, then follow the spawner's registered installation
instructions with the selected `source` reference and `visibility`.
You can also install a GitHub repository URL that is not in the source list:

- Omit the version, or use `@stable`, for its promoted Latest release.
- Use `@v1.2.3` to pin a version.
- For private repositories, set `visibility: private`. Supply tokens through
  managed secrets or the spawner environment; never put them in tool inputs.

MCP reads the official [agent source list](../../sources/README.md) on demand.
The list is discovery metadata; it has no saved trust or approval state.

### Create and update missions

Call `create_mission` with a flat mission definition containing `name`,
`objectives`, `input_schema` and `output_schema`.
Add actions and rules as needed; see
[mission definitions](../../docs/abstractions.md#mission-operation-and-task).
Actions may be installed later. Saving a mission does not start work.

Names are unique and remain fixed. To change objectives, actions, rules or schemas,
read `describe_mission`, then call `update_mission` with the complete replacement
definition under the same name. Updates affect future operations; existing
operations retain their saved mission definition.
After an uncertain save, read `describe_mission` before retrying.
Keep secret values out of definitions and chat.

### Operation workflow

1. Choose a mission with `list_missions` and `describe_mission`; inspect action
   schemas with `describe_action`.
2. Call `start_operation`, review preflight, then `assign_task` with the action
   name and input. The controller selects the agent.
3. Follow `get_operation`. Resolve model decisions with `resolve_continuation`;
   use `ask_question` and `answer_question` for operator choices.
4. Review the evidence, finish pending work, then `complete_operation` with output
   matching the mission schema. Retrieve the result with `get_report`.

Use Playground's masked form or **Agents → Manage secrets** in Admin when secrets
are missing. Do not send values through chat or tools.

**Query saved state before retrying an uncertain command.** Accepted work may
continue, and another assignment creates another task. Use `cancel_operation`
to stop queued work; dispatched tasks may finish.

### Queries and artifacts

Use `list_agents` and `list_actions` for discovery, `describe_agent` for readiness,
and `get_task` for full inputs, results and artifacts. For paginated task/event
queries, pass `next_cursor` unchanged to the next call for the same operation.

Read saved progress with `operation_logs` using `operation_id`. Optional `task_id`,
`agent_id` and `action_name` filters match exactly. Pages default to 100 entries,
up to 1000. Use `order: "asc"` (default) for oldest first or `order: "desc"` for
newest first; reuse `next_cursor` with the same scope, filters and order to page.
Empty pages retain the cursor. Descending cursors continue toward older entries.
To follow new logs after a descending page, switch to `asc` with the **first/newest
entry's ID** as `cursor`. Delayed logs remain readable after an operation ends,
without a live controller.

Read `missions://artifacts/{id}` for metadata and
`missions://artifacts/{id}/files/{file_id}` for file content. To view screenshots
or download files in a browser, open the operation in [Admin](../admin/ui/README.md).

## Development and testing

[Contributor guidance](AGENTS.md) ·
[Test setup](../../framework/tests/README.md#mcp-api-tests) · [MCP source](mcp/)
