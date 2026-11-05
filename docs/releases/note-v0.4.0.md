# Release notes v0.4.0

## Commander v0.4.0

Connect agents with their own models to shared missions: request credentials,
ask questions, report progress and checkpoints, and analyze evidence from other
agents while work continues.

### Agent integrations and shared data

- Route results, artifact references and answered questions between running
  actions with `policies.sharing`. Agents consume them through
  `action.ReceiveSharedData`; artifact bytes stay in shared file storage.
- Aggregated child groups have stable batch identities and
  `task.batch.started` / `task.batch.finished` notifications. Receivers know their
  permitted producers and their succeeded, failed or cancelled outcomes.
  Interrupted outcomes remain unknown. Sharing is live and best effort; finish
  does not prove every result arrived, and restarts do not replay earlier data.
- Controller workers now own their dependencies and lifecycle, joining accepted
  work during shutdown. This release continues to support one active controller
  per database.

### Secrets, policies and applications

- Use `action.RequestReusableSecret` for shared credentials and
  `action.RequestTransientSecret` for execution-specific codes. Reusable requests
  accept an optional `Default`; a missing name becomes a shared encrypted secret,
  while existing values take precedence. Transient values stay out of reusable
  storage and the agent-wide cache; receipt confirms arrival, not website acceptance.
- Admin and Playground provide masked one-time secret forms through the
  authenticated API. Values stay outside chat and question history.
- Typed mission policies group continuation, completion, session and sharing
  settings under `policies`. `policies.session` binds create/use actions to an
  exact resource owner, replacing the `same-agent` placement rule. API, MCP and
  Admin preserve these settings when creating, editing and inspecting missions.
- The default controller, filesystem and MongoDB stores retain batch membership
  and boundaries with operation transitions. RabbitMQ and S3 integrations share
  the coordinated module release for messaging and artifact storage.
- One `spawn-agent` action accepts `count: 1..100`, defaulting to one. Larger
  requests propose individual child launches for controller dispatch and review.
- The Hacker News digest demo installs the example agent when needed, asks for
  topics and returns Markdown/JSON artifacts from public story metadata.

### Upgrading from v0.3.0

- Upgrade the controller, adapters and agent binaries together, rebuilding custom
  agents against matching framework/provider modules. Execution messages, action
  calls and logs now carry explicit `operation_id` and `task_id` fields instead of
  nested `work`; old agents can reject the new wire format.
- Replace mission root `rules` and `completion` with typed
  `policies.continuation`, `policies.completion`, `policies.session` and
  `policies.sharing` as applicable. Legacy placement settings are rejected.
  Update saved definitions explicitly; bundled mission seeding preserves existing
  definitions. See [mission policies](https://github.com/defos-ai/commander/blob/v0.4.0/docs/features/missions.md#choose-policies).
- Builder checkout/build now use session bindings instead of `workspace_id` as
  routing input. The bundled `build-agent` mission supplies them through
  `policies.session`; direct workflows supply `session_scope` and `session_id`.
- Custom controller hosts should use `controller.NewController`; the independent
  runner and rule-selector APIs have been replaced by owned workers and typed
  policy evaluation.
- Back up retained state and its encryption key before upgrading. Saved legacy
  policies and nested execution records are not automatically migrated and may
  prevent startup recovery. Review their compatibility before rollout.

All twelve workspace modules use v0.4.0. Standalone Codex, Browser and other agent
packages are released separately; this release supplies the integration hooks.

[Get started](https://github.com/defos-ai/commander/blob/v0.4.0/README.md#quick-start) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.3.0...v0.4.0)

## Commander builder agent v0.4.0

- Keep checkout and build on the workspace session's exact owner. The bundled
  mission supplies the binding through `policies.session`; explicit scope/session
  IDs remain available for direct workflows and reuse across operations.
- Inspect and close workspace resources through Commander's session lifecycle.
  Failed checks preserve source for revision; successful builds publish the
  source archive through normal artifact acceptance.
- Rebuild and deploy with Commander v0.4.0 for the updated execution protocol.
  Update saved Builder missions explicitly; seeding does not replace them.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.4.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.4.0/apps/agents/builder/README.md)

## Commander spawner agent v0.4.0

- Request one to 100 instances through the existing `spawn-agent` action; omitted
  `count` remains one. Larger requests propose one child task per instance, each
  with its own launch identity, deadline, checkpoints and source approval.
- Review saved child results for actual launches. The parent's batch response is
  a request, not proof that every agent started. Pin an exact package version
  when all instances must use the same release.
- Continue using native, Docker or Kubernetes runtimes with host-controlled
  launch settings. Deploy with matching Commander v0.4.0 components for the
  updated execution protocol.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.4.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.4.0/apps/agents/spawner/README.md)

