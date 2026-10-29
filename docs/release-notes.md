# Release notes

## Commander v0.3.0

Prepare intelligent agents for distributed missions with shared credentials,
interactive questions, execution progress and operator-controlled completion.

### Missions and execution

- Create and edit mission objectives, actions, rules and schemas through Admin,
  HTTP API, MCP or Admin CLI. Names stay unchanged; running operations retain
  their original mission definition.
- Set `completion.decision_by` to `operator` to require explicit completion.
  Model submissions save `completion_draft`; operators review or edit it in
  Admin before completing through the existing API. Omitted/model policy keeps
  immediate completion.
- Actions can ask durable questions with `action.Question`. Operators answer
  first; the model may answer after `fallback_at`. Passing the waiting deadline
  never supplies an answer or completion approval.
- Record execution progress with `action.Log`, query it through HTTP/MCP and
  follow it in Admin, filtering by task, agent or action.
- Reconcile late execution outcomes after interrupted waits. The default initial
  task-result wait is now `15m`, configurable with `TASK_TIMEOUT`; a timeout does
  not prove the underlying work stopped.
- Inspect and manage agent-owned runtime resources in Admin's **Sessions** page.

### Secrets and deployment

- Save a credential once by name for all requesting agents, including agents
  started later. The controller decrypts requested values for delivery; agents
  and spawner no longer need `SECRETS_ENCRYPTION_KEY`.
- Playground shares the selected provider key through the API under its canonical
  environment-variable name, preserving any existing saved value.
- Spawner can launch Kubernetes Pods using in-cluster authentication and configured
  namespace, ServiceAccount and child configuration references.
- API, MCP and Admin UI honor platform `PORT`, with component-specific listener
  settings and explicit flags taking precedence.

### Playground and Admin

- Playground retains settings, chat history and locally encrypted provider keys
  in `~/.commander`, including remote sessions. `--temporary` opts out; Docker
  sessions retain state through their mounted volume.
- Provider and connection status stay fixed above the scrolling conversation.
  Completed tool calls replace their pending row, and the operation summary shows
  up to three recent operations.
- Admin opens on Operations, provides shared Secrets in the sidebar and exposes
  connection health through the current connection's Status page.

### Upgrading from v0.2.0

- Use `OPENAI_API_KEY` for Builder's managed credential instead of `CODEX_API_KEY`.
- Configure the same `SECRETS_ENCRYPTION_KEY` on API and controller only. Update
  custom agents to the current managed-secret protocol; secret names and API
  routes are no longer agent-scoped. Earlier encrypted secret formats are not
  automatically migrated.
- Update question clients for operator-first waiting and saved `fallback_at`;
  external MCP clients must schedule their own fresh read before a model answer.
- Express mission workflows through objectives, actions and rules; mission steps
  have been removed.
- Only spawner starts automatically. Enable Compose's `builder` profile or install
  Builder when needed. Bundled missions are `build-agent` and `install-agent`;
  `review-website` is no longer seeded. Existing saved missions are preserved.
- Rebuild custom Go integrations against the matching Commander module versions.
  Codex Agent and Browser Agent's browser-use integration follow this release.

## Commander builder agent v0.3.0

- Use the shared managed `OPENAI_API_KEY` delivered by the controller.
- Follow checkout, generation, validation, publication and release stages through
  execution logs, including periodic progress during long stages.
- Run as an optional Compose service or install the package separately. All-in-one
  retains the tooling needed by installed builders.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.3.0`

## Commander spawner agent v0.3.0

- Launch native processes, Docker containers or Kubernetes Pods and return each
  runtime's identity through `runtime_details`.
- Use mounted in-cluster credentials for Kubernetes; child Pods use configured
  references without receiving the secrets encryption key.
- Install `github.com/defos-ai` packages directly. Other owners trigger an
  execution question: operator first, model fallback after the waiting window.
  Approval applies to that execution; no owner trust is saved.
- Inspect execution logs for package selection, preparation, launch reconciliation
  and registration waits.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.3.0`

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
