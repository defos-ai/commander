package packages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

func assertKubernetesLaunchCounts(t *testing.T, client *fakes.PodClient, creates, releases int) {
	t.Helper()
	actualCreates, actualReleases, _, closes := client.Counts()
	if actualCreates != creates || actualReleases != releases || closes != 0 {
		t.Fatalf("unexpected Pod lifecycle: create=%d release=%d client close=%d; want %d/%d/0", actualCreates, actualReleases, closes, creates, releases)
	}
}

func TestKubernetesManagerPersistsIdentityBeforeActivation(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "private-parent-sentinel")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "private-parent-sentinel")
	fixture := fixtures.NewKubernetesManager(t)
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	var intentAcknowledged, receiptAcknowledged atomic.Bool
	journal.SetAfterSave(func(_ context.Context, name string, _ json.RawMessage) error {
		if name == "launch-intent" {
			intentAcknowledged.Store(true)
		}
		if name == "launch-receipt" {
			receiptAcknowledged.Store(true)
		}
		return nil
	})
	fixture.Client.SetBeforeCreate(func(ctx context.Context, pod kubernetes.Pod) error {
		if !intentAcknowledged.Load() {
			return errors.New("Pod created before intent acknowledgement")
		}
		data, found, err := journal.LoadCheckpoint(ctx, "launch-intent")
		if err != nil || !found || !bytes.Contains(data, []byte(pod.Metadata.Name)) || !bytes.Contains(data, []byte(pod.Metadata.Namespace)) {
			return errors.New("launch intent omitted the fixed Pod destination")
		}
		return nil
	})
	fixture.Client.SetBeforeRelease(func(ctx context.Context, pod kubernetes.Pod) error {
		if !receiptAcknowledged.Load() || fixture.Bus.SubscriptionCount() == 0 {
			return errors.New("Pod activated before durable UID receipt or registration subscription")
		}
		data, found, err := journal.LoadCheckpoint(ctx, "launch-receipt")
		if err != nil || !found || !bytes.Contains(data, []byte(pod.Metadata.UID)) {
			return errors.New("launch receipt omitted server-assigned Pod identity")
		}
		return nil
	})

	result, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

	if err != nil {
		t.Fatal(err)
	}
	details, ok := result.RuntimeDetails.(runtimes.KubernetesRuntimeDetails)
	pod := fixture.Client.Snapshot()
	if result.Runtime != runtimes.RuntimeKubernetes || !ok || details.Namespace != pod.Metadata.Namespace || details.PodName != pod.Metadata.Name || details.PodUID != pod.Metadata.UID ||
		result.AgentID != pod.Metadata.Labels["ai.defos.commander.agent-id"] || result.Package != fixtures.ModuleReferenceFixture || len(result.Actions) != 1 || result.Actions[0].Status != actionmodel.Ready {
		t.Fatalf("result lost Pod identity or observed readiness: %+v", result)
	}
	for _, name := range []string{"package-selection", "launch-intent", "launch-receipt", "launch-instance"} {
		if _, found, err := journal.LoadCheckpoint(ctx, name); err != nil || !found {
			t.Fatalf("missing acknowledged checkpoint %s: %v", name, err)
		}
	}
	assertCheckpointExcludes(t, journal, "private-parent-sentinel")
	result.Actions[0].Name = "caller-mutated"
	result.RuntimeDetails = runtimes.KubernetesRuntimeDetails{PodUID: "caller-mutated"}

	repeated, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

	if err != nil || repeated.AgentID != pod.Metadata.Labels["ai.defos.commander.agent-id"] || repeated.RuntimeDetails != details || repeated.Actions[0].Name != "fixture-action" {
		t.Fatalf("retry lost the original child or shared mutable output: %+v, %v", repeated, err)
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
}

func TestKubernetesManagerRejectsInvalidLaunchesBeforeCreate(t *testing.T) {
	for _, reason := range []string{"missing checkpoints", "wrong request identity", "Builder"} {
		t.Run(reason, func(t *testing.T) {
			fixture := fixtures.NewKubernetesManager(t)
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			reference := fixtures.Reference
			switch reason {
			case "missing checkpoints":
				ctx = action.WithExecution(t.Context(), action.Execution{CommandID: strings.TrimPrefix(requestID, "command:")})
			case "wrong request identity":
				requestID = "command:" + id.MessageID()
			case "Builder":
				reference = fixtures.BuilderReference
			}
			if _, err := fixture.Manager.Spawn(ctx, requestID, reference); err == nil {
				t.Fatal("invalid launch was accepted")
			}
			assertKubernetesLaunchCounts(t, fixture.Client, 0, 0)
		})
	}
}

func TestKubernetesManagerCopiesOperatorConfiguration(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	fixture.Config.Pod.Resources.Limits["cpu"] = "9"
	fixture.Config.Pod.ImagePullSecrets[0] = "caller-changed"
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err != nil {
		t.Fatal(err)
	}
	pod := fixture.Client.Snapshot()
	if strings.Contains(string(pod.Spec), "caller-changed") || !strings.Contains(string(pod.Spec), `"cpu":"1"`) {
		t.Fatal("caller mutation changed the manager's deployment configuration")
	}
}

func TestKubernetesManagerReconcilesLostAPIMutationResponses(t *testing.T) {
	for _, phase := range []string{"create", "release"} {
		t.Run(phase, func(t *testing.T) {
			fixture := fixtures.NewKubernetesManager(t)
			failure := errors.New("fixture lost accepted " + phase + " response")
			if phase == "create" {
				fixture.Client.SetCreateError(failure)
			} else {
				fixture.Client.SetReleaseError(failure)
			}
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			_, _ = fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

			result, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

			if err != nil || result.Runtime != runtimes.RuntimeKubernetes || result.AgentID != fixture.Client.Snapshot().Metadata.Labels["ai.defos.commander.agent-id"] {
				t.Fatalf("retry did not reconcile the accepted %s: %+v, %v", phase, result, err)
			}
			assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
		})
	}
}

func TestKubernetesManagerRetainsAcknowledgedCreateUIDBeforeReceipt(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	fixture.Client.SetOnCreate(func(context.Context, kubernetes.Pod) error {
		fixture.Client.Change(func(pod *kubernetes.Pod) { pod.Metadata.UID = "replacement-before-receipt" })
		return nil
	})
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	for range 2 {
		if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
			t.Fatal("known Create UID was discarded before its immutable receipt")
		}
	}
	if data, found, err := journal.LoadCheckpoint(ctx, "launch-receipt"); err != nil ||
		found && (!bytes.Contains(data, []byte("fixture-pod-uid")) || bytes.Contains(data, []byte("replacement-before-receipt"))) {
		t.Fatalf("replacement changed the acknowledged Pod identity: found=%t error=%v", found, err)
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 0)
}

func TestKubernetesManagerPinsFirstSuccessfulReleaseInstance(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	fixture.Client.SetOnRelease(func(ctx context.Context, pod kubernetes.Pod) error {
		fixture.Client.Change(func(current *kubernetes.Pod) {
			current.Status = bytes.Replace(current.Status, []byte("containerd://fixture-1"), []byte("containerd://replacement"), 1)
		})
		return fixtures.PublishKubernetesRegistration(ctx, fixture.Bus, pod, nil)
	})
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
		t.Fatal("a later GET replaced the first observed running container identity")
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
}

func TestKubernetesManagerRestartCannotActivateGatedPod(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	fixture.Client.SetCreateError(errors.New("fixture lost Create response"))
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
		t.Fatal("fixture did not leave an uncertain gated Pod")
	}
	other, err := kubernetes.NewManager(t.Context(), fixture.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Spawn(ctx, requestID, fixtures.Reference); err == nil {
		t.Fatal("saved intent transferred activation authority to another manager")
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 0)
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err != nil {
		t.Fatalf("original owner could not reconcile its gated Pod: %v", err)
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
}

func TestKubernetesManagerRestartRequiresFreshRegistration(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)

	first, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

	if err != nil {
		t.Fatal(err)
	}
	restarted := fixtures.RestartKubernetesManager(t, fixture)
	waiting, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	if _, err := restarted.Spawn(waiting, requestID, fixtures.Reference); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("restarted owner accepted cached registration: %v", err)
	}
	if err := fixtures.PublishKubernetesRegistration(ctx, fixture.Bus, fixture.Client.Snapshot(), nil); err != nil {
		t.Fatal(err)
	}

	result, err := restarted.Spawn(ctx, requestID, fixtures.Reference)

	if err != nil || result.AgentID != first.AgentID || result.RuntimeDetails != first.RuntimeDetails {
		t.Fatalf("restart could not observe original running Pod: %+v, %v", result, err)
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
}

func TestKubernetesManagerRejectsStaleFutureOrWrongTypeRegistration(t *testing.T) {
	for _, kind := range []string{"stale", "future", "wrong type"} {
		t.Run(kind, func(t *testing.T) {
			fixture := fixtures.NewKubernetesManager(t)
			fixture.Client.SetOnRelease(func(ctx context.Context, pod kubernetes.Pod) error {
				message, err := fixtures.KubernetesRegistration(pod, func(snapshot *agent.AgentSnapshot) {
					if kind == "wrong type" {
						snapshot.Type = "other-agent"
					}
				})
				if err != nil {
					return err
				}
				if kind == "stale" {
					message.CreatedAt = time.Now().Add(-time.Minute)
				} else if kind == "future" {
					message.CreatedAt = time.Now().Add(time.Minute)
				}
				return fixture.Bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message)
			})
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			waiting, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
			defer cancel()
			_, err := fixture.Manager.Spawn(waiting, requestID, fixtures.Reference)
			if err == nil || kind != "wrong type" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("invalid registration was accepted or not ignored: %v", err)
			}
			if kind != "wrong type" {
				if err := fixtures.PublishKubernetesRegistration(ctx, fixture.Bus, fixture.Client.Snapshot(), nil); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err != nil {
					t.Fatalf("valid late heartbeat was not observed: %v", err)
				}
			}
			assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
		})
	}
}

func TestKubernetesManagerRequiresRunningPodAndMatchingRegistration(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	fixture.Client.SetKeepPending(true)
	released, pendingObserved := make(chan struct{}), make(chan struct{})
	var observedOnce sync.Once
	fixture.Client.SetOnGet(func(pod kubernetes.Pod) {
		if !strings.Contains(string(pod.Spec), kubernetes.SchedulingGate) && strings.Contains(string(pod.Status), `"Pending"`) {
			observedOnce.Do(func() { close(pendingObserved) })
		}
	})
	fixture.Client.SetOnRelease(func(ctx context.Context, pod kubernetes.Pod) error {
		defer close(released)
		return fixtures.PublishKubernetesRegistration(ctx, fixture.Bus, pod, nil)
	})
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	finished := fixtures.SpawnKubernetesAsync(t, fixture.Manager, ctx, requestID)
	for _, signal := range []<-chan struct{}{released, pendingObserved} {
		select {
		case <-signal:
		case err := <-finished:
			t.Fatalf("registration alone completed a pending Pod: %v", err)
		case <-ctx.Done():
			t.Fatal("pending Pod was not observed")
		}
	}
	select {
	case err := <-finished:
		t.Fatalf("pending image startup was reported ready: %v", err)
	default:
	}
	fixture.Client.Change(fakes.SetPodRunning)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("running Pod with matching registration did not become ready: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("running Pod did not become ready")
	}
}

func TestKubernetesManagerRefusesMissingReplacedAndRestartedInstances(t *testing.T) {
	for _, reason := range []string{"missing", "replaced UID", "changed container", "changed start", "restart count", "terminal"} {
		t.Run(reason, func(t *testing.T) {
			fixture := fixtures.NewKubernetesManager(t)
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err != nil {
				t.Fatal(err)
			}
			if reason == "missing" {
				fixture.Client.Forget()
			} else {
				fixture.Client.Change(func(pod *kubernetes.Pod) {
					switch reason {
					case "replaced UID":
						pod.Metadata.UID = "replacement-uid"
					case "changed container":
						pod.Status = bytes.Replace(pod.Status, []byte("containerd://fixture-1"), []byte("containerd://replacement"), 1)
					case "changed start":
						pod.Status = bytes.Replace(pod.Status, []byte("10:00:00Z"), []byte("10:00:01Z"), 1)
					case "restart count":
						pod.Status = bytes.Replace(pod.Status, []byte(`"restartCount":0`), []byte(`"restartCount":1`), 1)
					case "terminal":
						pod.Status = json.RawMessage(`{"phase":"Succeeded"}`)
					}
				})
			}
			if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
				t.Fatal("retry accepted a missing, replaced or restarted child")
			}
			assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
		})
	}
}

func TestKubernetesManagerRetainsLateRegistrationAfterActionCancellation(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	activated := make(chan struct{})
	fixture.Client.SetOnRelease(func(context.Context, kubernetes.Pod) error { close(activated); return nil })
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	waiting, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := fixtures.SpawnKubernetesAsync(t, fixture.Manager, waiting, requestID)
	select {
	case <-activated:
	case err := <-finished:
		t.Fatalf("launch returned before activation: %v", err)
	case <-time.After(time.Second):
		t.Fatal("launch never activated")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("action cancellation was lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled action kept waiting")
	}
	if err := fixtures.PublishKubernetesRegistration(ctx, fixture.Bus, fixture.Client.Snapshot(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err != nil {
		t.Fatalf("retry missed host-owned late registration: %v", err)
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
}

func TestKubernetesManagerCloseCancelsWaitAndRetainsPod(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	activated := make(chan struct{})
	fixture.Client.SetOnRelease(func(context.Context, kubernetes.Pod) error { close(activated); return nil })
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	finished := fixtures.SpawnKubernetesAsync(t, fixture.Manager, ctx, requestID)
	select {
	case <-activated:
	case err := <-finished:
		t.Fatalf("launch returned before activation: %v", err)
	case <-time.After(time.Second):
		t.Fatal("launch never activated")
	}
	if err := fixture.Manager.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("closed manager returned a successful launch")
		}
	case <-time.After(time.Second):
		t.Fatal("manager Close did not end its registration wait")
	}
	if fixture.Client.Snapshot().Metadata.UID == "" {
		t.Fatal("manager Close removed the child Pod")
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 1, 1)
}

func TestKubernetesManagerRejectsInvalidConfiguration(t *testing.T) {
	fixture := fixtures.NewKubernetesManager(t)
	for _, test := range []struct {
		name string
		edit func(*kubernetes.ManagerConfig)
	}{
		{"missing bus", func(c *kubernetes.ManagerConfig) { c.Bus = nil }},
		{"missing Pod client", func(c *kubernetes.ManagerConfig) { c.Client = nil }},
		{"invalid namespace", func(c *kubernetes.ManagerConfig) { c.Pod.Namespace = "../agents" }},
		{"negative registration timeout", func(c *kubernetes.ManagerConfig) { c.RegistrationTimeout = -time.Second }},
		{"negative poll interval", func(c *kubernetes.ManagerConfig) { c.PollInterval = -time.Second }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := fixture.Config
			test.edit(&config)
			manager, err := kubernetes.NewManager(t.Context(), config)
			if err == nil {
				_ = manager.Close()
				t.Fatal("invalid manager configuration was accepted")
			}
		})
	}
	assertKubernetesLaunchCounts(t, fixture.Client, 0, 0)
}
