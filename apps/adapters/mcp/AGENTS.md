# Working on the MCP adapter

Follow the [shared adapter guide](../AGENTS.md) for implementation contracts and
validation.

Start in [adapter.go](adapter.go) for tool registration and input decoding.
[cli/factory.go](cli/factory.go) selects providers;
[cli/http.go](cli/http.go) configures the HTTP host.

`get_agent_sources` reads the required `AgentSourceStore` with a bounded context and
returns a `sources` list with source references and saved trust, version and audit
metadata. Keep public entries first, then
sort by name and full source reference. The CLI shares the other stores'
`DATABASE_URL` and cached Mongo connection. Discovery neither seeds entries nor
changes trust; backend failures must not expose paths or connection credentials.
