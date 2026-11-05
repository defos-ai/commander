# Release notes v0.3.0

## Commander v0.3.0

Prepare intelligent agents for distributed missions with shared credentials,
interactive questions, execution progress and operator-controlled completion.

### Missions and execution

- Create and edit mission objectives, actions, policies and schemas through Admin,
  HTTP API, MCP or Admin CLI. Names stay unchanged; running operations retain
  their original mission definition.
- Keep Builder workspaces and other resources in sessions; follow-up actions
  use the saved session owner across operations.
- Set `policies.completion.decision_by` to `operator` to require explicit completion.
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
- Update Builder workflows: checkout creates a workspace session with
  `session_scope`; build uses its returned `session_scope` and `session_id`.
  Update saved `build-agent` missions to match the bundled definition; seeding
  preserves existing definitions.
- Configure the same `SECRETS_ENCRYPTION_KEY` on API and controller only. Update
  custom agents to the current managed-secret protocol; secret names and API
  routes are no longer agent-scoped. Earlier encrypted secret formats are not
  automatically migrated.
- Update question clients for operator-first waiting and saved `fallback_at`;
  external MCP clients must schedule their own fresh read before a model answer.
- Express mission workflows through objectives, actions and policies; mission
  steps and the root `rules` field have been removed. Use sessions for shared
  resources, `policies.continuation.aggregate_results` for aggregation, and
  `policies.continuation.model_review` for model review. Move root `completion` to
  `policies.completion`. Stored definitions and operation snapshots are not
  automatically converted; old snapshots can block startup recovery.
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

