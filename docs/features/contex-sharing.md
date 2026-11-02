# Context sharing

**Status: mission policy and agent reception are available; controller forwarding
is planned.**

Let agents share findings within an operation while work is running:

1. Cybersecurity actions return vulnerability findings and evidence.
2. The controller saves their results and accepted artifact metadata normally,
   then forwards permitted data to an analyzing agent.
3. The analyzer aggregates the updates in memory and analyzes vulnerabilities as
   producer actions finish, while other tasks in the operation can continue.

## Mission policy

The mission names producing and receiving actions. The controller selects the
receiving agent and its active task execution. Configuration:

```json
{
  "context": {
    "sharing": [
      {
        "from_actions": ["find-vulnerabilities"],
        "to_actions": ["analyze-vulnerabilities"],
        "include": ["results", "artifacts"]
      }
    ]
  }
}
```

Each route requires producing actions, receiving actions and at least one of
`results`, `artifacts` or `decisions`. Omit `context` or leave `sharing` empty to
declare no routes. Each operation keeps its mission's policy snapshot; editing
the mission affects future operations. Saving policy does not enable delivery yet.

Results, decisions and artifact metadata are saved normally, whether sharing is
enabled or not. Sharing reuses those records and adds forwarding to matching
receiving tasks in the same operation. Relevant non-secret answered questions can
also be included, retaining whether an operator or model answered and the
decision's original scope.

## Delivery

```mermaid
sequenceDiagram
    participant Security as Cybersecurity agent
    participant Controller
    participant Database
    participant Storage as Artifact storage
    participant Analyzer as Analyzing agent with an active task

    Security->>Storage: Upload evidence files
    Storage-->>Security: File references
    Security->>Controller: Completed action result and artifact references
    Controller->>Database: Save result and accepted artifact metadata
    Database-->>Controller: Saved
    Note over Controller: Check mission sharing policy and receiving task
    Controller->>Analyzer: Push context update via agent event inbox
    Note over Analyzer: Aggregate and analyze in memory
```

**Database** means Commander's configured database provider. Artifact files live
separately in S3-compatible or filesystem storage; messages carry finding data and
file references rather than the file contents.

After accepting and saving evidence, the controller pushes permitted data to the
receiving agent's event inbox through the existing messaging bus. The framework
delivers it to the running action without polling. Start the analyzing task before
producer actions finish.

Updates retain existing task, command, artifact or question IDs and source
information so receivers can ignore duplicate deliveries. Forwarding uses the
existing saved records without another persistence layer.

A running action uses [`action.ReceiveContext(ctx)`](../../framework/agent/action/context.go)
to wait for the next pushed update. Reception ends with the execution or its
deadline. A full buffer reports an error so the analyzer can report incomplete
context rather than silently continue.

## Agent-owned memory

Receiving agents own aggregation and state. Start with process memory, kept
separate for each operation. The controller routes evidence without managing
the analyzer's accumulated memory.

The analyzer runs as a normal controller-assigned task. Sharing does not start
arbitrary work, extend task deadlines, or deliver updates to completed executions.
Managed secrets, execution checkpoints, raw execution logs and private chat
history stay outside automatic sharing.

The first scope is live delivery and in-memory aggregation. An analyzer restart
loses its local memory; replay and recovery are later work. Saved evidence remains
available, but persistence alone does not confirm delivery.

Controller filtering alone cannot isolate agents that already have broad access
to shared artifact storage. Strong isolation needs scoped storage access or
controller-mediated artifact retrieval.
