package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
)

func TestDockerCheckpointFailureNeverCreatesContainer(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		name := "save rejected"
		if persisted {
			name = "acknowledgement lost"
		}
		t.Run(name, func(t *testing.T) {
			fixture := fixtures.NewDockerManager(t)
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
			failure := errors.New("fixture checkpoint persistence acknowledgement failed")
			failIntent := func(_ context.Context, name string, _ json.RawMessage) error {
				if name == "launch-intent" {
					return failure
				}
				return nil
			}
			if persisted {
				journal.SetAfterSave(failIntent)
			} else {
				journal.SetBeforeSave(failIntent)
			}
			if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); !errors.Is(err, failure) {
				t.Fatalf("spawn did not return checkpoint failure: %v", err)
			}
			_, found, err := journal.LoadCheckpoint(ctx, "launch-intent")
			if err != nil || found != persisted {
				t.Fatalf("intent persistence: found=%t error=%v", found, err)
			}
			if persisted {
				journal.SetAfterSave(nil)
				if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil || !strings.Contains(err.Error(), "uncertain") {
					t.Fatalf("acknowledgement loss authorized creating a missing container: %v", err)
				}
			}
			creates, starts, _ := fixture.Engine.Counts()
			if creates != 0 || starts != 0 {
				t.Fatalf("checkpoint failure launched container: create=%d start=%d", creates, starts)
			}
		})
	}
}

func TestDockerCheckpointCreatedContainerNeedsOriginalOwner(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	fixture.Engine.SetCreateError(errors.New("fixture lost Docker Create response"))
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
		t.Fatal("lost Create response unexpectedly succeeded")
	}
	// The saved intent alone must not grant another manager permission to
	// Start the original container.
	config := fixture.Config
	second, err := docker.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Spawn(ctx, requestID, fixtures.Reference); err == nil || !strings.Contains(err.Error(), "ownership is uncertain") {
		t.Fatalf("second manager attempted to start restored intent: %v", err)
	}
	creates, starts, _ := fixture.Engine.Counts()
	if creates != 1 || starts != 0 {
		t.Fatalf("second owner launched container: create=%d start=%d", creates, starts)
	}
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err != nil {
		t.Fatalf("original owner could not reconcile lost Create response: %v", err)
	}
	creates, starts, _ = fixture.Engine.Counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("owner retry relaunched container: create=%d start=%d", creates, starts)
	}
}

func TestDockerCheckpointRejectsRuntimeMismatch(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	journal.SetAfterSave(func(_ context.Context, name string, _ json.RawMessage) error {
		if name == "launch-intent" {
			return errors.New("fixture stops before Docker Create")
		}
		return nil
	})
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
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
	journal.ReplaceCheckpoint("launch-intent", data)
	journal.SetAfterSave(nil)
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil || !strings.Contains(err.Error(), "invalid or incomplete") {
		t.Fatalf("runtime mismatch accepted: %v", err)
	}
	creates, starts, _ := fixture.Engine.Counts()
	if creates != 0 || starts != 0 {
		t.Fatalf("runtime mismatch launched container: create=%d start=%d", creates, starts)
	}
}
