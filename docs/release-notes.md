# Release notes

## Commander v0.1.0

The first release of DEFOS Commander: a framework and runtime for orchestrating
missions across distributed agents.

### Highlights

- **Run missions through chat:** Playground connects your chosen model to Commander through MCP.
- **Coordinate distributed agents:** the controller discovers capabilities, checks readiness and assigns tasks.
- **Automate follow-up work:** mission rules collect results and hand decisions to code, models or people.
- **Review execution in Admin:** inspect operations, execution graphs, questions, reports and downloadable artifacts.
- **Try website reviews:** included web and browser agents check links, SEO, technologies, accessibility and screenshots.
- **Choose your deployment:** native all-in-one, Docker AIO or Docker Compose, with filesystem, MongoDB, RabbitMQ and S3 providers.

Native Commander binaries are available for Linux, macOS and Windows on amd64 and arm64.

All-in-one sessions are temporary. Use Compose to retain results.

[Get started](https://github.com/defos-ai/commander/blob/v0.1.0/README.md#quick-start)

## Commander v0.2.0

Commander can now build agents, install published packages on demand and create
missions through Admin, MCP or the HTTP API.

### New

- **Builder agent:** generate agents with Codex, verify their action contracts and download the source. Optionally publish and release them on GitHub.
- **Spawner agent:** install compatible GitHub packages as native processes or Docker containers. Supports pinned versions, stable releases, private repositories and private GHCR images.
- **Agent catalog:** discover available agents through MCP, or install directly from a GitHub repository URL.
- **Mission builder:** select available actions, define capabilities before an agent exists, configure optional actions and inspect saved missions as JSON.
- **Managed secrets:** encrypted persistence and delivery to agents, with masked Playground and Admin forms that keep values outside the model conversation.
- **Task affinity:** the `same-agent` rule keeps related actions on one agent instance.
- **Execution checkpoints:** actions can persist facts before external side effects and inspect them when handling retries.

### Playground and Admin

- Admin UI is embedded in release binaries and Docker AIO.
- Completed operations link directly to their Admin details.
- Improved API-key retries, wheel/trackpad scrolling, text selection and terminal copying.
- Shared progress reporting keeps API, MCP, Playground and Admin consistent.

### Fixes and maintenance

- Improved Windows filesystem messaging and test reliability.
- Updated vulnerable frontend dependencies.
- Replaced web/browser-agent integration dependencies with the lightweight example agent.

### Upgrading from v0.1.0

- Replace mission root `objective` with `objectives: ["Your objective"]`. Step-level `objective` remains unchanged.
- Replace action requirement field `secret` with `sensitive`. Managed secret requirements use `kind: "secret"`.
- Update MCP clients to read task counts and pending work from `progress`.
- Default deployments now start builder and spawner. Install web and browser agents when needed; they are maintained in separate repositories.
- Custom Go integrations must support the new mission creation and execution checkpoint interfaces.

[Get started](https://github.com/defos-ai/commander/blob/v0.2.0/README.md#quick-start) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.1.0...v0.2.0)

## Commander builder agent v0.2.0

Build Commander agents from a request, review their source and optionally publish
them for others to install.

### Actions

- **`checkout-template`:** create a workspace from commander-example-agent.
- **`build-agent`:** generate code with Codex, run Go and package checks, verify registered action contracts and export a `source.tar.gz` artifact.
- **`publish-agent`:** publish reviewed source to a new or empty public/private GitHub repository.
- **`release-agent`:** run the repository's release workflow and return a verified installable package reference.

Failed build checks preserve the workspace for revision. Publishing and releasing
are optional; you can finish with the source artifact alone.

Supply Codex and GitHub secrets through Playground or Admin forms.

### Availability

Included in Commander's all-in-one and Compose deployments. Standalone packages
provide native binaries for Linux, macOS and Windows on amd64/arm64, plus a Linux
amd64 Docker image.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.2.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.2.0/apps/agents/builder/README.md)

## Commander spawner agent v0.2.0

Install published Commander agents as native processes or Docker containers
through the `spawn-agent` action.

### Highlights

- Install any compatible GitHub agent package; catalog membership is optional.
- Use an exact version, `@stable` or an unversioned GitHub URL.
- Download checksum-verified native executables or launch digest-pinned Docker images.
- Access private GitHub repositories and GHCR images using `GITHUB_TOKEN`, supplied through the environment or managed-secret forms.
- Preserve selected versions and launch intents through execution checkpoints.
- Return the installed agent's identity and observed action readiness.

Native and Docker AIO deployments launch child processes. Compose launches sibling
containers on the deployment network.

### Availability

Included in Commander's all-in-one and Compose deployments. Standalone packages
provide native binaries for Linux, macOS and Windows on amd64/arm64, plus a Linux
amd64 Docker image.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.2.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.2.0/apps/agents/spawner/README.md)
