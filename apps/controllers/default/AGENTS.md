# Working on the default controller

Follow the [controller host guide](../AGENTS.md) for lifecycle contracts,
selection policies and validation.

[cli/cli.go](cli/cli.go) composes the controller host.
[cli/config.go](cli/config.go) loads its configuration, and
[cli/factory.go](cli/factory.go) selects providers.
