# Controllers

The controller coordinates long-running missions across distributed agents,
applies saved rules to follow-up work, and retains progress and results for
operational review. Use Admin's **Controllers** page to check its health.

## Quick start

Compose and all-in-one include the default controller. To run it separately
from the repository root with Go 1.26.7+:

```sh
go run ./apps/controllers/default/cmd
```

Connect [adapters](../adapters/README.md#quick-start) and
[agents](../agents/README.md#quick-start) to the same providers. Run **one active
controller per database**.

Compose and all-in-one seed bundled missions through the API. For a separate
deployment, use [Admin](../admin/README.md) `missions seed` or `missions import`.
See [mission authoring](../../docs/abstractions.md) and
[deployment](../../docs/deployment.md).

For longer tasks such as agent builds, increase the default wait of 15 minutes:

```sh
go run ./apps/controllers/default/cmd --task-timeout=30m
```

`TASK_TIMEOUT=30m` also works; the flag overrides it. The setting applies to newly
dispatched tasks. Expiry marks an unknown outcome as interrupted while late-result
reconciliation continues; it does not stop the agent or change saved deadlines.

Set `SECRETS_ENCRYPTION_KEY` to the API's key to decrypt requested managed secrets
before delivery. Compose supplies the saved key; all-in-one shares its session
cipher. Saved records remain encrypted; agents receive requested values and do
not need the encryption key.

## Development and testing

Configure providers in [`default/cli/`](default/cli/). Use the
[framework controller](../../framework/controller/) when writing another host.

### Agent selection

The default is `runner.RoundRobinSelector{}`. To change selection, implement
[`runner.AgentSelector`](../../framework/controller/runner/agent_selector.go)
and pass it in `runner.Config.AgentSelector`. Return an eligible agent ID or
an empty ID to defer work. [Task affinity](../../docs/abstractions.md#task-affinity)
still applies.

### Rule selection

Pass a [`runner.RuleSelector`](../../framework/controller/runner/rule_selector.go)
in `runner.Config.RuleSelector` to choose rules when an operation starts.
The default uses the mission's rules. Return the full rule set; the saved
selection applies throughout that operation.

For either selector, report its `Name()` in the corresponding
`controller.RegistrationConfig` field so Admin displays it. See [AGENTS.md](AGENTS.md)
for lifecycle and scheduling constraints.

### Run tests

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/controllers/default/...
go vet ./apps/controllers/default/...
```

See [testing](../../framework/tests/README.md) for integration coverage.
