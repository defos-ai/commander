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
