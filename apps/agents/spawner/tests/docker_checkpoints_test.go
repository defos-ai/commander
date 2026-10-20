package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
)

func TestDockerCheckpointFailureNeverCreatesContainer(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		name := "save rejected"
		if persisted {
			name = "acknowledgement lost"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newDockerManagerFixture(t)
			ctx, requestID, journal := newSpawnCheckpoints(t)
			failure := errors.New("fixture checkpoint persistence acknowledgement failed")
			failIntent := func(_ context.Context, name string, _ json.RawMessage) error {
				if name == "launch-intent" {
					return failure
				}
				return nil
			}
			if persisted {
				journal.afterSave = failIntent
			} else {
				journal.beforeSave = failIntent
			}
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); !errors.Is(err, failure) {
				t.Fatalf("spawn did not return checkpoint failure: %v", err)
			}
			_, found, err := journal.LoadCheckpoint(ctx, "launch-intent")
			if err != nil || found != persisted {
				t.Fatalf("intent persistence: found=%t error=%v", found, err)
			}
			if persisted {
				journal.afterSave = nil
				if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil || !strings.Contains(err.Error(), "uncertain") {
					t.Fatalf("acknowledgement loss authorized creating a missing container: %v", err)
				}
			}
			creates, starts, _ := fixture.engine.counts()
			if creates != 0 || starts != 0 {
				t.Fatalf("checkpoint failure launched container: create=%d start=%d", creates, starts)
			}
			assertNoDockerCheckpointFiles(t, fixture.config.Directory)
		})
	}
}

func TestDockerCheckpointReusesContainerWithoutLocalRecords(t *testing.T) {
	fixture := newDockerManagerFixture(t)
	ctx, requestID, journal := newSpawnCheckpoints(t)
	first, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
	if err != nil || repeated.ContainerID != first.ContainerID || repeated.AgentID != first.AgentID {
		t.Fatalf("repeat changed container: result=%+v error=%v", repeated, err)
	}
	for _, name := range []string{"package-selection", "launch-intent"} {
		if _, found, err := journal.LoadCheckpoint(ctx, name); err != nil || !found {
			t.Fatalf("missing controller checkpoint %s: %v", name, err)
		}
	}
	assertNoDockerCheckpointFiles(t, fixture.config.Directory)

	// A different state directory cannot supply the old launch records. Only
	// the persisted checkpoint and the daemon's owned running instance remain.
	config := fixture.config
	config.Directory = filepath.Join(t.TempDir(), "restored")
	restored, err := docker.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	stop := dockerHeartbeats(t, fixture, first.ContainerID)
	defer stop()
	result, err := restored.Spawn(ctx, requestID, fixtureReference)
	if err != nil || result.ContainerID != first.ContainerID {
		t.Fatalf("restored manager did not observe saved container: result=%+v error=%v", result, err)
	}
	creates, starts, _ := fixture.engine.counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("repeat relaunched container: create=%d start=%d", creates, starts)
	}
	assertNoDockerCheckpointFiles(t, config.Directory)
}

func TestDockerCheckpointCreatedContainerNeedsOriginalOwner(t *testing.T) {
	fixture := newDockerManagerFixture(t)
	ctx, requestID, _ := newSpawnCheckpoints(t)
	fixture.engine.createErr = errors.New("fixture lost Docker Create response")
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
		t.Fatal("lost Create response unexpectedly succeeded")
	}
	// A second directory has its own OS lock, so saved intent alone must not
	// grant a second manager permission to Start the original container.
	config := fixture.config
	config.Directory = filepath.Join(t.TempDir(), "second-owner")
	second, err := docker.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Spawn(ctx, requestID, fixtureReference); err == nil || !strings.Contains(err.Error(), "ownership is uncertain") {
		t.Fatalf("second manager attempted to start restored intent: %v", err)
	}
	creates, starts, _ := fixture.engine.counts()
	if creates != 1 || starts != 0 {
		t.Fatalf("second owner launched container: create=%d start=%d", creates, starts)
	}
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
		t.Fatalf("original owner could not reconcile lost Create response: %v", err)
	}
	creates, starts, _ = fixture.engine.counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("owner retry relaunched container: create=%d start=%d", creates, starts)
	}
}

func TestDockerCheckpointRejectsRuntimeMismatch(t *testing.T) {
	fixture := newDockerManagerFixture(t)
	ctx, requestID, journal := newSpawnCheckpoints(t)
	journal.afterSave = func(_ context.Context, name string, _ json.RawMessage) error {
		if name == "launch-intent" {
			return errors.New("fixture stops before Docker Create")
		}
		return nil
	}
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
		t.Fatal("fixture did not stop before launch")
	}
	data, found, err := journal.LoadCheckpoint(ctx, "launch-intent")
	if err != nil || !found {
		t.Fatalf("missing launch intent: %v", err)
	}
	var intent map[string]any
	if err := json.Unmarshal(data, &intent); err != nil {
		t.Fatal(err)
	}
	intent["runtime"] = "native"
	data, err = json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	journal.mu.Lock()
	journal.record.Checkpoints["launch-intent"] = data
	journal.mu.Unlock()
	journal.afterSave = nil
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil || !strings.Contains(err.Error(), "invalid or incomplete") {
		t.Fatalf("runtime mismatch accepted: %v", err)
	}
	creates, starts, _ := fixture.engine.counts()
	if creates != 0 || starts != 0 {
		t.Fatalf("runtime mismatch launched container: create=%d start=%d", creates, starts)
	}
	assertNoDockerCheckpointFiles(t, fixture.config.Directory)
}

func assertNoDockerCheckpointFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(directory, "records"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("execution checkpoint path persisted local launch records")
	}
}
