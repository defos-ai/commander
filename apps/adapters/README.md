# Adapters

## Overview

Adapters connect clients to Commander: commands go through the message bus;
queries read domain stores. The bundled [MCP adapter](mcp/) lets an LLM act as
Commander: choose a mission, assign work, review evidence and resolve open decisions.
The [HTTP API](api/README.md) exposes the same commands and queries for applications.
The controller selects agent instances and runs mission automation.

Normal MCP commands operate on tracked operations. Agent/action discovery remains
available as queries; agent administration belongs to the controller.

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/adapters/mcp/cmd/commander-mcp
```

Start the [controller](../controllers/README.md#quick-start) and agents with matching
connection settings. Connect your MCP client to `http://localhost:4273/mcp`.
Examples: [VS Code](../../.vscode/mcp.json), [Codex](../../.codex/config.toml),
[Cursor](../../.cursor/mcp.json). See [deployment](../../docs/deployment.md) for
Compose, provider choices and host configuration.

### Operation workflow

1. Use `list_missions`, then `describe_mission` to read the selected mission's
   objective, actions, automation and schemas. Inspect action schemas with
   `describe_action`; optional `agent_id` selects an instance's definition.
2. Call `start_operation`, inspect its preflight, then `assign_task` with
   `action_name` and `input`. The controller chooses a ready instance with capacity.
   Missing required-action coverage keeps tasks queued. Omit `step_id` unless the
   mission explicitly declares that step.
3. Follow results with `get_operation` or the paged tools below. Resolve pending
   continuations with `resolve_continuation`, using the current task/version and
   selected actions; an empty array finishes that branch.
4. For an operator decision, call `ask_question`, present it, and record the actual
   response with `answer_question`. Questions allow four options or free text and
   default to a two-minute deadline. Expiry returns the decision to the model.
5. After reviewing evidence and resolving work, call `complete_operation` with
   output matching the mission schema. Read the saved report with `get_report`.
   `fail_operation` and `cancel_operation` end work when appropriate; dispatched
   tasks may still finish after cancellation.

See [preflight](../../docs/abstractions.md#discovery-and-preflight) and
[continuations](../../docs/abstractions.md#next-actions-and-continuations) for the
decision rules. Finished operations are read-only; new work needs a new operation.

### Queries and artifacts

| Tool | Use |
| --- | --- |
| `list_agents`, `list_actions`, `list_operations` | Compact summaries. |
| `describe_agent` | Full catalog, readiness, freshness and current `work` snapshot. |
| `get_operation` | Tasks, results, preflight, continuations and questions. `last_question` is displayed; `remaining_questions` counts the others. |
| `operation_tasks` | Task summaries in assignment order. |
| `operation_events` | Events in sequence order; optional `wait_seconds` up to 30. |
| `get_task` | One task's full input, result, artifacts and continuation; requires `operation_id` and `task_id`. |

Paged tools take `operation_id`, optional `cursor`, and `limit` (default 100,
maximum 1,000). Copy `next_cursor` unchanged into the next call for the same tool
and operation; empty pages preserve it. Agent `work` is a snapshot; tasks and
execution records hold history.

Successful tool results contain JSON in one text `content` block, without
`structuredContent`; file references add resource-link blocks. Read
`missions://artifacts/{id}` for artifact metadata and
`missions://artifacts/{id}/files/{file_id}` for file content.

## Development and testing

Add tools in [`mcp/`](mcp/) and startup/configuration in [`mcp/cli/`](mcp/cli/).
Another adapter gets its own module, CLI, entrypoint and `tests/` directory.
Use domain stores for queries and the typed
[controller command client](../../framework/comms/clients/controller/commands/)
for operation commands; see the [comms guide](../../framework/comms/README.md).

Run the grouped HTTP and MCP tests against either isolated Compose stack in the
[test setup](../../framework/tests/README.md#mcp-api-tests).
Follow the [framework contracts](../../framework/README.md) and
[testing conventions](../../framework/tests/README.md#writing-tests).
