# Working on controller hosts

## Host wiring and lifecycle

- [default/cli/cli.go](default/cli/cli.go) composes registration, coordination,
  the runner and adapter-command consumer. Configuration lives in `cli/config.go`;
  concrete providers and their cleanup belong in `cli/factory.go`.
- Keep framework scheduling and recovery in [framework/controller](../../framework/controller/).
  A host supplies dependencies and policies; it must not duplicate the runner's
  owner loop or introduce its own operation mutation path.
- Mark registration ready only after the runner and consumers start. On shutdown,
  mark draining, stop command intake, close the runner, then coordination, then
  registration and providers. Keep coordination available while the runner suspends
  accepted tasks, and use bounded shutdown contexts that survive parent cancellation.
- Keep the current one-active-controller-per-database assumption explicit.
  Registration and provider support do not implement distributed ownership or
  capacity across controllers.
- Construct both secret and request stores from the configured database and
  inject them into coordination. The controller routes ciphertext and must not
  load `SECRETS_ENCRYPTION_KEY`. Framework reconciliation owns delivery retries,
  pending request recovery and installation acknowledgements.

## Selection and durable work

- [runner.AgentSelector](../../framework/controller/runner/agent_selector.go)
  chooses only among supplied eligible candidates. Affinity, readiness, capacity
  checks and durable dispatch reservation remain the runner's responsibility.
- Selection runs serially in the owner loop. Return promptly, honor cancellation,
  and do not dispatch work or retain mutable candidate snapshots. An empty ID
  defers work; errors or invalid IDs leave it queued.
- Report the selector's `Name()` in registration. The previous selected agent ID
  advances only after durable dispatch reservation; load/capacity describe this
  runner's work, not a distributed global limit.
- [runner.RuleSelector](../../framework/controller/runner/rule_selector.go) selects
  the full rule set at operation start. Inject it through `runner.Config.RuleSelector`;
  the default `MissionRuleSelector` clones the mission's rules. To extend a definition,
  clone `selection.Mission.Rules` before editing; nil or empty means no explicit rules.
  Register the rule selector's `Name()` in `RegistrationConfig.RuleSelector`, just
  as the agent selector's name is registered in `RegistrationConfig.AgentSelector`.
- Rule selection runs outside the owner loop and may overlap concurrent starts.
  Implementations must honor the context and be concurrency-safe. The runner
  validates and clones selected rules before saving them only in the operation's
  mission snapshot. Errors and invalid rules reject the start without fallback.
  Recovery uses that saved selection; do not reselect rules or mutate the catalog.
- Dispatched tasks remain bound to their recorded agent and command. Never silently
  reassign an execution whose outcome is unknown or retry it from host code.
- Lifecycle changes need coordinated persistence, consumers and restart recovery.
  Reconcile uncertain writes before dispatching more work; preserve immutable
  execution outcomes, accepted artifact ownership and reports.

## Mission loading

- [default/data/missions.go](default/data/missions.go) validates all file definitions
  before upserting any. Preserve missions omitted from the file; no `--with-missions`
  means no import. An operation retains the mission definition saved at its start.
  Mission `objectives` must contain at least one nonblank string. Keep separate
  goals or instruction groups in separate entries; every entry applies. Steps
  retain a singular `objective`. Preserve order and snapshot independence.
- Update [default-missions.json](default/data/default-missions.json) for mission
  rules. Agents propose actions, while the controller applies these rules;
  do not move mission policy into host or agent special cases.
  The no-browser review uses `discover-pages-no-browser` and three HTTP checks;
  the with-browser review uses `discover-pages` and five checks. Keep starting
  actions, requirements and rules aligned with each discovery's proposals.
- The bundled `install-agent` mission requires only `spawn-agent`, so a fresh
  deployment can install packages before business actions are available. Keep
  its model review rule: the client must inspect launch results and current
  readiness before completing provisioning. A mission waiting on the new
  actions cannot dispatch its own provisioning task.
  Its `package` input accepts GitHub URLs, `@stable`, and exact versions. Leave
  stable resolution and retry pinning to the spawner; an omitted version means stable.
  Its optional `visibility` input is `public` or `private`, defaulting to public;
  clients preserve it when assigning `spawn-agent`. Never accept tokens in mission input.
- The bundled `build-agent` mission requires action contracts alongside instructions.
  Define them from the operator's request before checkout. It starts checkout
  with `{}`, then model review assigns the returned workspace ID and original
  instructions and actions unchanged to `build-agent`.
  Keep `same-agent` on both stages because workspace files are local, and model
  review on both results. Managed `CODEX_API_KEY` goes through the secret flow.
  After successful build review, offer the optional `publish-agent` action in
  this operation and wait for operator agreement. Declining it completes the
  mission with its source artifact. Declare publication in `optional_actions`,
  outside `required_actions`, step requirements and `starting_actions`, so missing
  `GITHUB_TOKEN` cannot block checkout/build. Action lists describe expected work;
  they are not an execution allowlist. Normal rules and readiness apply when
  optional work is scheduled.
  Publication uses the saved source artifact ID and full file metadata, with
  model review but no `same-agent` rule or local workspace dependency. Read the
  action's visibility default; if neither an explicit choice nor a default exists,
  ask Public (recommended) or Private through `ask_question` and wait for the real
  answer. Never infer approval from expiry. Managed `GITHUB_TOKEN` uses the secret
  flow only when publication or release is selected. Publication returns a source
  commit. Offer `release-agent` as another optional action in the same operation;
  obtain release consent and a version independently of publication consent.
  Keep it outside `same-agent` and `decide-next-actions`: bounded pending checks
  propose the same action with `workflow_run_id` and continue automatically.
  Empty terminal proposals still return to model review. Only a verified released
  result provides a pinned package. Offer installation only after that result;
  honor an explicit operator choice or ask a linked **Install this agent?**
  question with **Install** and **Finish**, waiting for the actual answer.
  Complete the build with its source/publication/release evidence first. Approval
  starts the existing `install-agent` mission in a separate operation, passing
  the exact released package and visibility to `spawn-agent`. Keep provisioning
  out of the build's task tree. Review the saved spawn result and action readiness;
  registration alone is insufficient, and uncertain launches require inspection.
  Declining installation or an installation failure preserves the successful build.

## Validation

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/controllers/default/...
go vet ./apps/controllers/default/...
```

- [Host tests](default/tests/) cover registration, startup, imports and the bundled
  mission. Mongo startup coverage requires `COMMANDER_TEST_MONGO_URL` and a replica
  set; report that coverage as skipped if the variable is absent.
- [Integration CI](../../.github/workflows/test-integration.yml) supplies MongoDB;
  [unit CI](../../.github/workflows/test-unit.yml) covers the host without it.
  Follow [Mongo setup](../../providers/mongo/README.md#development-and-testing) for
  service-backed checks, and stop only the test services/state you started.
- Scheduler or recovery changes need affected framework tests plus public
  [MCP/API scenarios](../../framework/tests/README.md#mcp-api-tests), including
  restart, uncertain writes and settled results where relevant.
