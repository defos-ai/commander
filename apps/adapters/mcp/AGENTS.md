# Working on the MCP adapter

Follow the [shared adapter guide](../AGENTS.md) for implementation contracts and
validation.

Start in [adapter.go](adapter.go) for tool registration and input decoding.
[cli/factory.go](cli/factory.go) selects providers;
[cli/http.go](cli/http.go) configures the HTTP host.

`get_agent_sources` uses the caller-owned `SourceClient` to load the fixed public
`DefaultAgentSourcesURL` on demand. Return only name, description, source and
visibility in a `sources` list, sorted public first, then by name and full source
reference. The CLI owns its anonymous client and closes idle connections. Never
fetch during startup or accept source URLs/credentials as tool input. Reads are
bounded, and remote failures stay tool-local without exposing response bodies or
connection details. Source discovery grants no permissions and stores no state.

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
