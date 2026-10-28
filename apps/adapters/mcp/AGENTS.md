# Working on the MCP adapter

Follow the [shared adapter guide](../AGENTS.md) for implementation contracts and
validation.

Start in [adapter.go](adapter.go) for tool registration and input decoding.
[cli/factory.go](cli/factory.go) selects providers;
[cli/http.go](cli/http.go) configures the HTTP host.
