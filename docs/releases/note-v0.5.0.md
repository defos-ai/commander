# Release notes v0.5.0

## Commander v0.5.0

Run Commander as a local control plane for your coding agent, independently of
Playground and without an additional model API key for the Commander hosts.

```sh
commander start
commander setup --json
```

Configure your MCP client with the verified connection details, then call
`list_missions` through that client. Your connected AI client supplies planning
and model decisions; `setup` verifies the server without editing client settings.

- Add persistent background hosting with `start`, `status` and `stop`. Playground
  can attach to the running local instance; closing the client leaves its hosts
  running. Repeated start reuses the existing instance, and stop retains saved work,
  credentials and encryption keys.
- Add foreground `--all-in-one --headless` for hosts without the terminal chat.
  Restricted coding-agent shells can keep it running in a long-lived tool session
  when their sandbox ends detached processes after a command finishes.
- Serve bundled Admin automatically. An Admin startup failure leaves the required
  hosts running and reports how to retry. Native builds include the frontend.
- Publish checksum-verified `install.sh` and `install.ps1` with the native release
  assets. Installers support Linux, macOS and Windows on amd64 and arm64, preserve
  existing files on failure, and require explicit force for different bytes.
  They do not start Commander or edit shell profiles, persistent PATH or MCP settings.
- Return compact MCP confirmations from `start_operation`, `resolve_continuation`,
  `answer_question`, `complete_operation`, `fail_operation` and `cancel_operation`.
  Read complete saved state through `get_operation`, task evidence through
  `get_task`, and final output through `get_report`. Operator completion remains
  an explicit approval through Admin or the authenticated HTTP API.
- Reduce Playground model payloads by compacting JSON tool results without
  changing values or numeric precision. OpenAI Responses requests cap output at
  16,384 tokens. Explicit rate-limit rejections can retry before any generated
  output, honoring retry headers with at most two retries and 30 seconds of total
  waiting; accepted or uncertain MCP commands are not automatically repeated.
- Gate native builds on installation and onboarding checks using the actual
  executable: verify authenticated MCP and bundled Admin assets, then stop,
  restart and reconnect to retained state. Fixtures use local downloads and no
  paid model calls.

### Upgrading from v0.4.1

- Stop the running local instance before replacing its executable, then restart
  with the same `--state-dir` when using a custom profile. Keep the complete
  profile and original encryption keys. This release introduces no automatic
  saved-data migration or reset.
- Remove `--serve-admin-ui` from scripts; Admin starts automatically. Native local
  MCP defaults to `127.0.0.1:4273`. Explicit deployment addresses still take
  precedence; use `setup` to discover the actual endpoint.
- Update MCP consumers that expected full operation snapshots in command replies
  to use the detail tools. Confirmations retain `id`, `status`, `progress` and
  optional preflight, report reference, error or operator guidance. HTTP operation
  responses and `assign_task`/`ask_question` replies retain their existing shapes.
- Keep Commander components on the same release. Existing `open`/`use` agent
  session contracts remain in place. Browser, Cybersec and other standalone agents
  publish separately; their host tools and managed secrets are still required.

[Installation](https://github.com/defos-ai/commander/blob/v0.5.0/docs/installation.md) ·
[Coding-agent connection](https://github.com/defos-ai/commander/blob/v0.5.0/apps/playground/README.md#connect-a-coding-agent) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.4.1...v0.5.0)

## Commander builder agent v0.5.0

- Align module requirements with Commander v0.5.0. Checkout, build, publication
  and release actions keep the existing controller-owned `open`/`use` workspace
  session contract.
- Run Builder with its required host tools and managed credentials; the local
  headless hosts do not supply a model key to Builder automatically. Retain the
  existing workspace/session state when upgrading.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.5.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.5.0/apps/agents/builder/README.md)

## Commander spawner agent v0.5.0

- Return concise per-action readiness after registration: action name, optional
  session mode, status, reason, missing requirements and required secret names.
  Spawn results omit full action schemas; query the registered agent and
  `describe_action` for the complete contract.
- Distinguish a registered agent from usable capabilities. Successful installation
  can return actions that remain unavailable until host tools, configuration or
  managed secrets are supplied. Concise spawn replies leave the controller's full
  registration metadata intact.
- Deploy with matching Commander v0.5.0 components. Native, Docker and Kubernetes
  launch settings remain host-controlled; the source-approval flow is unchanged.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.5.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.5.0/apps/agents/spawner/README.md)
