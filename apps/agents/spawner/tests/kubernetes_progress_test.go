package packages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

func TestKubernetesProgressPreservesLaunchFactsAndObservedReadiness(t *testing.T) {
	for _, failedLogs := range []bool{false, true} {
		name := "logs accepted"
		var logError error
		if failedLogs {
			name, logError = "logging failed", errors.New("log transport failed")
		}
		t.Run(name, func(t *testing.T) {
			fixture := fixtures.NewKubernetesManager(t)
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
			logs := fakes.NewProgressLogs(logError)
			ctx = action.WithLogs(ctx, logs)
			fixture.Client.SetBeforeCreate(func(ctx context.Context, pod kubernetes.Pod) error {
				data, found, err := journal.LoadCheckpoint(ctx, "launch-intent")
				if err != nil || !found || !bytes.Contains(data, []byte(pod.Metadata.Name)) {
					return errors.New("Create preceded its immutable Pod intent")
				}
				return nil
			})
			fixture.Client.SetBeforeRelease(func(ctx context.Context, pod kubernetes.Pod) error {
				data, found, err := journal.LoadCheckpoint(ctx, "launch-receipt")
				if err != nil || !found || !bytes.Contains(data, []byte(pod.Metadata.UID)) || fixture.Bus.SubscriptionCount() == 0 {
					return errors.New("activation preceded durable identity or registration subscription")
				}
				return nil
			})
			fixture.Client.SetOnRelease(func(ctx context.Context, pod kubernetes.Pod) error {
				return fixtures.PublishKubernetesRegistration(ctx, fixture.Bus, pod, func(snapshot *agent.AgentSnapshot) {
					snapshot.Actions[0].Status = actionmodel.Unavailable
				})
			})

			first, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

			if err != nil || first.Runtime != runtimes.RuntimeKubernetes || len(first.Actions) != 1 || first.Actions[0].Status != actionmodel.Unavailable {
				t.Fatalf("progress changed the observed readiness snapshot: %+v %v", first, err)
			}
			checkpoints := journal.Checkpoints()
			if len(checkpoints) != 4 || checkpoints["package-selection"] == nil || checkpoints["launch-intent"] == nil ||
				checkpoints["launch-receipt"] == nil || checkpoints["launch-instance"] == nil {
				t.Fatalf("progress replaced immutable launch facts: %+v", checkpoints)
			}
			pod := fixture.Client.Snapshot()
			assertKubernetesProgress(t, logs, []string{
				"Kubernetes preparation started", "Kubernetes preparation completed",
				"Kubernetes launch reconciliation started", "Kubernetes launch reconciliation completed",
				"Kubernetes running Pod and registration observation started", "Kubernetes running Pod and registration observation completed",
			}, requestID, first.AgentID, fixtures.DockerImage, pod.Metadata.UID, fixture.Config.Pod.Secret)

			repeated, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

			if err != nil || !reflect.DeepEqual(first, repeated) || !reflect.DeepEqual(checkpoints, journal.Checkpoints()) {
				t.Fatalf("progress changed exact launch replay: %+v %v", repeated, err)
			}
			assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
		})
	}
}

func TestKubernetesProgressDoesNotRepeatUncertainMutations(t *testing.T) {
	for _, mutation := range []string{"create", "activation"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := fixtures.NewKubernetesManager(t)
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
			private := "private-cluster-diagnostic/token-value"
			want := errors.New(private)
			if mutation == "create" {
				fixture.Client.SetCreateError(want)
			} else {
				fixture.Client.SetReleaseError(want)
			}
			logs := fakes.NewProgressLogs(errors.New("logging unavailable: " + private))
			logged := action.WithLogs(ctx, logs)

			_, err := fixture.Manager.Spawn(logged, requestID, fixtures.Reference)

			if !errors.Is(err, want) {
				t.Fatalf("logging replaced the uncertain mutation error: %v", err)
			}
			intent := journal.Checkpoints()["launch-intent"]
			if intent == nil {
				t.Fatal("mutation lost its create fence")
			}
			assertKubernetesProgress(t, logs, []string{
				"Kubernetes preparation started", "Kubernetes preparation completed",
				"Kubernetes launch reconciliation started", "Kubernetes launch reconciliation ended with error",
			}, private, requestID, fixtures.Reference)
			retryLogs := fakes.NewProgressLogs(nil)

			result, err := fixture.Manager.Spawn(action.WithLogs(ctx, retryLogs), requestID, fixtures.Reference)

			if err != nil || result.AgentID == "" || !bytes.Equal(intent, journal.Checkpoints()["launch-intent"]) {
				t.Fatalf("progress prevented observation of an accepted mutation: %+v %v", result, err)
			}
			assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
		})
	}
}

func TestKubernetesProgressDoesNotAuthorizeUnconfirmedIntent(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	want := errors.New("private checkpoint error containing token-value")
	journal.SetAfterSave(func(_ context.Context, name string, _ json.RawMessage) error {
		if name == "launch-intent" {
			return want
		}
		return nil
	})
	logs := fakes.NewProgressLogs(errors.New("log transport unavailable"))
	logged := action.WithLogs(ctx, logs)

	_, err := fixture.Manager.Spawn(logged, requestID, fixtures.Reference)

	if err != want {
		t.Fatalf("logging changed the original checkpoint error: %v", err)
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 0, 0)
	intent := journal.Checkpoints()["launch-intent"]
	assertKubernetesProgress(t, logs, []string{
		"Kubernetes preparation started", "Kubernetes preparation completed",
		"Kubernetes launch reconciliation started", "Kubernetes launch reconciliation ended with error",
	}, want.Error(), "token-value", requestID)

	_, err = fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

	if err == nil || !bytes.Equal(intent, journal.Checkpoints()["launch-intent"]) {
		t.Fatal("retry treated uncertain intent persistence as permission to create")
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 0, 0)
}

func TestKubernetesProgressCancellationRetainsLateRegistration(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	fixture.Client.SetOnRelease(func(context.Context, kubernetes.Pod) error { return nil })
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	logs := fakes.NewProgressLogs(nil)
	waiting, cancel := context.WithCancel(action.WithLogs(ctx, logs))
	defer cancel()
	finished := fixtures.SpawnKubernetesAsync(t, fixture.Manager, waiting, requestID)
	for {
		select {
		case message := <-logs.Published():
			if message == "Kubernetes running Pod and registration observation started" {
				goto observing
			}
		case err := <-finished:
			t.Fatalf("spawn ended before registration observation: %v", err)
		case <-time.After(time.Second):
			t.Fatal("spawn did not report its registration observation")
		}
	}

observing:
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("logging changed action cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled progress reporter kept the spawn waiting")
	}
	for _, message := range logs.Messages() {
		if message == "Kubernetes running Pod and registration observation completed" {
			t.Fatal("canceled wait claimed a registration observation")
		}
	}
	checkpoints := journal.Checkpoints()
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
	if err := fixtures.PublishKubernetesRegistration(ctx, fixture.Bus, fixture.Client.Snapshot(), nil); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

	if err != nil || result.AgentID == "" || !reflect.DeepEqual(checkpoints, journal.Checkpoints()) {
		t.Fatalf("progress cancellation lost the host-owned child or late observation: %+v %v", result, err)
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
}

func assertKubernetesProgress(t *testing.T, logs *fakes.ProgressLogs, expected []string, private ...string) {
	t.Helper()
	var stages []string
	for _, message := range logs.Messages() {
		if strings.HasPrefix(message, "Kubernetes ") {
			stages = append(stages, message)
		}
		for _, value := range private {
			if strings.Contains(message, value) {
				t.Fatal("Kubernetes progress exposed launch inputs, identity or diagnostics")
			}
		}
	}
	if !reflect.DeepEqual(stages, expected) {
		t.Fatalf("progress did not describe sequential observation phases: %+v", stages)
	}
}
