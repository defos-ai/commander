# Operations

Open **Admin → Operations → Start operation**, choose a [mission](missions.md)
and provide input matching its schema. For AI-driven work, follow the
[MCP workflow](../../apps/adapters/README.md#operation-workflow); applications can
use the [HTTP API](../../apps/adapters/api/README.md#operation-commands).

## Start and assign work

1. Inspect the mission and the action schemas before assigning tasks.
2. Start the operation. It keeps the current mission definition for this run.
3. Review preflight. Install missing capabilities, configure their dependencies
   and provide requested [secrets](secrets.md).
4. Assign an action and its input. The controller chooses a compatible agent;
   tasks stay queued until readiness and placement requirements allow dispatch.
5. Follow the operation, answer [questions](questions.md) and review decisions.

Starting does not automatically assign `starting_actions`. Optional actions do
not block initial preflight, but must be ready when queued. Work using a
[session](sessions.md) must also have a usable resource on its exact owner.

Successful actions can propose follow-ups. Valid nonempty proposals run
automatically unless continuation policy requires model review. Failures,
interrupted outcomes, empty proposals and invalid proposals need review too.
Aggregation waits for the current decision's saved direct children.

In MCP, read the latest task and use `resolve_continuation` to choose follow-ups.
Submit `actions: []` to close that branch. Refresh saved state before retrying a
stale decision. Proposal order alone does not establish dependencies.

## Follow progress and evidence

| In Admin | What to inspect |
| --- | --- |
| Execution graph and tasks | Assignments, outcomes, pending decisions and results |
| Logs | Progress filtered by task, agent or action |
| Artifacts | Accepted files such as screenshots, source archives and downloads |
| Final report | The accepted operation output, downloadable as JSON |

In Logs, **Pause following** pauses updates and scrolling while work continues;
**Follow live** catches up. Task results and artifacts remain inspectable after
the operation ends. Artifact references are not public browser URLs; use Admin
to view or download files. See [log queries](../../apps/adapters/api/README.md#execution-logs)
for API access, pagination and following new entries.

Logs describe progress; checkpoints record execution facts. Neither proves that
an external side effect finished or that repeating it is safe. Use
[data sharing](data-sharing.md) to forward evidence to active analysis tasks and
[sessions](sessions.md) to manage resources that survive individual actions.

## Complete and settle

Finishing a scan or another task keeps the operation open. Continue work in the
same operation with `resolve_continuation` for follow-up actions or `assign_task`
for independent actions. An empty continuation decision ends only that branch.

Review task outcomes and answer pending questions, then submit output matching
the mission's output schema. With omitted/model completion policy,
`complete_operation` saves the final report immediately. With operator policy,
the model saves a completion draft; an operator reviews or edits it in Admin and
explicitly chooses **Complete operation**. Questions cannot grant this approval.
Operator completion policy and a saved draft do not block further work; only
terminal operations require starting a new operation.

`completed` means a final report was accepted. It can document limitations or
unknown outcomes; it does not certify that every objective or task succeeded.
An operation is **settled** when it is terminal and every task has a finished
outcome. Interrupted tasks remain unknown, so a completed operation may still
await late results. Settlement does not prove that an agent or resource is idle.

## Stop, time out and recover

Cancel an operation to stop queued work and prevent new assignments. Already
dispatched actions may still finish. Stopping a client reply or model review
does not cancel remote work. Exiting all-in-one Playground also stops its local
hosts; exiting a remote Playground leaves the server deployment running.

If a task reaches its deadline without a known result, its outcome becomes
unknown; this does not prove the action stopped. Late results and artifacts can
still be accepted. Before assigning replacement work, read the operation, task and
evidence to avoid repeating an external effect. Read saved state after an
uncertain command or write before submitting again.

The controller's initial task-result wait defaults to `15m`, configurable with
`TASK_TIMEOUT`. Logs and question activity do not extend saved task deadlines;
long operations may contain many shorter actions.

For missions lasting weeks or months, keep the controller and agents deployed
and connect a continuously running MCP decision maker outside Playground.
The controller executes automatic follow-ups; the MCP client must revisit model
decisions and pending questions, including their `fallback_at` deadlines.
See [deployment](../deployment.md) and [Playground controls](../../apps/playground/README.md#chat).
