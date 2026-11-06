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

Stack-test teardown answers leftover questions through the authenticated operator
HTTP API before cancelling operations. Use the test API connection settings
(`COMMANDER_TEST_API_*`), never relabel MCP submissions as operator answers or
bypass `fallback_at`. Terminal unanswered questions cannot be repaired by answer
commands, so fail with a clear fixture diagnostic instead of polling forever.
