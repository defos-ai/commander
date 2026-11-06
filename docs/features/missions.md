# Missions

Open **Admin → Missions → Create mission** to define a reusable goal. You can
also use [MCP](../../apps/adapters/README.md#create-and-update-missions), the
[HTTP API](../../apps/adapters/api/README.md#create-and-update-missions), or the
[Admin CLI](../../apps/admin/README.md#missions).

Saving a mission does not install agents or start work. Start an
[operation](operations.md) when you are ready to run it.

## Create a mission

Write `objectives` as an ordered list of clear goals. Define `input_schema` for
operation input and `output_schema` for the final report; both are JSON Schema
objects. `{}` accepts any JSON value.

| Field | Use it for |
| --- | --- |
| `required_actions` | Capabilities that must be ready before dispatch |
| `optional_actions` | Useful extras checked when selected for queued work |
| `starting_actions` | Suggested first assignments, chosen from required actions |
| `action_definitions` | Desired contracts for capabilities to build later |

Required and optional lists cannot overlap. These lists describe coverage, not
an execution allowlist. `starting_actions` guides the decision maker; starting
an operation does not automatically queue those actions.

Each desired action definition needs a name, description and input/output object
schemas. Reference its name in the required or optional list. Required secret
**names** may be included; values belong in [Secrets](secrets.md).
Definitions describe what an agent should implement. They do not register an
executor, install dependencies or make an action ready.

## Example: build source for review

Save this definition, then start an operation and assign `checkout-template`
with `session_scope` set to the operation ID:

```json
{
  "name": "build-source",
  "objectives": ["Build and verify the requested agent source for operator review."],
  "required_actions": ["checkout-template", "build-agent"],
  "optional_actions": ["publish-agent", "release-agent"],
  "starting_actions": ["checkout-template"],
  "input_schema": {"type": "object"},
  "output_schema": {"type": "object"},
  "policies": {
    "continuation": {"model_review": ["build-agent"]},
    "completion": {"decision_by": "operator"}
  }
}
```

The controller selects the agent. Check its action schemas and readiness before
assigning work; mission input and an individual action's input are separate.
After checkout succeeds, assign build with its returned scope and session ID,
the requested instructions and action contracts.
See the [bundled missions](../../apps/admin/missions/default-missions.json)
for complete workflows.

## Choose policies

| Policy | Effect |
| --- | --- |
| `continuation.aggregate_results` | Collect a matching action's saved direct children before deciding what follows |
| `continuation.model_review` | Review matching results before running proposed follow-ups |
| `completion.decision_by` | Allow model completion or require explicit operator completion |
| [`sharing`](data-sharing.md) | Forward results, artifacts or decisions to active receiving actions |

All policies live under `policies`. Omitted continuation and sharing add no
constraints or routes. Omitted/null completion defaults to `model`;
an empty completion object is invalid.

### Resource ownership

Use [sessions](sessions.md) for resources retained between actions, such as
Builder workspaces or browser sessions. Pass the saved session identity explicitly;
the controller routes work to its recorded owner. Different sessions in one
operation can belong to different agents. Mission policies do not open or close resources.

### Continuation policy

By default, valid nonempty proposals continue automatically. Model review also
handles failed, cancelled or interrupted work, empty proposals and invalid proposals.
The aggregation and model-review lists may overlap. Aggregation covers only the
saved direct children of that decision, without recursively collecting descendants.
Policies do not merge outputs or invent inputs for follow-up actions.

### Completion

Omitted/model completion allows immediate completion through MCP or Admin.
Operator completion makes a model submission save a draft without a final report;
review or edit it in Admin and explicitly complete it there, or through the HTTP API.
There is no timeout or model fallback for this approval. [Questions](questions.md)
have their own waiting window and do not grant completion authority.

## Edit for future runs

Mission names are unique and cannot change. Read the saved definition, edit its
objectives, actions, schemas or policies, then save the complete replacement
under the same name. In Admin, choose **Edit mission**; MCP uses `update_mission`, and the
CLI uses `missions update`.

Every operation keeps an independent snapshot of its mission. Updates affect
future operations only. After an uncertain save, read the saved definition
before submitting again.
