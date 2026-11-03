# Core concepts

Use the [feature guides](features/README.md) for workflows and examples.
Commander separates reusable goals, individual runs and the agents doing the work:

| Concept | Meaning |
| --- | --- |
| **Mission** | A reusable definition of objectives, action requirements, schemas and policies. |
| **Policy** | A mission setting for placement, continuation, completion or data sharing; each operation retains its accepted definition. |
| **Operation** | One run of a mission, with its own input, tasks, questions, evidence and final report. |
| **Task** | An action assignment within an operation; the controller chooses an eligible agent. |
| **Execution** | The saved command and any accepted outcome of work on an agent; an unknown outcome does not authorize another attempt. |
| **Agent type / agent ID** | An implementation and one exact running instance of it. |
| **Action** | An executable capability with input/output schemas and readiness requirements. A desired action definition alone supplies no executor. |
| **Continuation** | The decision about what follows a task or a group of direct child results. |
| **Question** | A saved request for an answer, raised by a decision maker or a running action. |
| **Log** | An execution observation used to follow progress; it does not change a task's deadline. |
| **Checkpoint** | An immutable execution fact used to reconcile external effects, not permission to repeat them. |
| **Artifact** | Accepted metadata for uploaded evidence or files, such as a screenshot or generated source archive. |
| **Report** | The accepted final output of an operation; it can document limitations and unknown outcomes. |
| **Session** | A resource retained across actions or operations by one exact agent process. |
| **Scope** | The context in which one current session reservation is held until release. |
| **Managed secret** | A shared credential identified by name within a Commander deployment. |

A decision maker chooses what to do, the controller chooses who executes, and
agents perform actions. Models and operators use the adapters to submit work;
clients do not dispatch directly to agents. See the [architecture](overview.md).

For implementation responsibilities and consumers of each model, see the
[domain reference](../framework/domain/README.md).
