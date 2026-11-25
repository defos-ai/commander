# Maintaining missiontest

Follow the framework [guide](../../AGENTS.md) and shared
[test rules](../../tests/AGENTS.md). Keep human usage in [README.md](README.md).

## Boundaries

- This is a public Go test driver for an existing Commander test API. Keep it
  independent of application/provider imports and stack startup. The controller
  remains responsible for execution, selection, policies and persistence.
- Reuse framework domain types where their JSON matches the API; explicitly
  project requests when internal fields are not accepted by the public endpoint.
  Operation snapshots do not include API-only live projections.
- Tests supply decisions, question answers and final output explicitly. Never
  resolve pending reviews, approve proposals or complete operations implicitly.
  API answers and completion use operator authority; they do not test MCP model
  restrictions or model planning quality.
- A no-model mission driver does not replace intelligence inside an agent. Agent
  repositories must inject their own deterministic model/tool fixtures.

## Requests and lifetime

- Require explicit URL and credentials. Keep credentials, request/response bodies
  and remote diagnostics out of helper errors. Disable redirects, bound HTTP
  bodies and requests, and preserve test-context cancellation.
- Never retry mutations automatically after transport errors or uncertain
  responses. Reserve and retain the operation ID before starting it so cleanup
  can address that exact operation even when its creation response is lost.
- Cleanup may cancel only operations created by this driver. Preserve imported
  missions, accepted artifacts, reports, execution history and shared runtime
  state. Docker lifecycle and deployment teardown remain in setup/CI scripts.
- Mission loading accepts an object or an array of definitions. Validate before
  importing; duplicate names fail rather than replacing a saved definition.
  Shared-stack fixtures import each definition once.
- Bound polling, retain the most recent saved state for timeout diagnostics, and
  leave pending decisions/questions visible to the scenario. Waits never submit
  work or extend execution deadlines.
- Verify downloaded file size and SHA-256 against accepted metadata before
  returning bytes; apply the configured artifact limit before allocation.

## Validation

Keep helper flow and failure scenarios under `framework/tests/missiontest/`,
setup under `framework/tests/fixtures/missiontest/`, and HTTP substitutes under
`framework/tests/fakes/missiontest/`. Use owned local fixtures without paid model
requests. Cover uncertain mutations, scoped cleanup and corrupt evidence through
public helper calls.

From the repository root, the focused checks are:

```sh
go build ./framework/...
go vet ./framework/...
go test -race -count=1 -timeout=3m ./framework/tests/missiontest/...
```
