# Domain models

Use this reference to choose a model and find its consumers before changing a
contract. Links lead to the definitions and store interfaces; field lists and
allowed status values stay in source. For workflow examples, read
[missions and rules](../../docs/abstractions.md).

A mission describes reusable work. An operation runs that mission, tasks assign
actions, and executions retain the exact dispatched commands and outcomes.
The [Agent], [Controller], [Runner], [Queries], [Secret service] and
[Session coordinator] implement runtime behavior around these models.
[Adapters] expose them to clients; [Providers] implement storage contracts.

## Actions

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`Definition`](actions/definition.go) | Describes schemas, required secret names and session mode before an executor or readiness exists. | Agent action constructors, mission validation, [Builder]. |
| [`Action`](actions/action.go) | Adds one instance's current capability readiness and missing requirements to its definition. | Agent discovery, Controller inventory, Runner preflight, Adapters. |
| [`Requirement`](actions/requirement.go) | Names a missing executable, configuration item or secret without exposing its value. | Discovery, agent secret waits, operation readiness queries, Adapters. |
| [`ActionCall`](actions/action_call.go) | Carries an action assignment, input, work identity and optional session binding to an agent. | Controller dispatch, execution persistence/recovery, agent command runtime. |
| [`ActionOutput`](actions/action_output.go) | Returns structured output, artifact metadata, follow-up proposals and an optional session observation. | Agent result publishing, Controller acceptance, Runner task completion, Adapters. |
| [`NextAction`](actions/next_action.go) | Proposes follow-up work for mission rules or a model to decide; a proposal does not dispatch itself. | Agent actions, Runner admission and rule application, model continuation tools. |

## Agents

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`AgentSnapshot`](agents/agent_snapshot.go) | Advertises an instance's identity, responsibilities, capabilities, sessions and current work. | Agent state publishing, Controller registration and heartbeat consumers. |
| [`Agent`](agents/agent.go) | Retains received inventory plus controller-owned observation times, heartbeat lease and derived freshness. | Controller selection, Runner preflight, Session coordinator, Queries, Adapters. |
| [`Work`](agents/agent_snapshot.go) | Correlates current action activity and managed-secret waits with a saved assignment. | Agent runtime, Controller observations, operation requirement queries. |
| [`DiscoveryScope`](agents/discovery_scope.go) | Constrains executable search locations and named services during discovery. | Discovery commands, agent environments, inventory snapshots. |
| [`ServerInfo`](agents/server_info.go) | Identifies the agent's host or container network namespace. | Host discovery, registration, inventory queries and Adapters. |

## Missions

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`Mission`](missions/mission.go) | Defines reusable objectives, action coverage, schemas and rules; each operation saves its accepted definition. | [Admin] authoring/seeding, Adapters, Runner start and rule selection. |
| [`CompletionPolicy`](missions/completion_policy.go) | Declares who may complete the whole mission; an operator policy requires the operator submission channel. Each operation retains its policy. | Mission validation, Adapters, Runner and atomic completion in operation stores. |
| [`RuleSet`, `Rule`, `RuleParams`](missions/rule.go) | Express aggregation, model review and same-agent policy declaratively. | Runner rule selection, operation rule application, mission authoring. |

## Operations

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`Operation`](operations/operation.go), [`CompletionOrigin`](operations/operation_completion.go) | Own one mission run: its input, tasks, questions, event history, completion draft for operator review, final output/report reference and accepted completion channel. | Runner scheduling/recovery, Controller question consumer, Queries, Adapters. |
| [`Task`](operations/task.go) | Retains one action assignment, parentage, selected process, deadline, outcome and continuation. | Runner dispatch/results, Session coordinator, operation queries and Adapters. |
| [`TaskDispatch`](operations/task_dispatch.go) | Supplies the process and timestamps for atomically claiming a queued task. | Runner, Session coordinator, OperationStore implementations. |
| [`TaskResult`](operations/task_result.go) | Supplies an execution outcome for the atomic task/continuation/event transition. | Runner result reconciliation, OperationStore implementations. |
| [`Continuation`](operations/continuation.go) | Records who decides the next work, its decision version, proposals and accepted direct children. | Mission rule application, Runner continuation commands, model clients through Adapters. |
| [`Question`](operations/question.go) | Saves an operator-first decision, its answer and origin, including questions from running actions. | Model `ask_question`, `action.Question`, Controller question consumer, Runner answers, Queries and Adapters. |
| [`Event`](operations/event.go) | Retains ordered evidence of operation state changes for review and incremental reads. | Operation transitions, event queries, Adapters and watching clients. |
| [`Preflight`, `ActionReadiness`, `AgentReadiness`](operations/preflight.go) | Explain whether current agents cover required actions and what prevents dispatch. | Runner assessment, OperationStore, Queries and Adapters. |
| [`TaskPlacement`](operations/session_placement.go) | Explains an exact-owner/session placement constraint or missing requirement for a task. | Session placement assessment, Runner selection, preflight views. |
| [`Progress`](operations/progress.go) | Derives task counts, pending decisions/questions and settlement from saved state. | Queries, Adapters, Admin UI and Playground through adapter responses. |
| [`Affinity`](operations/affinity.go) | Projects the same-agent rule and retained assignments into a bound process or a diagnostic. | Runner placement checks, Queries and Adapters. |
| [`RuleContext`, `RuleEffects`, `RuleFollowUp`](operations/rule_applier.go) | Separate the inputs and effects of pure mission-rule evaluation from dispatch and persistence. | `RuleApplier`, its `DefaultRuleApplier`, operation result/continuation transitions. |

Questions stay here because a model can ask before any execution exists or while
reviewing a finished task. An action-originated question adds command/key identity
and waits within that execution; both paths share the same saved answers and
operator-first policy. See [execution questions](../comms/messages/README.md#execution-questions).

## Executions

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`Execution`](executions/execution.go) | Reserves an exact command before publication and retains its immutable outcome independently of message delivery. | Controller dispatch/results, Runner recovery, Session coordinator, ExecutionStore implementations. |
| [Checkpoint entries](executions/checkpoint.go) | Save immutable named JSON facts within an execution to reconcile external effects. They are entries in `Execution.Checkpoints`, not a separate entity. | Agent checkpoint helpers, Controller checkpoint consumer, runtime launch reconciliation. |
| [`Log`](executions/log.go) | Retains a progress observation without changing the execution outcome or authorizing a retry. | Agent logging, Controller log consumer, Queries and Adapters. |
| [`LogQuery`](executions/log_query.go) | Defines intersecting filters and cursor/order/limit for append-ordered observations. | Operation log queries, Adapters, ExecutionStore implementations. |
| [`CommandError`, `CommandFailure`](executions/errors.go) | Distinguish a reported command failure from an unknown remote outcome after transport loss. | Result decoding, Controller error handling, Runner reconciliation, Adapters. |

## Artifacts and reports

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`artifacts.Artifact`](artifacts/artifact.go) | Groups accepted output-file metadata under the producing agent/action/command. | Agent result publishing, Controller acceptance, artifact/task queries, Adapters. |
| [`artifacts.Owner`](artifacts/artifact.go) | Validates the production identity shared by an artifact's files. | Artifact validation, agent result construction, Controller acceptance. |
| [`artifacts.ArtifactFile`](artifacts/artifact_file.go) | Describes a stored file's identity, media type, byte size and checksum without carrying its bytes. | Artifact stores, Agent uploads, Controller metadata acceptance, file queries. |
| [`artifacts.FileSource`](artifacts/artifact_file.go) | Supplies local bytes or a local path for upload; it never belongs in a broker message. | Agent upload runtime, ArtifactFileStore implementations. |
| [`reports.Report`](reports/report.go) | Retains an operation's final structured output for later review. | Runner completion, Queries, Adapters, ReportStore implementations. |

Agents upload file bytes; controllers accept artifact metadata. A report records
the accepted final output and can include limitations; completion does not itself
certify that every objective was achieved.

## Secrets

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`Key`](secrets/key.go) | Identifies one deployment-wide secret by name, independently of agent type. | Secret service, agent requests, Controller distribution, Admin/Adapters. |
| [`Metadata`](secrets/secret.go) | Exposes identity, version and validity without revealing the credential. | Secret service, Controller distribution, agent cache, Admin/Adapters. |
| [`Secret`, `EncryptedValue`](secrets/secret.go) | Retain authenticated metadata and encrypted bytes for storage. | Secret service, [Cryptography], Controller decryptor, secret-store providers. |
| [`Request`, `Fulfillment`](secrets/request.go) | Track one recipient's bounded request and the exact secret/version/delivery acknowledged for it. | Agent synchronization, Controller distribution, request queries and Adapters. |
| [`Delivery`](secrets/delivery.go) | Reserves the exact encrypted snapshot and message identity before delivery; it does not prove publication or installation. | Controller distribution, RequestStore implementations. |
| [`Installation`](secrets/delivery.go) | Records the recipient's acknowledgement of a specific delivery and secret version. | Agent acknowledgement publishing, Controller validation, RequestStore. |
| [`RequestRecord`](secrets/request_record.go) | Keeps the request, reserved delivery and acknowledgement together for atomic lifecycle transitions. | Controller distribution and recovery, RequestStore implementations. |

The controller decrypts a reserved delivery before sending the requested value.
Agents receive values and metadata, not the encrypted storage record or its key.
Secret values stay outside operation questions, logs and model context.

## Sessions

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`Reference`](sessions/session.go), [`ExecutionRef`](sessions/lifecycle.go) | Identify the resource, scope, exact owner process and original creating execution. | Session coordinator, agent resource providers, bindings and controls. |
| [`Session`](sessions/session.go) | Presents an observed/query view of the resource's connectivity, revision and lifecycle disposition. | Agent observations, Session coordinator, Queries and Adapters. |
| [`Intent`](sessions/lifecycle.go) | Retains the requested create/reuse/use behavior resolved from an action contract and target. | Task admission, Runner placement/preflight, Session coordinator. |
| [`Binding`](sessions/lifecycle.go) | Places the exact owner, resource, creation identity and mode in the saved dispatch. | Session coordinator, Controller replay, agent session context and resource providers. |
| [`CreationClaim`](sessions/lifecycle.go) | Registers an unstarted identity so a prior close can prevent a delayed start. | Agent session preparation/control, `Provider`. |
| [`Scope`](sessions/lifecycle.go) | Atomically groups a scope's current reservation and retained resource identities. | Session coordinator, session-store providers. |
| [`Record`, `Release`](sessions/lifecycle.go) | Retain durable ownership, lifecycle disposition, observations, pending control and release evidence. | Session coordinator, session-store providers, Queries. |
| [`Change`](sessions/lifecycle.go) | Describes a pure lifecycle transition applied to a saved scope. | Session coordinator and session-store providers. |
| [`Control`](sessions/session.go) | Correlates inspection, verification, closure or abandonment with an exact resource owner. | Admin/Adapters, Session coordinator, agent resource providers. |
| [`Query`](sessions/query.go) | Defines intersecting session-ID, scope and exact-owner criteria for retained records. | Queries, Adapters, session-query providers. |
| [`Provider`](sessions/session.go) | Defines how an agent-owned resource prepares, observes and controls its lifecycle. | Agent session runtime and concrete resource adapters. |

An operation finishing does not close its sessions. `Session` is a public view;
`Scope` and `Record` own durable reservation and lifecycle state. See the
[runtime-session contract](../../docs/runtime-sessions.md).

## Controllers

| Model | Why it exists | Consumers |
| --- | --- | --- |
| [`Controller`, `ServerInfo`](controllers/controller.go) | Register runtime status, selectors, version, host identity and heartbeat freshness. | Default controller host, HTTP controller queries, Admin UI through the API, ControllerStore providers. |

Registration describes live instances; it does not establish distributed scheduling
ownership. Only one active controller per database is currently supported.

## Store contracts and shared helpers

| Contract | Responsibility |
| --- | --- |
| [`ActionStore`](actions/action_store.go), [`AgentStore`](agents/agent_store.go) | Persist advertised capabilities and received agent inventory for the controller. |
| [`MissionStore`](missions/mission_store.go) | Reserve mission names and retain reusable definitions. |
| [`OperationStore`](operations/operation_store.go) | Commit operation/task/question/continuation/event transitions atomically. |
| [`ExecutionStore`](executions/execution_store.go) | Reserve commands, accept outcomes, append checkpoints and retain/query logs. |
| [`ArtifactStore`](artifacts/artifact_store.go), [`ArtifactFileStore`](artifacts/artifact_file_store.go) | Keep accepted metadata separate from uploaded file bytes. |
| [`ReportStore`](reports/report_store.go) | Persist and retrieve final reports. |
| [`MetadataStore`, `SecretStore`](secrets/secret_store.go), [`RequestStore`](secrets/request_store.go) | Separate metadata-only access, encrypted credentials and delivery lifecycle persistence. |
| [`Store`](sessions/lifecycle.go), [`QueryStore`](sessions/query.go) | Apply session-scope transitions atomically and query retained resource views. |
| [`ControllerStore`](controllers/controller_store.go) | Retain heartbeat-ordered controller registrations. |

Providers implement these contracts; host factories inject them into runtimes and
query services. Store comments specify concurrency, replay and uncertain-write
behavior. Shared [validation](validation.go), [errors](errors.go) and
[pagination](pagination.go) support the packages without introducing another
domain entity. Change guidance lives in [AGENTS.md](AGENTS.md).

[Agent]: ../agent/
[Controller]: ../controller/
[Runner]: ../controller/runner/
[Queries]: ../queries/
[Secret service]: ../secrets/
[Session coordinator]: ../sessions/
[Adapters]: ../../apps/adapters/README.md
[Providers]: ../../providers/README.md
[Builder]: ../../apps/agents/builder/README.md
[Admin]: ../../apps/admin/README.md
[Cryptography]: ../crypto/README.md
