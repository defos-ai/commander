package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/native"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
)

func TestNativeExecutionWaitsForCheckpointBeforeStartingAndRetainsOwnership(t *testing.T) {
	fixture := fixtures.NewNativeManager(t, "", 5*time.Second)
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	saved, acknowledge := make(chan struct{}), make(chan struct{})
	journal.SetAfterSave(func(ctx context.Context, name string, _ json.RawMessage) error {
		if name != "launch-intent" {
			return nil
		}
		close(saved)
		select {
		case <-acknowledge:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	type outcome struct {
		result runtimes.Result
		err    error
	}
	finished, stopped := make(chan outcome, 1), make(chan struct{})
	go func() {
		defer close(stopped)

		result, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)
		finished <- outcome{result: result, err: err}
	}()
	t.Cleanup(func() { cancel(); <-stopped })
	select {
	case <-saved:
	case result := <-finished:
		t.Fatalf("spawn returned before saving launch intent: %v", result.err)
	case <-ctx.Done():
		t.Fatal("launch intent was not saved")
	}
	data, found, err := journal.LoadCheckpoint(ctx, "launch-intent")
	if err != nil || !found {
		t.Fatalf("missing checkpoint before acknowledgement: %v", err)
	}
	var intent struct {
		Runtime   string `json:"runtime"`
		RequestID string `json:"request_id"`
		Package   string `json:"package"`
		AgentID   string `json:"agent_id"`
	}
	if err := json.Unmarshal(data, &intent); err != nil {
		t.Fatal(err)
	}
	if intent.Runtime != "native" || intent.RequestID != requestID || intent.Package != fixtures.ModuleReferenceFixture || intent.AgentID == "" {
		t.Fatalf("checkpoint lacks complete launch identity: %s", data)
	}
	if len(fixtures.ChildReports(t, fixture.Storage)) != 0 {
		t.Fatal("child started before controller acknowledgement")
	}
	close(acknowledge)
	var result runtimes.Result
	select {
	case completed := <-finished:
		if completed.err != nil {
			t.Fatal(completed.err)
		}
		result = completed.result
	case <-ctx.Done():
		t.Fatal("acknowledged launch did not register")
	}
	if result.AgentID != intent.AgentID {
		t.Fatal("child identity differs from the acknowledged checkpoint")
	}
	before := fixture.RequestCount()

	repeated, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

	if err != nil || nativeDetails(t, repeated).PID != nativeDetails(t, result).PID || repeated.AgentID != result.AgentID {
		t.Fatalf("retry did not reuse the owned child: result=%+v error=%v", repeated, err)
	}
	if fixture.RequestCount() != before || len(fixtures.ChildReports(t, fixture.Storage)) != 1 {
		t.Fatal("retry downloaded or launched another child")
	}
	if err := fixture.Manager.Close(); err != nil {
		t.Fatal(err)
	}

	// A new host has the execution checkpoint but no exec.Cmd ownership. Even
	// with an empty local state directory it must refuse to replay the launch.
	restartedConfig := fixture.Config
	restartedConfig.Directory = filepath.Join(t.TempDir(), "spawner")
	restarted, err := native.NewManager(t.Context(), restartedConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := restarted.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("new host did not refuse the uncertain process: %v", err)
	}
	if _, err := restarted.Spawn(ctx, requestID, fixtures.Reference); err == nil || !strings.Contains(err.Error(), "different package") {
		t.Fatalf("checkpoint did not preserve its module package: %v", err)
	}
	if fixture.RequestCount() != before || len(fixtures.ChildReports(t, fixture.Storage)) != 1 {
		t.Fatal("new host downloaded or relaunched the checkpointed request")
	}
}

func TestNativeExecutionUncertainCheckpointDoesNotStartOrFallback(t *testing.T) {
	fixture := fixtures.NewNativeManager(t, "", 5*time.Second)
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	acknowledgementLost := errors.New("fixture acknowledgement lost")
	journal.SetAfterSave(func(_ context.Context, name string, _ json.RawMessage) error {
		if name == "launch-intent" {
			return acknowledgementLost
		}
		return nil
	})
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); !errors.Is(err, acknowledgementLost) {
		t.Fatalf("uncertain save returned %v", err)
	}
	before := fixture.RequestCount()
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("retry did not refuse the saved launch intent: %v", err)
	}
	if fixture.RequestCount() != before || len(fixtures.ChildReports(t, fixture.Storage)) != 0 {
		t.Fatal("uncertain save authorized a process or another download")
	}
}
