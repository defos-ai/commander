package packages_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
)

func TestNativeProgressFailuresPreserveReadinessAndLiveChildReplay(t *testing.T) {
	fixture := fixtures.NewNativeManager(t, "", 5*time.Second)
	execution, requestID, _ := fixtures.SpawnCheckpoints(t)
	ctx, cancel := context.WithTimeout(execution, 10*time.Second)
	defer cancel()
	logError := errors.New("untrusted diagnostic with private-token and /private/download/path")
	logs := fakes.NewProgressLogs(logError)

	first, err := fixture.Manager.Spawn(action.WithLogs(ctx, logs), requestID, fixtures.Reference)

	if err != nil || first.Package != fixtures.Reference || nativeDetails(t, first).PID <= 0 || len(first.Actions) != 1 ||
		first.Actions[0].Name != "fixture-action" || first.Actions[0].Status != action.Ready {
		t.Fatalf("progress failure changed native registration: result=%+v error=%v", first, err)
	}
	want := []string{
		"Package version selection started", "Package version selection completed",
		"Native package preparation started", "Native package preparation completed",
		"Native launch reconciliation started", "Native launch reconciliation completed",
		"Native registration observation started", "Native registration observation completed",
	}
	if !reflect.DeepEqual(logs.Messages(), want) {
		t.Fatalf("native progress changed phase order or exposed runtime data: %v", logs.Messages())
	}
	before := fixture.RequestCount()
	replayLogs := fakes.NewProgressLogs(logError)

	replayed, err := fixture.Manager.Spawn(action.WithLogs(ctx, replayLogs), requestID, fixtures.Reference)

	if err != nil || !reflect.DeepEqual(replayed, first) || fixture.RequestCount() != before || len(fixtures.ChildReports(t, fixture.Storage)) != 1 {
		t.Fatalf("logging changed live-child reuse or repeated its effects: result=%+v error=%v", replayed, err)
	}
	if !reflect.DeepEqual(replayLogs.Messages(), []string{
		"Package version selection started", "Package version selection completed",
		"Native registration observation started", "Native registration observation completed",
	}) {
		t.Fatalf("replay reported a fresh preparation or launch: %v", replayLogs.Messages())
	}
	for _, forbidden := range []string{logError.Error(), fixtures.Reference, fixtures.FixtureReleaseURL, fixture.Config.Directory, requestID, first.AgentID} {
		if strings.Contains(strings.Join(append(logs.Messages(), replayLogs.Messages()...), "\n"), forbidden) {
			t.Fatalf("native progress exposed private runtime data: %q", forbidden)
		}
	}
}

func TestNativeProgressIsVisibleDuringACancelledRegistrationWait(t *testing.T) {
	fixture := fixtures.NewNativeManager(t, "-delayed", 5*time.Second)
	execution, requestID, _ := fixtures.SpawnCheckpoints(t)
	ctx, cancel := context.WithTimeout(execution, 10*time.Second)
	defer cancel()
	observer := fixtures.NativeProgressObserver(t, ctx, fixture)
	request, cancelRequest := context.WithCancel(ctx)
	logs := fakes.NewProgressLogs(errors.New("private log backend diagnostic"))
	done := make(chan struct{})
	var spawnErr error
	t.Cleanup(func() {
		cancelRequest()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("native progress spawn did not finish after cancellation")
		}
	})

	go func() {
		defer close(done)
		_, spawnErr = fixture.Manager.Spawn(action.WithLogs(request, logs), requestID, fixtures.Reference)
	}()
	for waiting := false; !waiting; {
		select {
		case message := <-logs.Published():
			waiting = message == "Native registration observation started"
		case <-done:
			t.Fatalf("spawn ended before its pending registration was observable: %v", spawnErr)
		case <-ctx.Done():
			t.Fatal("native registration wait did not report progress")
		}
	}
	report := fixtures.AwaitChildReport(t, ctx, fixture.Storage)
	cancelRequest()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("canceled native wait did not end")
	}

	if !errors.Is(spawnErr, context.Canceled) || !reflect.DeepEqual(logs.Messages(), []string{
		"Package version selection started", "Package version selection completed",
		"Native package preparation started", "Native package preparation completed",
		"Native launch reconciliation started", "Native launch reconciliation completed",
		"Native registration observation started",
	}) {
		t.Fatalf("cancellation changed ownership or left a reporter active: error=%v messages=%v", spawnErr, logs.Messages())
	}
	fixtures.AnnounceNativeRegistration(t, report)
	readAgentAnnouncement(t, ctx, observer, messages.AgentStarted, report.AgentID)
	before := fixture.RequestCount()
	replayLogs := fakes.NewProgressLogs(nil)

	result, err := fixture.Manager.Spawn(action.WithLogs(ctx, replayLogs), requestID, fixtures.Reference)

	if err != nil || result.AgentID != report.AgentID || nativeDetails(t, result).PID != report.PID ||
		fixture.RequestCount() != before || len(fixtures.ChildReports(t, fixture.Storage)) != 1 ||
		!reflect.DeepEqual(replayLogs.Messages(), []string{
			"Package version selection started", "Package version selection completed",
			"Native registration observation started", "Native registration observation completed",
		}) {
		t.Fatalf("late registration did not retain the original child and replay-only phases: result=%+v error=%v messages=%v", result, err, replayLogs.Messages())
	}
}
