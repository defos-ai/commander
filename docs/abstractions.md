# Core concepts

The [architecture overview](overview.md) explains Commander → Controller → Agent.
This guide defines the data and decisions passed between them.

## Mission, operation, and task

| Concept | Meaning | Website example |
| --- | --- | --- |
| **Mission** | Reusable objective, input/output schemas, required and optional actions, steps and rules. | `review-website-with-browser` |
| **Operation** | One run with specific input and a saved copy of the mission. | Review `https://defos.ai`, up to three pages. |
| **Task** | One action assignment. The controller selects an agent when dispatching it. | Check one page's links. |
| **Mission step** | Optional planning guidance that tasks can reference. It is not an action name or automatic workflow. | A declared step such as `quality-review`. |
| **Report** | The output saved when Commander explicitly completes an operation. | Findings and limitations from the website review. |

`list_missions` gives a summary; `describe_mission` supplies the full definition.
`starting_actions` guides initial assignments. Starting an operation still requires
an initial task assignment. Omit `step_id` when the mission has no declared steps.

`required_actions` declares capabilities needed before dispatch. `optional_actions`
declares useful choices that do not block initial preflight. For example, a build
mission requires checkout and build, while publication is optional:

```json
{
  "required_actions": ["checkout-template", "build-agent"],
  "optional_actions": ["publish-agent"],
  "starting_actions": ["checkout-template"]
}
```

Required and optional names must be unique and cannot overlap. These lists are
planning guidance and readiness requirements, not an execution allowlist. When
an optional action is scheduled, normal readiness checks and mission rules apply;
its missing secrets can then block dispatch. A step requirement or `same-agent`
rule can still include an optional action in preflight before it is scheduled.

An operation keeps its mission snapshot through updates and restarts. Callers may
supply a canonical lowercase UUID as `operation_id`; an existing ID is a conflict.
Operation status is `planning`, `running`, `waiting`, `completed`, `failed`, or
`cancelled`. Task status is independent: `interrupted` means its execution outcome
is unknown. Cancelling an operation stops new dispatch; running actions may finish.

## Agents and actions

- **Agent type** identifies an implementation, such as `web-agent` or `browser-agent`. Its generated
  **agent ID** identifies one process. Several instances can share a type.
- **Action** is an executable capability with input/output schemas and
  readiness. It may use Go code, a host program or an external API.
- **Agent snapshot** reports actions, status and current `work` (action, operation,
  task and command IDs). Work clears when execution ends. Stale snapshots describe
  last known state; tasks and execution records hold history.
- **Inventory** is the controller's persisted view of those snapshots and heartbeats.

Missions refer to action names. MCP **tools** are client-facing endpoints such as
`assign_task`; they are distinct from agent actions such as `check-links`.

## Discovery and preflight

**Discovery runs on each agent:** actions check their environment and report
`ready` or `unavailable`, with a reason and structured `missing_requirements`.
Requirements identify executables (`kind: executable`), host configuration keys
(`kind: configuration`), or managed secrets (`kind: secret`); values never appear
in them. A configuration requirement's `sensitive: true` flag marks sensitive host
configuration, which is still supplied on the host. Discovery does not install
dependencies or configure credentials.

An action's `required_secrets` lists names it uses even when ready. Each secret
belongs to an agent type and is identified by that type and its name.
Configured agents receive encrypted values through the controller and expose
them to declared actions through `action.Secret(ctx, name)`. Missing or expired
values block those actions. See [secret delivery](../framework/secrets/README.md#agent-requirements).

**Preflight runs on the controller:** it checks the mission's and steps' required
actions and queued optional actions against live inventory. Optional actions leave
this coverage after dispatch or completion, unless another requirement or placement
rule includes them. Different agents can satisfy different requirements.
The [`same-agent` rule](#task-affinity) restricts its grouped actions to one agent.
A busy agent can provide coverage, but dispatch still waits for capacity.

Missing coverage blocks new dispatch. The operation is `waiting` unless a task
is still running. Tasks can be queued; updated inventory refreshes preflight and
lets them resume once ready. Already dispatched tasks continue. For missing managed
secrets, preflight includes the candidate's `agent_type` and entries with
`kind: secret` in `missing_requirements`. Only live candidates report actionable
requirements; offline candidates report their unavailability reason.
Admin and Playground guide the operator to **Secrets**; never enter values in chat.
Saving a value starts delivery, and readiness updates after agent installation.
Other missing dependencies still need host configuration. Background secret
refresh requests alone do not mean an operation is blocked.

An action can also discover a need during execution and call
`action.RequestSecret(ctx, name)`. Its agent reports the same requirement shape
on the current work, leaving the action's general readiness unchanged. API/MCP
task views include this evidence only for the matching live, running execution.
Playground and Admin reuse their secret entry flow. These waits keep the existing
task deadline; they do not durably pause execution or cause automatic retries.

## Next actions and continuations

An action's optional `next_actions` proposes follow-up work as `action_name`,
`input`, and `reason`. The controller queues valid nonempty proposals automatically
unless mission rules require a model decision. Proposals do not select agents,
and their list order does not establish dependencies.

A finished or interrupted task has a **continuation**: the decision about what
follows. Its `decision_by` is `code`, `model`, or `user`; its status is `pending`,
`waiting`, or `resolved`. One operation can mix all three decision makers.

- **Code:** the controller queues proposals or collects a group's results under mission rules.
- **Model:** Commander reviews results, selects follow-ups, or closes the branch.
- **User:** Commander asks a linked question and waits for operator guidance.

`resolve_continuation` saves a decision and its queued tasks atomically.
Its explicit `version` rejects stale decisions; refresh the task before retrying.
`actions: []` closes a branch. Children retain `parent_task_id`; the continuation
records its current group's `created_task_ids`. Finishing all tasks still requires explicit operation
completion, with no queued/running work or pending questions.

## Mission rules

Rules belong to the **mission**. Each rule's `for_actions` selects the actions whose
tasks it applies to; one entry can cover several actions with the same settings.
For example, the website mission collects discovery's
generated checks before asking Commander to review their results:

```json
{
  "rules": {
    "aggregate-results": {
      "for_actions": ["discover-pages"]
    },
    "decide-next-actions": {
      "for_actions": ["check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"],
      "params": {"decision_by": "model"}
    }
  }
}
```

| Rule | Behavior |
| --- | --- |
| `aggregate-results` | Wait for all direct children generated by one execution before applying their rules. A group requiring review exposes one model decision. |
| `decide-next-actions` with `decision_by: "model"` | Ask Commander to decide follow-up work instead of automatically executing proposals. |
| `same-agent` | Keep the listed actions on the same agent instance within an operation. |

Without aggregation, children continue independently. Valid proposals run
automatically by default; failures, missing or invalid proposals return to the
model. Rules do not invent follow-up inputs, merge outputs or wait for recursive
descendants. They apply independently of their order in JSON. Changes affect new
operations, which snapshot the mission when started.

A custom controller can [select rules per operation](../apps/controllers/README.md#rule-selection)
from its mission and input. The selected set replaces the rules in that operation's
saved mission without changing the catalog. Selection happens once at start;
recovery keeps the saved rules.

See the complete [website missions](../apps/controllers/default/data/default-missions.json),
[authoring follow-ups](../apps/agents/README.md#propose-follow-up-work), and
[loading missions](../apps/controllers/README.md#quick-start).

### Task affinity

Use `same-agent` when later actions need state held by the agent that did earlier
work, such as a checked-out source tree:

```json
{
  "rules": {
    "same-agent": {
      "for_actions": ["checkout-template", "build-agent"]
    }
  }
}
```

The first dispatched matching task binds the operation's one affinity group.
Before binding, candidates must advertise every grouped action; current readiness
and capacity still govern dispatch. Later matching tasks, including proposed
follow-ups, use that same agent ID. Other operations can choose another instance;
actions outside the group use normal agent selection.

A busy owner leaves tasks queued. An unavailable owner blocks preflight, which
gates all new dispatch for the operation, including actions outside the group.
Grouped actions participate in preflight even when listed in `optional_actions`
or omitted from both action lists. Keep publication outside this group when it
uses a saved source artifact and can run on another builder.
Work resumes when the saved owner is ready and has capacity; there is no fallback
to another instance. Failed, interrupted and cancelled dispatched tasks retain the
binding. Restart recovery uses saved rules and task assignments without rebinding
or replaying work, including when a custom `RuleSelector` supplied the rule.

Affinity does not serialize tasks, create dependencies or restore lost local
files. Grouped actions can run concurrently up to the agent's capacity; use
follow-up proposals to express execution order. API/MCP operation details expose
the group's actions and bound agent under `affinity`; preflight explains readiness
blockers. Admin shows the bound agent on queued tasks.

## Questions

A **question** requests operator guidance when Commander cannot decide. It can
pause new work for the operation or a step and link to a continuation. Running
tasks continue. Questions cannot be added to a finished operation.

Questions have `pending`, `answered`, or `expired` status, up to four options and
free-text answers. The default deadline is two minutes; `timeout_seconds` overrides
it. An answer or expiration returns a linked decision to the model; neither
executes an action. Expiration records no answer and is not approval.

MCP provides `last_question` and `remaining_questions`. Playground shows one
question at a time with keyboard selection. Question history stays in the operation.

## Results and evidence

| Concept | Stored information |
| --- | --- |
| **Action output** | Structured result and artifact references. A failed command carries an error instead. |
| **Artifact** | Database metadata for produced evidence, including its owner and file descriptors. |
| **Artifact file** | Bytes in file storage: screenshots, downloads or detailed JSON. Descriptors include name, media type, size and checksum. |
| **Execution** | Durable command reservation, optional checkpoints and received outcome. A reservation does not prove execution happened. |
| **Checkpoint** | A named, immutable JSON fact inside one execution, such as a selected package version or launch intent. |
| **Operation event** | An ordered record of progress, available through cursor-based queries. |

Artifacts and reports can be read independently of the model conversation.
See [providers](../providers/README.md) for persistence implementations and
[recovery](overview.md#state-and-restart-recovery) for uncertain outcomes.

Actions running through `Agent.Serve` use
[`action.LoadCheckpoint` and `action.SaveCheckpoint`](../framework/agent/action/checkpoints.go).
The controller verifies the saved execution claim and persists checkpoints through
[`ExecutionStore`](../framework/domain/executions/execution_store.go) in FS or
MongoDB before acknowledging a save. Each execution can contain
up to 32 checkpoints, each a JSON object of at most 16 KiB, including JSON
escaping when encoded for storage or transport. An identical retry
confirms the saved value; different data under the same name is rejected. New
checkpoints cannot be added after completion. Checkpoints contain no secrets or
file contents, and do not grant permission to repeat an external side effect.

```go
data, found, err := action.LoadCheckpoint(ctx, "intent")
if err != nil {
    return err
}
if !found {
    data = json.RawMessage(`{"package":"github.com/example/agent@v1.2.3"}`)
    if err := action.SaveCheckpoint(ctx, "intent", data); err != nil {
        return err
    }
}
// Use the confirmed intent; reconcile any external effect before repeating it.
```

The runtime retries lost checkpoint replies without rerunning the action. Waits
end when the caller, action or agent host stops; cancellation does not prove a
write was rejected. Standalone `Agent.Work` has no checkpoint provider.

## Messages and durable execution

A **command** requests work; an **event** reports something that happened. A
**message** is their bus envelope, with identity and correlation. A **result receipt**
confirms that the controller persisted an agent outcome. It is separate from the
bus acknowledgement that a message was processed.

See [message contracts](../framework/comms/messages/README.md) for routes and
[message handling](../framework/comms/README.md) for acknowledgement behavior.
