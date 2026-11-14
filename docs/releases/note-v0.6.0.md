# Release notes v0.6.0

## Commander v0.6.0

Inspect an agent's declared software prerequisites before installation and review
them alongside its observed action readiness after launch.

- Add optional `dependencies` metadata to agent registration and release
  manifests. Each entry has a required `name` and `description`, plus optional
  `version` guidance. Match names to executable requirements reported by action
  discovery; declarations do not install software, check versions or gate
  unrelated actions.
- Expose declarations through HTTP agent list/detail responses, MCP
  `describe_agent` and Admin's agent details. Admin connects missing executable
  requirements to their declared installation guidance; compact MCP agent lists
  remain summaries.
- Add Spawner's `inspect-agent` to review a release before launching it, then
  pass its returned exact package reference to `spawn-agent`. Local API/MCP
  integration scenarios cover inspection, continuation, pinned launch and the
  registered dependency metadata.

### Upgrading from v0.5.1

- Retain saved data and the original encryption keys. No saved-data reset or
  migration is required; existing agents and manifests can omit dependencies.
- Update Commander hosts, agent runtimes, controller, adapters and Admin together.
  Reconnect MCP clients to load the new action catalog and guidance.
- Mission seeding preserves saved `install-agent` definitions. Explicitly edit
  that mission to adopt the optional inspection guidance; restarting or seeding
  does not overwrite it.
- Prepare prerequisites in the actual execution environment. Dependency names
  and version hints do not prove that software is installed or that actions are
  ready.

[Agent packages and dependencies](https://github.com/defos-ai/commander/blob/v0.6.0/docs/agent-packages.md#release-manifest) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.5.1...v0.6.0)

## Commander builder agent v0.6.0

- Declare `git`, `go` and `codex` prerequisites in live registration and the
  published release manifest. Go's version guidance is `1.26.7+`; Git and Codex
  have no newly imposed version minimums.
- Use the same declarations before installation through Spawner's
  `inspect-agent` and after launch through agent details. Missing tools still
  affect only the actions that need them; version hints are not installed-version
  checks. Managed-secret requirements stay unchanged.
- Align module requirements with Commander v0.6.0. Retain existing workspaces,
  session state and managed credentials when upgrading.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.6.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.6.0/apps/agents/builder/README.md)

## Commander spawner agent v0.6.0

- Add `inspect-agent`, accepting `package` and optional `visibility`. It returns
  the resolved exact package/version, agent type, configured runtime, declared
  dependencies, supported native platforms and any declared Docker image.
  Use that exact package reference for the later `spawn-agent` task.
- Report the current execution platform only for native mode, including the
  container environment in Docker all-in-one. Inspection does not install
  software, launch agents or verify Docker/Kubernetes target environments.
- Reuse private repository authentication through `GITHUB_TOKEN` or the existing
  managed-secret flow. Public inspection remains anonymous. Inspection grants no
  installation approval; `spawn-agent` independently applies source approval to
  its own execution.
- Align module requirements with Commander v0.6.0. Restart the spawner to expose
  both actions and reconnect clients that cache the action catalog.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.6.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.6.0/apps/agents/spawner/README.md)
