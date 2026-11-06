# Release notes v0.4.1

## Commander v0.4.1

Let the controller assign runtime sessions and keep follow-up actions on their
resource's exact owner. Models no longer create or copy session IDs for routing
through action inputs.

- Session-aware actions declare `session_mode: "open"` or `"use"`. New opens
  receive controller-assigned identities; continuations inherit their direct
  parent's session. Separate branches retain separate bindings, and stateless
  actions remain unbound.
- Agents acquire the approved binding through `action.OpenSession(ctx)` or
  `action.UseSession(ctx)`. Their providers open the actual resource and retain
  readiness and close checks. Closed or lost bindings never select a replacement
  resource automatically.
- Trusted controller hosts can supply `ActionCall.Session` to reuse a retained
  resource outside operation tasks. Public API/MCP root assignments do not expose
  that attachment; use a continuation for session-aware follow-up work.
- Session records, bindings and creation claims retain the original `command_id`,
  `operation_id` and `task_id` directly. Command identity is required; operation
  and task provenance may be omitted together. Direct and operation dispatch
  share reservation, owner binding and publication checks.

### Upgrading from v0.4.0

This patch changes session contracts. Upgrade Commander components and session-aware
agents together; all twelve workspace modules use v0.4.1.

- Remove `policies.session` from saved mission definitions. It is rejected rather
  than ignored. Route consumers through their session-owning parent's continuation.
  Update saved Builder missions explicitly; seeding preserves existing definitions.
- Rebuild custom agents against matching modules, replacing `create`/`reuse`
  handling with `open`/`use`, adopting the helpers, and updating provider creation
  provenance, input schemas and mission guidance. Browser and Cybersec agents
  publish separately after their adoption. Older agents advertising `create` are
  rejected during registration as invalid action metadata.
- Back up retained state and its original encryption key before rollout. Saved
  mission/operation policies, nested creation claims and `create`/`reuse` intents
  are not automatically migrated and may prevent recovery. Review retained data
  compatibility before upgrading; this release does not reset it.

[Session usage](https://github.com/defos-ai/commander/blob/v0.4.1/docs/features/sessions.md) ·
[Agent integration contract](https://github.com/defos-ai/commander/blob/v0.4.1/framework/sessions/AGENTS.md) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.4.0...v0.4.1)

## Commander builder agent v0.4.1

- Checkout prepares the example-agent source without session routing fields in
  its input. Continue from checkout with `build-agent`, supplying only instructions
  and action contracts; the controller retains the workspace and exact owner.
- Checkout uses `action.OpenSession`; build uses `action.UseSession`. Session IDs
  remain output metadata for inspection and controls. Failed checks retain source,
  and successful builds return source archives through normal artifact acceptance.
- Deploy with matching Commander v0.4.1 components. Update saved Builder missions
  to remove `policies.session` and input IDs; bundled mission seeding does not
  replace saved definitions. Saved legacy session records are not migrated.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.4.1`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.4.1/apps/agents/builder/README.md)

## Commander spawner agent v0.4.1

- Deploy with matching Commander v0.4.1 components. Spawned agents must advertise
  the updated `open`/`use` session contract; older `create` metadata is rejected
  after launch during registration.
- Rebuild and publish session-aware Browser, Cybersec and custom agents against
  the new framework before installing their updated packages. Adopt the session
  helpers, flat creation provenance and continuation-based routing together;
  renaming metadata alone does not complete the upgrade.
- Native, Docker and Kubernetes launch settings remain host-controlled. A launched
  process or container is not proof that its registration and action readiness
  were accepted; review the saved spawn result.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.4.1`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.4.1/apps/agents/spawner/README.md)
