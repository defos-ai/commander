# Missions and rules

Use a mission to describe a repeatable goal, the actions it needs and how to
handle follow-up work. Start in **Admin → Missions → Create mission**, or use
the [HTTP API](../apps/adapters/api/README.md#create-and-update-missions) or MCP.

## Mission, operation, and task

| Concept | Example |
| --- | --- |
| **Mission**: reusable objectives, action requirements and rules | Review a website |
| **Operation**: one run of a mission | Review `https://defos.ai` |
| **Task**: one action assignment | Check links on one page |
| **Report**: the final saved output | Findings and recommended fixes |

### Create a mission

1. Write `objectives` as a list of clear goals; every item applies.
2. Select required actions. Add useful extras as `optional_actions` so they do not
   block the initial readiness check.
3. Choose `starting_actions` from the required actions and define input/output schemas.
4. Add rules where you need aggregation, model review or the same agent instance.
5. Save the mission. In Admin, open its **JSON** view to inspect the definition.

For example, a build mission can require checkout and build while leaving sharing optional:

```json
{
  "name": "build-agent",
  "objectives": ["Build and verify the requested agent."],
  "required_actions": ["checkout-template", "build-agent"],
  "optional_actions": ["publish-agent", "release-agent"],
  "starting_actions": ["checkout-template"],
  "input_schema": {"type": "object"},
  "output_schema": {"type": "object"},
  "rules": {
    "same-agent": {"for_actions": ["checkout-template", "build-agent"]}
  }
}
```

Start an operation, then assign its first task; `starting_actions` does not start
work automatically. Editing a mission affects new operations, not existing runs.
See the complete [bundled missions](../apps/admin/missions/default-missions.json).

### Define an action before an agent exists

Add an `action_definitions` entry, or use the action dialog on the creation page:

```json
{
  "name": "inspect-service",
  "description": "Inspect the requested service.",
  "input_schema": {"type": "object"},
  "output_schema": {"type": "object"},
  "required_secrets": ["SERVICE_TOKEN"]
}
```

Reference its name in the mission's action lists. This describes the contract a
builder should implement; it does not install an agent or make the action ready.
Use secret **names**, never values, in definitions.

## Agents and actions

An **agent type** identifies an implementation, such as `browser-agent`.
An **agent ID** identifies one running instance. Its **actions** are executable
capabilities with input/output schemas and readiness checks.

Select actions by capability; the controller chooses an instance. MCP tools such
as `assign_task` submit work to the controller rather than executing agent code directly.

## Discovery and preflight

Before dispatch, Commander checks that live agents can run the required actions.
If an operation is waiting, open its preflight details in Admin:

| Missing requirement | Next step |
| --- | --- |
| Agent or action | Install a suitable agent and check its readiness |
| Executable or configuration | Install the dependency or configure that agent's host |
| Managed secret | Use the Playground secret form or **Admin → Agents → Manage secrets** |

Never paste secret values into chat. Saving a secret resumes eligible work once
it reaches the agent. An action can also request a secret while running, using
the same form; its task deadline still applies. See [secret setup](../framework/secrets/README.md).

## Next actions and continuations

Agents can propose multiple `next_actions`. By default, the controller queues
valid proposals automatically. Use mission rules to request a model decision instead.
Proposal order does not create dependencies.

A **continuation** is the decision about what follows a task:

| Decision maker | What happens |
| --- | --- |
| Code | Follow mission rules and valid proposals |
| Model | Commander reviews results and chooses follow-ups |
| User | Commander asks for your guidance |

When reviewing through MCP, `resolve_continuation` accepts follow-up actions;
`actions: []` closes the branch. Refresh the task before retrying a stale decision.
Once work and questions are resolved, explicitly complete the operation with its report.

## Mission rules

Choose the action names with `for_actions`:

```json
{
  "rules": {
    "aggregate-results": {
      "for_actions": ["discover-pages"]
    },
    "decide-next-actions": {
      "for_actions": ["check-links", "capture-page"],
      "params": {"decision_by": "model"}
    }
  }
}
```

| Rule | Use it when… |
| --- | --- |
| `aggregate-results` | One action generates several checks and you want their direct results collected before deciding what follows |
| `decide-next-actions` | Commander should review results instead of automatically running proposed follow-ups |
| `same-agent` | Later actions need local state held by the same agent |

Aggregation waits for all **direct children** from one execution, not their recursive
follow-ups. Without it, each child continues independently. Rules do not merge
outputs or invent action inputs.

### Task affinity

Use `same-agent` for checkout and build actions that share a local workspace:

```json
{"same-agent": {"for_actions": ["checkout-template", "build-agent"]}}
```

The first matching dispatch binds that group to one agent for the operation.
If it becomes unavailable, work waits; it does not move to another instance.
Affinity does not create execution order or prevent concurrency: propose dependent
actions only after their prerequisites finish. Leave artifact-based work outside
the group when any compatible agent can do it.

For custom selection, see [controller rules](../apps/controllers/README.md#rule-selection).

## Questions

Commander can ask a question when a decision needs your input. Answer in
Playground or Admin. A question can block new work; running tasks continue.
Operators can answer immediately. After the waiting window (120 seconds by
default), the model may answer too. Questions stay open until answered; time
passing alone is not approval. Answers and their origins stay with the operation.

## Results and evidence

Open a completed operation in Admin to inspect task outputs, reports and artifacts.
Screenshots and downloads are artifact files; their internal references are not
public browser URLs.

For agent authors, use [artifact helpers](../framework/agent/action/artifact.go)
to save files and [execution checkpoints](../framework/agent/action/checkpoints.go)
to record a small immutable fact before an external side effect. Checkpoints do
not authorize repeating that effect after an uncertain result.
See [restart recovery](overview.md#state-and-restart-recovery).

## Messages and durable execution

A bus acknowledgement confirms message handling; a result receipt confirms the
controller saved an outcome. When a reply is lost, inspect saved progress before
retrying. See the [messaging guide](../framework/comms/README.md) when implementing a host.

Agent-owned resources that survive an action use the
[runtime session contract](runtime-sessions.md), including explicit owner routing
and an independent control channel.
