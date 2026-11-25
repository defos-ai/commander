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
	"encoding/json"
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

	op := driver.Start(definitions[0].Name, map[string]any{"target": "fixture"})
	task := driver.Assign(op.ID, missiontest.Assignment{
		ActionName: "inspect",
		Input:      json.RawMessage(`{"target":"fixture"}`),
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
Adjust the action input and add assertions for your mission's actual results.

Use `Resolve` with explicit next actions for reviews that should continue. The
empty call above deliberately finishes that review. Use `WaitFor` for custom
conditions, `Answer` for scripted question answers, and `Download` to retrieve
artifact files with size and SHA-256 verification. Unknown decisions remain
pending until your scenario handles them; waits fail with saved-state details.

Waits default to two minutes and downloads to 32 MiB; adjust `Timeout` or
`MaxArtifactBytes` when needed. Cleanup cancels unfinished operations created by
the driver and preserves saved evidence and mission definitions.
API answers and completion represent operator input; test model permissions
through MCP separately. Agents that call a model internally still need a local
fake model or tool client to make their tests deterministic.

See the [test layout](../../tests/AGENTS.md) and
[Commander test setup](../../tests/README.md). Implementation contracts are in
[AGENTS.md](AGENTS.md).
