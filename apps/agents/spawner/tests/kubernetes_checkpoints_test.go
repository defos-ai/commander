package packages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
)

func TestKubernetesIntentFailureNeverCreatesPod(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		name := "save rejected"
		if persisted {
			name = "acknowledgement lost"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newKubernetesManagerFixture(t)
			ctx, requestID, journal := newSpawnCheckpoints(t)
			failure := errors.New("fixture intent persistence failed")
			hook := func(_ context.Context, name string, _ json.RawMessage) error {
				if name == "launch-intent" {
					return failure
				}
				return nil
			}
			if persisted {
				journal.afterSave = hook
			} else {
				journal.beforeSave = hook
			}
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); !errors.Is(err, failure) {
				t.Fatalf("intent failure was hidden: %v", err)
			}
			if _, found, err := journal.LoadCheckpoint(ctx, "launch-intent"); err != nil || found != persisted {
				t.Fatalf("unexpected intent persistence: found=%t error=%v", found, err)
			}
			assertKubernetesLaunchCounts(t, fixture.client, 0, 0)
			journal.beforeSave, journal.afterSave = nil, nil
			_, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
			if persisted {
				if err == nil {
					t.Fatal("uncertain saved intent authorized creating a missing Pod")
				}
				assertKubernetesLaunchCounts(t, fixture.client, 0, 0)
			} else {
				if err != nil {
					t.Fatalf("rejected intent prevented a first acknowledged launch: %v", err)
				}
				assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
			}
		})
	}
}

func TestKubernetesCheckpointFailureRequiresDurabilityConfirmation(t *testing.T) {
	for _, checkpoint := range []string{"launch-receipt", "launch-instance"} {
		for _, persisted := range []bool{false, true} {
			name := checkpoint + "/save rejected"
			if persisted {
				name = checkpoint + "/acknowledgement lost"
			}
			t.Run(name, func(t *testing.T) {
				fixture := newKubernetesManagerFixture(t)
				ctx, requestID, journal := newSpawnCheckpoints(t)
				failure := errors.New("fixture checkpoint acknowledgement failed")
				hook := func(_ context.Context, name string, _ json.RawMessage) error {
					if name == checkpoint {
						return failure
					}
					return nil
				}
				if persisted {
					journal.afterSave = hook
				} else {
					journal.beforeSave = hook
				}
				for range 2 {
					if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); !errors.Is(err, failure) {
						t.Fatalf("unconfirmed checkpoint authorized readiness or activation: %v", err)
					}
				}
				if _, found, err := journal.LoadCheckpoint(ctx, checkpoint); err != nil || found != persisted {
					t.Fatalf("unexpected checkpoint persistence: found=%t error=%v", found, err)
				}
				releases := 0
				if checkpoint == "launch-instance" {
					releases = 1
				}
				assertKubernetesLaunchCounts(t, fixture.client, 1, releases)
				journal.beforeSave, journal.afterSave = nil, nil
				if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
					t.Fatalf("owner could not confirm persistence and observe the same Pod: %v", err)
				}
				assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
			})
		}
	}
}

func TestKubernetesRejectedReceiptDoesNotForgetKnownPodUID(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	ctx, requestID, journal := newSpawnCheckpoints(t)
	failure := errors.New("fixture receipt save rejected")
	journal.beforeSave = func(_ context.Context, name string, _ json.RawMessage) error {
		if name == "launch-receipt" {
			return failure
		}
		return nil
	}
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); !errors.Is(err, failure) {
		t.Fatalf("fixture did not reject receipt persistence: %v", err)
	}
	if _, found, err := journal.LoadCheckpoint(ctx, "launch-receipt"); err != nil || found {
		t.Fatalf("failed receipt was persisted: found=%t error=%v", found, err)
	}
	fixture.client.change(func(pod *kubernetes.Pod) { pod.Metadata.UID = "replacement-after-save-failure" })
	journal.beforeSave = nil
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
		t.Fatal("receipt failure allowed a replacement Pod to acquire the same launch")
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 0)
}

func TestKubernetesRejectedInstanceDoesNotForgetObservedProcess(t *testing.T) {
	for _, field := range []string{"container ID", "started at"} {
		t.Run(field, func(t *testing.T) {
			fixture := newKubernetesManagerFixture(t)
			ctx, requestID, journal := newSpawnCheckpoints(t)
			failure := errors.New("fixture instance save rejected")
			journal.beforeSave = func(_ context.Context, name string, _ json.RawMessage) error {
				if name == "launch-instance" {
					return failure
				}
				return nil
			}
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); !errors.Is(err, failure) {
				t.Fatalf("fixture did not reject instance persistence: %v", err)
			}
			if _, found, err := journal.LoadCheckpoint(ctx, "launch-instance"); err != nil || found {
				t.Fatalf("failed instance was persisted: found=%t error=%v", found, err)
			}
			fixture.client.change(func(pod *kubernetes.Pod) {
				before, after := "containerd://fixture-1", "containerd://replacement"
				if field == "started at" {
					before, after = "10:00:00Z", "10:00:01Z"
				}
				pod.Status = bytes.Replace(pod.Status, []byte(before), []byte(after), 1)
			})
			journal.beforeSave = nil
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
				t.Fatal("instance persistence failure allowed another process under the same Pod UID")
			}
			assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
		})
	}
}

func TestKubernetesCheckpointRejectsChangedRuntimeOrDeployment(t *testing.T) {
	for _, change := range []string{"runtime", "deployment", "package"} {
		t.Run(change, func(t *testing.T) {
			fixture := newKubernetesManagerFixture(t)
			ctx, requestID, journal := newSpawnCheckpoints(t)
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
				t.Fatal(err)
			}
			reference := fixtureReference
			manager := fixture.manager
			switch change {
			case "runtime":
				data, found, err := journal.LoadCheckpoint(ctx, "launch-intent")
				if err != nil || !found {
					t.Fatalf("missing intent: %v", err)
				}
				var record map[string]json.RawMessage
				if err := json.Unmarshal(data, &record); err != nil {
					t.Fatal(err)
				}
				record["runtime"] = json.RawMessage(`"docker"`)
				data, err = json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				journal.mu.Lock()
				journal.record.Checkpoints["launch-intent"] = data
				journal.mu.Unlock()
			case "deployment":
				config := fixture.config
				config.Pod.Secret = "another-provider-secret"
				var err error
				manager, err = kubernetes.NewManager(t.Context(), config)
				if err != nil {
					t.Fatal(err)
				}
				defer manager.Close()
			case "package":
				reference = fixtureModuleReference
			}
			if _, err := manager.Spawn(ctx, requestID, reference); err == nil {
				t.Fatal("changed runtime, deployment or package reused the launch")
			}
			assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
		})
	}
}

func TestKubernetesCheckpointOrphanFactsNeverCreateAnotherPod(t *testing.T) {
	for _, retained := range []string{"launch-receipt", "launch-instance"} {
		t.Run(retained, func(t *testing.T) {
			fixture := newKubernetesManagerFixture(t)
			ctx, requestID, journal := newSpawnCheckpoints(t)
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
				t.Fatal(err)
			}
			journal.mu.Lock()
			for _, name := range []string{"launch-intent", "launch-receipt", "launch-instance"} {
				if name != retained {
					delete(journal.record.Checkpoints, name)
				}
			}
			journal.mu.Unlock()
			restarted := restartKubernetesManager(t, fixture)
			if _, err := restarted.Spawn(ctx, requestID, fixtureReference); err == nil {
				t.Fatal("orphaned launch evidence authorized a new Create")
			}
			assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
		})
	}
}

func TestKubernetesCheckpointRunningHistoryForbidsGateRelease(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	ctx, requestID, _ := newSpawnCheckpoints(t)
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
		t.Fatal(err)
	}
	fixture.client.change(func(pod *kubernetes.Pod) {
		var spec map[string]json.RawMessage
		if err := json.Unmarshal(pod.Spec, &spec); err != nil {
			t.Fatal(err)
		}
		spec["schedulingGates"] = json.RawMessage(`[{"name":"ai.defos.commander/launch"}]`)
		pod.Spec, _ = json.Marshal(spec)
		pod.Status = json.RawMessage(`{"phase":"Pending"}`)
	})
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
		t.Fatal("recorded running instance was activated again from a gated observation")
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
}
