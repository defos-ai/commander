# Core concepts

The [architecture overview](overview.md) explains Commander → Controller → Agent.
This guide defines the data and decisions passed between them.

## Mission, operation, and task

| Concept | Meaning | Website example |
| --- | --- | --- |
| **Mission** | Reusable objective, input/output schemas, required actions, optional steps and automation rules. | `review-website` |
| **Operation** | One run with specific input and a saved copy of the mission. | Review `https://defos.ai`, up to three pages. |
| **Task** | One action assignment. The controller selects an agent when dispatching it. | Check one page's links. |
| **Mission step** | Optional planning guidance that tasks can reference. It is not an action name or automatic workflow. | A declared step such as `quality-review`. |
| **Report** | The output saved when Commander explicitly completes an operation. | Findings and limitations from the website review. |

`list_missions` gives a summary; `describe_mission` supplies the full definition.
`starting_actions` guides initial assignments. Starting an operation still requires
an initial task assignment. Omit `step_id` when the mission has no declared steps.

An operation keeps its mission snapshot through updates and restarts. Callers may
supply a canonical lowercase UUID as `operation_id`; an existing ID is a conflict.
Operation status is `planning`, `running`, `waiting`, `completed`, `failed`, or
`cancelled`. Task status is independent: `interrupted` means its execution outcome
is unknown. Cancelling an operation stops new dispatch; running actions may finish.

## Agents and actions

- **Agent type** identifies an implementation, such as `web-quality`. Its generated
  **agent ID** identifies one process. Several instances can share a type.
- **Action** is an executable capability with input/output schemas, permissions and
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
Requirements describe executables, configuration keys or permissions; secret values
must never appear in them. Discovery does not install dependencies or grant permissions.

**Preflight runs on the controller:** it checks the mission's and steps' required
actions against live inventory. Different agents can satisfy different requirements.
A busy agent can provide coverage, but dispatch still waits for capacity.

Missing coverage blocks new dispatch. The operation is `waiting` unless a task
is still running. Tasks can be queued; updated inventory refreshes preflight and
lets them resume once ready. Already dispatched tasks continue. MCP exposes the missing requirements so Commander can explain the
blocker or ask the operator. Installation and credential configuration remain host
operations, followed by rediscovery.

## Next actions and continuations

An action's optional `next_actions` proposes follow-up work as `action_name`,
`input`, and `reason`. Proposals neither select an agent nor authorize execution.
Their list order does not establish dependencies.

A finished or interrupted task has a **continuation**: the decision about what
follows. Its `decision_by` is `code`, `model`, or `user`; its status is `pending`,
`waiting`, or `resolved`. One operation can mix all three decision makers.

- **Code:** mission rules authorize proposed actions and the controller queues them.
- **Model:** Commander reviews results, selects follow-ups, or closes the branch.
- **User:** Commander asks a linked question and waits for operator guidance.

`resolve_continuation` saves a decision and its queued tasks atomically.
Its explicit `version` rejects stale decisions; refresh the task before retrying.
`actions: []` closes a branch. Children record `parent_task_id`; the continuation
records `created_task_ids`. Finishing all tasks still requires explicit operation
completion, with no queued/running work or pending questions.

## Mission automation

Rules belong to the **mission**, keyed by the action that finished. For example,
the website mission follows discovery with its five proposed checks:

```json
{
  "automations": {
    "discover-pages": {
      "run": ["check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"],
      "continue": "when_all"
    },
    "check-links": {"decide": "model"},
    "check-accessibility": {"decide": "model"},
    "capture-page": {"decide": "model"},
    "check-seo": {"decide": "model"},
    "detect-technologies": {"decide": "model"}
  }
}
```

| Rule | Behavior |
| --- | --- |
| `run` | Every proposed follow-up must be listed. One unapproved proposal returns the whole decision to the model. |
| `continue: "when_each"` | Each generated child continues independently; the default. |
| `continue: "when_all"` | Collect all children before applying their rules; if the group cannot continue automatically, expose one model decision. |
| `decide: "model"` | Return the decision to Commander. |

Failures, absent proposals, unconfigured transitions and proposals outside a rule
return to the model. Rules do not invent follow-up inputs. Changes affect new
operations, which snapshot the mission when started.

See the complete [website workflow](../apps/agents/web-quality/README.md#workflow),
[authoring follow-ups](../apps/agents/README.md#propose-follow-up-work), and
[loading missions](../apps/controllers/README.md#quick-start).

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
| **Execution** | Durable command reservation and received outcome. A reservation does not prove execution happened. |
| **Operation event** | An ordered record of progress, available through cursor-based queries. |

Artifacts and reports can be read independently of the model conversation.
See [providers](../providers/README.md) for persistence implementations and
[recovery](overview.md#state-and-restart-recovery) for uncertain outcomes.

## Messages and durable execution

A **command** requests work; an **event** reports something that happened. A
**message** is their bus envelope, with identity and correlation. A **result receipt**
confirms that the controller persisted an agent outcome. It is separate from the
bus acknowledgement that a message was processed.

See [message contracts](../framework/comms/messages/README.md) for routes and
[message handling](../framework/comms/README.md) for acknowledgement behavior.
