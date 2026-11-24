# Working on the MCP adapter

Follow the [shared adapter guide](../AGENTS.md) for implementation contracts and
validation.

Start in [adapter.go](adapter.go) for tool registration and input decoding.
[cli/factory.go](cli/factory.go) selects providers;
[cli/http.go](cli/http.go) configures the HTTP host.

Resolve listen configuration after flag parsing: explicit `--listen-address`,
nonempty `MCP_LISTEN_ADDRESS`, `0.0.0.0:$PORT`, then `127.0.0.1:4273`. Validate
`PORT` only when selected, requiring decimal digits in the range 1–65535.
Explicit addresses retain port zero for embedded hosts. The Docker image sets
`PORT=4273` rather than a fixed command argument, so platform ports can override it.

Resolve allowed hosts after parsing: explicit `--allowed-hosts`, nonempty
`MCP_ALLOWED_HOSTS`, then `localhost,127.0.0.1,::1`, matching API's flag/environment
precedence. Preserve validation of explicit empty flags and reject URLs, ports
and wildcards. Host and Origin checks remain active alongside authentication.

Embedded hosts may supply a canonical UUID through `Options.InstanceID`; `/aboutz`
then includes `instance_id` alongside runtime/application. Standalone hosts omit
the field. Keep identity outside flags/environment and preserve public GET/HEAD
Host/Origin checks. This metadata identifies an instance; it is not authentication.

`get_agent_sources` uses the caller-owned `SourceClient` to load the fixed public
`DefaultAgentSourcesURL` on demand. Return only name, description, source and
visibility in a `sources` list, sorted public first, then by name and full source
reference. The CLI owns its anonymous client and closes idle connections. Never
fetch during startup or accept source URLs/credentials as tool input. Reads are
bounded, and remote failures stay tool-local without exposing response bodies or
connection details. Source discovery grants no permissions and stores no state.

`list_agents` and `describe_agent` preserve the optional agent `version` from
saved inventory. It identifies the agent package/build, independently of the
Commander framework and software dependency versions. Omit unreported versions;
never infer them from tags, framework versions or dependency guidance.

`stop_agent` accepts `selector` with at least one exact `id`, `type`, `version` or `hostname`;
supplied fields intersect. Reject supplied null/empty/unknown selector fields
through the shared contract, including mixtures with valid fields. Pass it to
the separately injected `AgentCommands.StopAgents`; only the controller captures
and selects the fixed match set. MCP never matches or reselects inventory.
Saved reads remain usable without this capability; the tool reports unavailable.
Bound acceptance to ten seconds and never publish directly, mutate inventory or
retry commands. Validate the controller's aggregate result and return compact
`agents` summaries plus optional per-agent `errors` and a message. Preserve an
empty agents array for no matches. `stop_requested` is accepted intent while work
drains; only `stopped` confirms shutdown. Partial errors must not become a claim
that every target succeeded. Sanitize outer uncertainty, directing readback
through `list_agents`/`describe_agent` without backend details. Deployment resources
and restart policies remain host responsibilities.

`assign_task.selector` is optional; omitted/null means unconstrained. A present
object uses the same exact AND filters and strict validation as stopping. Forward
criteria without adapter-side selection; the controller retains the filter with
the task and still enforces readiness, capacity and exact session ownership.
No match stays queued without fallback. Same-action direct children inherit the
saved filter, while different-action children do not. Task details and summaries
expose independent selector copies. Hostname is the agent's advertised
`server.hostname`, including its container/Pod namespace, not a Kubernetes node
or the hostname of a parent spawner. Keep placement out of action input.

`describe_agent` preserves optional dependency declarations from saved inventory;
`list_agents` omits them to keep discovery compact. Match only executable
`missing_requirements` to dependency names exactly. Declarations and dependency
version hints do not establish installed software or action readiness. Keep
secret/configuration handoffs separate and leave release inspection and installation to their existing
boundaries.

Question fallback is available to every MCP client through the same
`ask_question`, `get_operation` and `answer_question` tools. Clients schedule their
own fresh read at or after saved `fallback_at`; crossing it emits no durable event.
Recheck pending status before answering. Preserve the controller's atomic gate and
first-answer-wins result. Early-answer errors may add the saved cutoff through a
bounded read; failed enrichment must retain the original rejection without exposing
backend errors or retrying the write.

Questions with `command_id` originate from running actions. Answer the saved
question, then await that task's result; do not ask a duplicate, resolve its
continuation or queue replacement work while it runs. The action resumes after
answer delivery and retains its execution deadline.

Completion policy controls finalization only. Keep MCP guidance and completion
receipts explicit that finished tasks and a saved `completion_draft` leave the
operation open for requested assignments and model continuations. An empty
continuation decision finishes its branch, not the operation. Retain normal
question, session, readiness and terminal-state guards; never require operator
completion before continuing work in a nonterminal operation.
For a clear user request with known mission input and scope, guide the model to
start without redundant confirmation. Operator completion does not gate startup;
explicit approval requirements for the requested work still apply. Missing
readiness can create a waiting operation instead of preventing its creation.
When a required capability is not installed, guide installation through a separate
operation using registered mission and installer metadata. The original mission's
preflight blocks every queued task, including installer actions. Preserve its ID
and wait for registration and a ready original preflight before continuing.
Missing secrets/configuration, owner availability and capacity follow their own
handoff/recovery paths; false preflight alone does not justify reinstallation.

`assign_task.session_task_id` reuses the saved session of a finished task in the
same operation, including after that task's continuation resolves. It creates an
independent assignment; never reopen the prior decision, accept routing session
IDs in action input or infer a latest session. Keep lifecycle and question guards.

Stack-test teardown answers leftover questions through the authenticated operator
HTTP API before cancelling operations. Use the test API connection settings
(`COMMANDER_TEST_API_*`), never relabel MCP submissions as operator answers or
bypass `fallback_at`. Terminal unanswered questions cannot be repaired by answer
commands, so fail with a clear fixture diagnostic instead of polling forever.
