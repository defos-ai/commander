# Working on agent hosts

## Construction and lifecycle

- Use [commander-example-agent](https://github.com/defos-ai/commander-example-agent)
  as the reference for new standalone agents. Its `agent.go`, `actions/`, `cli/`
  and `tests/` separate construction, capabilities, providers and validation.
  Only Commander-owned modules belong in [go.work](../../go.work).
- Give each agent a fixed type and a fresh ID per process. Construct fresh action
  instances for every agent so discovery state is never shared between hosts.
  Propagate constructor failures instead of starting a partial runtime.
- Keep the fixed `AgentType`, `NewAgent` constructor and action catalog in
  `agent.go`. Return the framework's `*agent.Agent` directly when the host adds no
  agent methods or state; individual action implementations belong in `actions/`.
- Keep `actions/` for registered action implementations, their input/output types
  and readiness logic. Put subprocess, Git, archive and other supporting code in
  `internal/<purpose>/`. Small helpers used by one action can stay in its action
  file. Keep tests in `tests/` and exercise the public action/host contracts.
- Keep provider construction in each host's `cli/factory.go`; inject the bus and
  artifact file store into the runtime. Unsupported schemes must fail at startup.
- `Run` accepts cancellation without installing signal handlers; `Main` owns
  process signals. Cancel the host, await its runtime shutdown, then close provider
  clients. Joined transport or persistence errors must survive normal cancellation.
- Reusable hosts also run inside Playground. Keep process exits, global signal
  handlers and UI-specific dependencies out of constructors and reusable `Run` code.

## Package contract

- [Agent packages and bootstrap](../../docs/agent-packages.md) defines the release
  manifest and spawner behavior. The [spawner module](spawner/AGENTS.md)
  implements native/Docker spawning through the controller-dispatched `spawn-agent`
  action. Compose uses Docker mode; Playground and Docker AIO embed
  the spawner in native mode. Only spawner starts automatically; Compose's builder
  service is opt-in through its `builder` profile. Other agents are requested
  through the `install-agent` mission.
  Keep web/browser examples pinned to `v0.2.0`.
  Web/browser source, unit tests and releases belong to their standalone repositories.
- Keep `commander-example-agent` aligned with the package contract.
  Package workflows must publish binaries, image digest and manifest from the
  same tested revision; keep action schemas and requirements in registration.
  Export pure `Responsibilities()` and `ActionDescriptions()` functions for
  capability metadata without constructing agents, discovery or providers. Return
  fresh slices and share action description constants with registered constructors.
  Release manifests project responsibilities and action names/descriptions only;
  running hosts report actual readiness, schemas and required secret names.
  Built-in `ActionDescriptions()` returns `[]manifest.Action` summaries. Runtime
  snapshots expose static contracts through `Action.Definition()`.
- Built-in packages use their module-path references and existing module tags:
  `github.com/defos-ai/commander/apps/agents/<name>@<version>` resolves
  `agent.yaml` from `apps/agents/<name>/<version>` in the Commander repository.
  The manifest version remains the bare version. Builder and spawner each
  provide six native assets and Docker. Keep standalone repository
  references resolving `agent.yaml` from the root version tag. Agent releases
  never become `Latest`; root publication and stable promotion wait for them.
- `--agent-id-suffix` overrides `COMMANDER_AGENT_ID_SUFFIX`, including an explicit
  empty flag. Nonempty suffixes accept exactly eight lowercase hex characters.
  The host combines it with its fixed type using `id.AgentIDFromSuffix` and sets
  `AgentConfig.ID`; reject full IDs, prefixes and invalid suffixes before opening
  providers. Unset/empty values generate fresh IDs. The spawner saves the full
  ID before launch and passes only its suffix to the child. It reconciles retries
  against that instance; a new or replacement launch needs a fresh ID.
  Never start two processes with the same
  ID. This is instance correlation, not authorization or duplicate prevention.
  Keep provider settings, secrets and launch permissions under deployment control.

## Actions and readiness

- Use [action.NewFunc](../../framework/agent/action/func.go) for typed JSON actions;
  use [action.Executor](../../framework/agent/action/action.go) when execution needs
  file artifacts. Keep declared schemas and runtime validation consistent.
- Discover executables and configuration through the
  [environment abstraction](../../framework/agent/environment/). Honor explicit
  discovery scopes. Missing requirements make that action unavailable without
  disabling unrelated actions; report actionable requirement metadata.
- Advertise secret names, never values. Recheck required configuration during
  execution and invalidate stale discovery state when rediscovering dependencies.
- Declare persistent names in `Action.RequiredSecrets`; do not infer them from
  transient `MissingRequirements`. A secret name identifies one shared value
  across the Commander deployment; each recipient retains its own delivery state.
  Enable the framework secret runtime with `AgentConfig.Secrets: &agent.SecretConfig{}`
  for hosts that need managed secrets. Builder and spawner enable it without a key.
  Agents receive requested values from the controller rather than decrypting records.
  Access declared values with `action.Secret(ctx, name)`, never action inputs or
  global `os.Setenv`. Keep returned values out of outputs/logs and clear temporary
  byte copies. Missing values gate the affected action even without a decryptor.
- Catalog entries describe capabilities; per-agent readiness and leases
  determine availability. Do not claim that installing one agent guarantees
  complete required-action coverage for a mission.
- Keep HTTP/HTML actions in `commander-web-agent` and rendering actions in
  `commander-browser-agent`. Discovery
  can propose another agent type's actions by name; it must not import or call
  that agent's runtime.

## Results and follow-up work

- Return artifact groups as bytes or local paths. The runtime uploads file bytes
  through `ArtifactFileStore`; the controller accepts metadata through `ArtifactStore`.
  Uploaded bytes alone are not published evidence. Preserve ownership and checksums.
- Agents may return schema-valid next-action proposals with input and a reason.
  Mission policies own automation, and the controller dispatches accepted work; an
  action must never directly execute or send its proposed follow-up commands.
- Keep immutable execution outcomes separate from later operation decisions.
  Cancellation or transport uncertainty must not lead to duplicate execution.

## Receiving shared data

Use `action.ReceiveSharedData(ctx)` inside an operation execution served by
`Agent.Serve`. Inject the artifact file store from the host's factory and use the
same storage as the producers. Register the returned executor in
`AgentConfig.Actions`; handle constructor errors. Producers return ordinary
results/artifacts without extra sharing-specific publication code. See
[mission configuration](../../docs/features/data-sharing.md) for the operator flow
and the [framework guide](../../framework/AGENTS.md) for runtime contracts.

- Keep aggregation and deduplication memory local to the execution. Process every
  nonempty section independently: one update can contain both results and artifacts.
- Deduplicate results by source agent/command, artifacts by artifact ID and
  decisions by source agent/command/question ID. One command can produce several
  answers followed by its result. Mark records handled only after processing succeeds.
- Retain result JSON as `json.RawMessage` or decode into typed fields to preserve
  numeric precision. Use `ArtifactFileStore.Get(ctx, artifact.ID, file)` to retrieve
  and verify evidence. Preserve question scope, timestamps and answer origin.
- Define an explicit stopping condition. This example waits for a requested number
  of distinct successful producer results; it includes only artifacts and answers
  received before it stops. Later continuation answers need a longer-lived receiver.
- Propagate cancellation, receive deadlines, storage errors and buffer overflow.
  Silence does not prove completion, and restarting does not replay missed data.
  Bound retained data and evidence sizes for the workload; use managed secrets
  separately. Sharing neither starts tasks nor extends their deadlines.

Use this implementation as `actions/collect_findings.go` in an external agent.
Apply incremental analysis inside the loop and replace the counting output with
its summary. For an unreleased framework, use the
[local dependency setup](README.md#develop-an-agent-in-a-private-repository).

```go
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type CollectFindingsInput struct {
	ExpectedResults int `json:"expected_results"`
}

func (input CollectFindingsInput) Validate() error {
	if input.ExpectedResults < 1 || input.ExpectedResults > 100 {
		return errors.New("expected_results must be between 1 and 100")
	}
	return nil
}

type CollectFindingsOutput struct {
	Results   int `json:"results"`
	Artifacts int `json:"artifacts"`
	Decisions int `json:"decisions"`
}

type evidence struct {
	Metadata artifacts.Artifact
	Files    map[string][]byte
}

func NewCollectFindings(files artifacts.ArtifactFileStore) (action.Executor, error) {
	if files == nil {
		return nil, errors.New("artifact storage is required")
	}
	return action.NewFunc(action.FuncConfig[CollectFindingsInput, CollectFindingsOutput]{
		Action: actionmodel.Action{
			Name:        "collect-findings",
			Description: "Collect shared findings and evidence from other tasks.",
		},
		Run: func(ctx context.Context, input CollectFindingsInput) (CollectFindingsOutput, error) {
			results := make(map[[2]string]json.RawMessage)
			attachments := make(map[string]evidence)
			decisions := make(map[[3]string]operations.Question)
			for len(results) < input.ExpectedResults {
				update, err := action.ReceiveSharedData(ctx)
				if err != nil {
					if errors.Is(err, action.ErrSharedDataOverflow) {
						return CollectFindingsOutput{}, fmt.Errorf("findings are incomplete: %w", err)
					}
					return CollectFindingsOutput{}, err
				}
				for _, artifact := range update.Artifacts {
					if _, exists := attachments[artifact.ID]; exists {
						continue
					}
					item := evidence{Metadata: artifact, Files: make(map[string][]byte)}
					for _, file := range artifact.Files {
						data, err := files.Get(ctx, artifact.ID, file)
						if err != nil {
							return CollectFindingsOutput{}, fmt.Errorf("read evidence: %w", err)
						}
						item.Files[file.ID] = data
					}
					attachments[artifact.ID] = item
				}
				if question := update.Decision; question != nil {
					key := [3]string{update.Source.AgentID, update.Source.CommandID, question.ID}
					if _, exists := decisions[key]; !exists {
						decisions[key] = question.Clone()
					}
				}
				if len(update.Result) != 0 {
					key := [2]string{update.Source.AgentID, update.Source.CommandID}
					if _, exists := results[key]; !exists {
						results[key] = update.Result
					}
				}
			}
			return CollectFindingsOutput{Results: len(results), Artifacts: len(attachments), Decisions: len(decisions)}, nil
		},
	})
}
```

## Validation

Keep tests and fixtures in each agent module's `tests/` directory, using external
test packages to exercise public action and host APIs. Use existing dependency
injection for local fixtures. Do not place tests in `actions/` or `cli/` merely
to access private helpers, or export implementation details and add runtime
settings solely for tests.

Run the affected agent module from the repository root, for example:

```sh
go build ./apps/agents/builder/...
go vet ./apps/agents/builder/...
```

[Build/static CI](../../.github/workflows/test-unit.yml) covers Commander-owned hosts. Test
discovery failures, readiness and local fixtures without paid models
or public websites. Use [adapter scenarios](../../framework/tests/README.md#mcp-api-tests)
for behavior spanning agents, controller persistence and mission automation.
