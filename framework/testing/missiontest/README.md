# Mission tests without a model

Start an isolated Commander API/controller and your agents in test setup, then
drive their missions from Go with `missiontest`. The controller runs real work;
your test supplies decisions and checks results. Keep stack startup and teardown
in scripts or CI.

For a scenario under `tests/`, an agent exposing `inspect` and a repository file
at `missions/inspect.json`:

```go
package example_test

import (
	"os"
	"testing"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/testing/missiontest"
)

func TestInspectMission(t *testing.T) {
	driver := missiontest.Connect(t, missiontest.Config{
		URL: os.Getenv("COMMANDER_TEST_API_URL"),
		Credentials: host.BasicCredentials{
			Username: os.Getenv("COMMANDER_TEST_API_USERNAME"),
			Password: os.Getenv("COMMANDER_TEST_API_PASSWORD"),
		},
	})
	definitions := driver.LoadMissions("../missions/inspect.json")
	driver.WaitForActions("inspect")

	input := map[string]any{"target": "fixture"}
	op := driver.Start(definitions[0].Name, input)
	task := driver.Assign(op.ID, missiontest.Assignment{
		ActionName: "inspect",
		Input:      input,
	})
	finished := driver.WaitForTask(op.ID, task.ID)
	if finished.Status != operations.TaskSucceeded || finished.Result == nil {
		t.Fatalf("inspect finished with status %s", finished.Status)
	}

	review := driver.WaitForContinuation(op.ID, task.ID)
	driver.Resolve(op.ID, review)
	completed := driver.Complete(op.ID, finished.Result.Output)
	report := driver.Report(op.ID)
	if completed.Status != operations.Completed || report.ID != completed.ReportID {
		t.Fatal("mission did not retain its final report")
	}
}
```

`LoadMissions` accepts a JSON object or array. Import each definition once per
test stack, or use unique names; existing definitions are never overwritten.
`Start`, `Assignment.Input` and `Complete` accept Go values or raw JSON. Adjust
the action input and add assertions for your mission's actual results.
See the [example-agent operation test](../../../apps/adapters/api/tests/operations_test.go)
for a real-stack scenario with proposed actions, aggregation and digest downloads.

Use `Resolve` with explicit next actions for reviews that should continue. The
empty call above deliberately finishes that review. Keep `WaitFor` predicates
short and observational, then call `Resolve` or `Answer` after the wait. Use
`Download` to retrieve artifact files with size and SHA-256 verification. Unknown
decisions remain pending until your scenario handles them; waits fail with
saved-state details.

Waits default to two minutes and downloads to 32 MiB; adjust `Timeout` or
`MaxArtifactBytes` when needed. HTTP requests default to ten seconds; a supplied
client's positive timeout is respected. Cleanup cancels unfinished operations
created by the driver and waits up to five seconds for their saved tasks and
matching live agent work to settle, reporting anything unfinished. It preserves
saved evidence and mission definitions.
Inspect saved state after an uncertain write before submitting again. API error
codes distinguish rejected requests from uncertain outcomes.

API answers and completion represent operator input; test model permissions
through MCP separately. Agents that call a model internally still need a local
fake model or tool client to make their tests deterministic.

See the [test layout](../../tests/AGENTS.md) and
[Commander test setup](../../tests/README.md). Implementation contracts are in
[AGENTS.md](AGENTS.md).
