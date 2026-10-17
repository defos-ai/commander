# Working on the builder agent

Read the [agent guide](../AGENTS.md) and [root guide](../../../AGENTS.md) first.

## Entry points

- [builder_agent.go](builder_agent.go) fixes the type to `builder-agent` and
  supplies a fresh action catalog when `AgentConfig.Actions` is nil. An explicitly
  empty catalog stays empty.
- [cli](cli/) owns host configuration, provider factories and lifecycle.
  `--workspace-dir` overrides `BUILDER_WORKSPACE_DIR`; the default is
  `state/builder-workspaces`. Providers follow the web host's existing conventions.
- [checkout-template](actions/checkout_template.go) is the only action so far.
  Its input is `{}`. The source URL is fixed; do not expose paths or arbitrary
  Git URLs through task input.

## Workspace and process boundaries

- Constructors must not clone repositories or require Git. Discovery uses
  `environment.Environment.FindExecutable` and respects explicit scopes, including
  an empty scope. Missing or removed Git makes only the action unavailable.
- Create a private, unique checkout per execution. Return an opaque workspace ID
  and exact revision, never a host path. Successful checkouts remain in configured
  storage; remove only the current incomplete checkout on errors or cancellation.
- [git.go](actions/git.go) runs argv directly with bounded output and an isolated
  environment. Never inherit managed secrets, Git config, credential helpers or
  proxy settings. Disable hooks/templates and recursive submodules. The temporary
  child home is removed after execution. Keep Git errors free of raw remote output.
- Unix cancellation stops the Git process group before cleanup; other platforms
  currently use `exec.CommandContext` cancellation of the direct process.
- Checkout must not run template code, build, publish, or launch generated agents.
  Those stages need their own actions and managed-secret declarations. Do not add
  environment fallbacks for managed secrets.
- Workspace storage is local to the owning builder. Controller selection has no
  workspace affinity yet. Add explicit ownership/routing or source transfer before
  proposing multi-step work across builder instances.

## Validation

Read [shared test guidance](../../../framework/tests/AGENTS.md) for new tests.
Follow the [agent test layout](../AGENTS.md#validation): keep tests and fixtures in
`tests/` and exercise public APIs. Use local Git fixtures; never clone the public
template in tests. Cover discovery, strict input, isolated workspaces,
revision output, config/credential isolation, failure cleanup and cancellation.

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/builder/...
go vet ./apps/agents/builder/...
```

Keep the builder owner filter and unit step in `.github` aligned with provider
imports. CI and release runs include this module; images and native release
assets are not wired for the builder yet.
