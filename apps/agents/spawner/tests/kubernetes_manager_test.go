package packages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

type fakePodClient struct {
	mu                      sync.Mutex
	current                 *kubernetes.Pod
	creates, releases, gets int
	closes                  int
	createErr, releaseErr   error
	beforeCreate            func(context.Context, kubernetes.Pod) error
	onCreate                func(context.Context, kubernetes.Pod) error
	beforeRelease           func(context.Context, kubernetes.Pod) error
	onRelease               func(context.Context, kubernetes.Pod) error
	onGet                   func(kubernetes.Pod)
	keepPending             bool
}

func (client *fakePodClient) Create(ctx context.Context, desired kubernetes.Pod) (kubernetes.Pod, error) {
	if client.beforeCreate != nil {
		if err := client.beforeCreate(ctx, desired); err != nil {
			return kubernetes.Pod{}, err
		}
	}
	client.mu.Lock()
	client.creates++
	if client.current != nil {
		client.mu.Unlock()
		return kubernetes.Pod{}, kubernetes.ErrConflict
	}
	pod := cloneManagerPod(desired)
	pod.Metadata.UID, pod.Metadata.ResourceVersion = "fixture-pod-uid", "1"
	pod.Status = json.RawMessage(`{"phase":"Pending"}`)
	client.current = &pod
	result, hook, failure := cloneManagerPod(pod), client.onCreate, client.createErr
	client.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, cloneManagerPod(pod)); err != nil {
			return kubernetes.Pod{}, err
		}
	}
	if failure != nil {
		return kubernetes.Pod{}, failure
	}
	return result, ctx.Err()
}

func (client *fakePodClient) Get(ctx context.Context, name string) (kubernetes.Pod, error) {
	client.mu.Lock()
	client.gets++
	if err := ctx.Err(); err != nil {
		client.mu.Unlock()
		return kubernetes.Pod{}, err
	}
	if client.current == nil || client.current.Metadata.Name != name {
		client.mu.Unlock()
		return kubernetes.Pod{}, kubernetes.ErrNotFound
	}
	pod, hook := cloneManagerPod(*client.current), client.onGet
	client.mu.Unlock()
	if hook != nil {
		hook(cloneManagerPod(pod))
	}
	return pod, nil
}

func (client *fakePodClient) ReleaseGate(ctx context.Context, observed kubernetes.Pod) (kubernetes.Pod, error) {
	if client.beforeRelease != nil {
		if err := client.beforeRelease(ctx, observed); err != nil {
			return kubernetes.Pod{}, err
		}
	}
	client.mu.Lock()
	client.releases++
	if client.current == nil || client.current.Metadata.UID != observed.Metadata.UID || client.current.Metadata.ResourceVersion != observed.Metadata.ResourceVersion {
		client.mu.Unlock()
		return kubernetes.Pod{}, kubernetes.ErrConflict
	}
	var spec map[string]json.RawMessage
	if err := json.Unmarshal(client.current.Spec, &spec); err != nil {
		client.mu.Unlock()
		return kubernetes.Pod{}, err
	}
	var gates []map[string]string
	if err := json.Unmarshal(spec["schedulingGates"], &gates); err != nil {
		client.mu.Unlock()
		return kubernetes.Pod{}, err
	}
	var remaining []map[string]string
	found := false
	for _, gate := range gates {
		if gate["name"] == kubernetes.SchedulingGate {
			found = true
		} else {
			remaining = append(remaining, gate)
		}
	}
	if !found {
		client.mu.Unlock()
		return kubernetes.Pod{}, errors.New("fixture refused to release an absent gate")
	}
	if len(remaining) == 0 {
		delete(spec, "schedulingGates")
	} else {
		spec["schedulingGates"], _ = json.Marshal(remaining)
	}
	client.current.Spec, _ = json.Marshal(spec)
	client.current.Metadata.ResourceVersion = "2"
	if !client.keepPending {
		setManagerPodRunning(client.current)
	}
	pod, hook, failure := cloneManagerPod(*client.current), client.onRelease, client.releaseErr
	client.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, cloneManagerPod(pod)); err != nil {
			return kubernetes.Pod{}, err
		}
	}
	if failure != nil {
		return kubernetes.Pod{}, failure
	}
	return pod, ctx.Err()
}

// The manager accepts a narrower interface and must leave this caller-owned
// client untouched, even though the fixture also happens to implement Close.
func (client *fakePodClient) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.closes++
	return nil
}

func (client *fakePodClient) counts() (creates, releases, gets, closes int) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.creates, client.releases, client.gets, client.closes
}

func (client *fakePodClient) snapshot() kubernetes.Pod {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.current == nil {
		return kubernetes.Pod{}
	}
	return cloneManagerPod(*client.current)
}

func (client *fakePodClient) change(edit func(*kubernetes.Pod)) {
	client.mu.Lock()
	defer client.mu.Unlock()
	edit(client.current)
}

func cloneManagerPod(pod kubernetes.Pod) kubernetes.Pod {
	pod.Spec, pod.Status = bytes.Clone(pod.Spec), bytes.Clone(pod.Status)
	pod.Metadata.Labels, pod.Metadata.Annotations = maps.Clone(pod.Metadata.Labels), maps.Clone(pod.Metadata.Annotations)
	pod.Metadata.Finalizers = append([]string(nil), pod.Metadata.Finalizers...)
	pod.Metadata.OwnerReferences = append([]json.RawMessage(nil), pod.Metadata.OwnerReferences...)
	for i := range pod.Metadata.OwnerReferences {
		pod.Metadata.OwnerReferences[i] = bytes.Clone(pod.Metadata.OwnerReferences[i])
	}
	if pod.Metadata.DeletionTimestamp != nil {
		value := *pod.Metadata.DeletionTimestamp
		pod.Metadata.DeletionTimestamp = &value
	}
	return pod
}

func setManagerPodRunning(pod *kubernetes.Pod) {
	pod.Status = json.RawMessage(`{"phase":"Running","containerStatuses":[{"name":"agent","containerID":"containerd://fixture-1","restartCount":0,"ready":true,"started":true,"state":{"running":{"startedAt":"2026-10-27T10:00:00Z"}}}]}`)
}

type managerPodBus struct {
	comms.Bus
	subscriptions atomic.Int64
}

func (bus *managerPodBus) Subscribe(ctx context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	subscription, err := bus.Bus.Subscribe(ctx, inbox)
	if err == nil {
		bus.subscriptions.Add(1)
	}
	return subscription, err
}

type kubernetesManagerFixture struct {
	manager *kubernetes.Manager
	config  kubernetes.ManagerConfig
	client  *fakePodClient
	bus     *managerPodBus
}

func newKubernetesManagerFixture(t *testing.T) kubernetesManagerFixture {
	t.Helper()
	bus, err := fscomms.NewFsBus(filepath.Join(t.TempDir(), "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	trackedBus := &managerPodBus{Bus: bus}
	client := &fakePodClient{}
	client.onRelease = func(ctx context.Context, pod kubernetes.Pod) error {
		return publishKubernetesRegistration(ctx, trackedBus, pod, nil)
	}
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		agentType := "fixture-agent"
		switch request.URL.String() {
		case fixtureReleaseURL + "agent.yaml", fixtureModuleReleaseURL + "agent.yaml":
		case fixtureBuilderReleaseURL + "agent.yaml":
			agentType = "builder-agent"
		default:
			return response(request, http.StatusNotFound, "not found"), nil
		}
		manifest := "schema_version: 1\ntype: " + agentType + "\nversion: v0.1.0\ndocker:\n  image: " + dockerFixtureImage + "\n"
		return response(request, http.StatusOK, manifest), nil
	}))
	t.Cleanup(resolver.CloseIdleConnections)
	config := kubernetes.ManagerConfig{Pod: kubernetesPodConfig(), Bus: trackedBus, Client: client, Resolver: resolver,
		RegistrationTimeout: time.Second, PollInterval: 5 * time.Millisecond}
	manager, err := kubernetes.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return kubernetesManagerFixture{manager: manager, config: config, client: client, bus: trackedBus}
}

func publishKubernetesRegistration(ctx context.Context, bus comms.Bus, pod kubernetes.Pod, edit func(*agent.AgentSnapshot)) error {
	message, err := kubernetesRegistration(pod, edit)
	if err != nil {
		return err
	}
	return bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message)
}

func kubernetesRegistration(pod kubernetes.Pod, edit func(*agent.AgentSnapshot)) (messages.Message, error) {
	agentID := pod.Metadata.Labels["ai.defos.commander.agent-id"]
	agentType, valid := id.RuntimeType(agentID, id.Agent)
	if !valid {
		return messages.Message{}, errors.New("fixture Pod lacks an assigned agent ID")
	}
	snapshot := agent.AgentSnapshot{ID: agentID, Type: agent.AgentType(agentType + "-agent"), Status: agent.AgentReady,
		Server:  agent.ServerInfo{Hostname: pod.Metadata.Name, IPAddress: "127.0.0.1"},
		Actions: []actionmodel.Action{{Name: "fixture-action", Status: actionmodel.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}}}
	if edit != nil {
		edit(&snapshot)
	}
	return messages.NewMessage(messages.AgentHeartbeat, snapshot.ID, snapshot)
}

func restartKubernetesManager(t *testing.T, fixture kubernetesManagerFixture) *kubernetes.Manager {
	t.Helper()
	if err := fixture.manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := kubernetes.NewManager(t.Context(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return manager
}

func assertKubernetesLaunchCounts(t *testing.T, client *fakePodClient, creates, releases int) {
	t.Helper()
	actualCreates, actualReleases, _, closes := client.counts()
	if actualCreates != creates || actualReleases != releases || closes != 0 {
		t.Fatalf("unexpected Pod lifecycle: create=%d release=%d client close=%d; want %d/%d/0", actualCreates, actualReleases, closes, creates, releases)
	}
}

func spawnKubernetesAsync(t *testing.T, manager *kubernetes.Manager, parent context.Context, requestID string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	finished, stopped := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(stopped)
		_, err := manager.Spawn(ctx, requestID, fixtureReference)
		finished <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Error("test launch did not end after cancellation")
		}
	})
	return finished
}

func TestKubernetesManagerPersistsIdentityBeforeActivation(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "private-parent-sentinel")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "private-parent-sentinel")
	fixture := newKubernetesManagerFixture(t)
	ctx, requestID, journal := newSpawnCheckpoints(t)
	var intentAcknowledged, receiptAcknowledged atomic.Bool
	journal.afterSave = func(_ context.Context, name string, _ json.RawMessage) error {
		if name == "launch-intent" {
			intentAcknowledged.Store(true)
		}
		if name == "launch-receipt" {
			receiptAcknowledged.Store(true)
		}
		return nil
	}
	fixture.client.beforeCreate = func(ctx context.Context, pod kubernetes.Pod) error {
		if !intentAcknowledged.Load() {
			return errors.New("Pod created before intent acknowledgement")
		}
		data, found, err := journal.LoadCheckpoint(ctx, "launch-intent")
		if err != nil || !found || !bytes.Contains(data, []byte(pod.Metadata.Name)) || !bytes.Contains(data, []byte(pod.Metadata.Namespace)) {
			return errors.New("launch intent omitted the fixed Pod destination")
		}
		return nil
	}
	fixture.client.beforeRelease = func(ctx context.Context, pod kubernetes.Pod) error {
		if !receiptAcknowledged.Load() || fixture.bus.subscriptions.Load() == 0 {
			return errors.New("Pod activated before durable UID receipt or registration subscription")
		}
		data, found, err := journal.LoadCheckpoint(ctx, "launch-receipt")
		if err != nil || !found || !bytes.Contains(data, []byte(pod.Metadata.UID)) {
			return errors.New("launch receipt omitted server-assigned Pod identity")
		}
		return nil
	}
	result, err := fixture.manager.Spawn(ctx, requestID, fixtureModuleReference)
	if err != nil {
		t.Fatal(err)
	}
	details, ok := result.RuntimeDetails.(runtimes.KubernetesRuntimeDetails)
	pod := fixture.client.snapshot()
	if result.Runtime != runtimes.RuntimeKubernetes || !ok || details.Namespace != pod.Metadata.Namespace || details.PodName != pod.Metadata.Name || details.PodUID != pod.Metadata.UID ||
		result.AgentID != pod.Metadata.Labels["ai.defos.commander.agent-id"] || result.Package != fixtureModuleReference || len(result.Actions) != 1 || result.Actions[0].Status != actionmodel.Ready {
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
	repeated, err := fixture.manager.Spawn(ctx, requestID, fixtureModuleReference)
	if err != nil || repeated.AgentID != pod.Metadata.Labels["ai.defos.commander.agent-id"] || repeated.RuntimeDetails != details || repeated.Actions[0].Name != "fixture-action" {
		t.Fatalf("retry lost the original child or shared mutable output: %+v, %v", repeated, err)
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
}

func TestKubernetesManagerRejectsInvalidLaunchesBeforeCreate(t *testing.T) {
	for _, reason := range []string{"missing checkpoints", "wrong request identity", "Builder"} {
		t.Run(reason, func(t *testing.T) {
			fixture := newKubernetesManagerFixture(t)
			ctx, requestID, _ := newSpawnCheckpoints(t)
			reference := fixtureReference
			switch reason {
			case "missing checkpoints":
				ctx = action.WithExecution(t.Context(), action.Execution{CommandID: strings.TrimPrefix(requestID, "command:")})
			case "wrong request identity":
				requestID = "command:" + id.MessageID()
			case "Builder":
				reference = fixtureBuilderReference
			}
			if _, err := fixture.manager.Spawn(ctx, requestID, reference); err == nil {
				t.Fatal("invalid launch was accepted")
			}
			assertKubernetesLaunchCounts(t, fixture.client, 0, 0)
		})
	}
}

func TestKubernetesManagerCopiesOperatorConfiguration(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	fixture.config.Pod.Resources.Limits["cpu"] = "9"
	fixture.config.Pod.ImagePullSecrets[0] = "caller-changed"
	ctx, requestID, _ := newSpawnCheckpoints(t)
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
		t.Fatal(err)
	}
	pod := fixture.client.snapshot()
	if strings.Contains(string(pod.Spec), "caller-changed") || !strings.Contains(string(pod.Spec), `"cpu":"1"`) {
		t.Fatal("caller mutation changed the manager's deployment configuration")
	}
}

func TestKubernetesManagerReconcilesLostAPIMutationResponses(t *testing.T) {
	for _, phase := range []string{"create", "release"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newKubernetesManagerFixture(t)
			failure := errors.New("fixture lost accepted " + phase + " response")
			if phase == "create" {
				fixture.client.createErr = failure
			} else {
				fixture.client.releaseErr = failure
			}
			ctx, requestID, _ := newSpawnCheckpoints(t)
			_, _ = fixture.manager.Spawn(ctx, requestID, fixtureReference)
			result, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
			if err != nil || result.Runtime != runtimes.RuntimeKubernetes || result.AgentID != fixture.client.snapshot().Metadata.Labels["ai.defos.commander.agent-id"] {
				t.Fatalf("retry did not reconcile the accepted %s: %+v, %v", phase, result, err)
			}
			assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
		})
	}
}

func TestKubernetesManagerRetainsAcknowledgedCreateUIDBeforeReceipt(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	fixture.client.onCreate = func(context.Context, kubernetes.Pod) error {
		fixture.client.change(func(pod *kubernetes.Pod) { pod.Metadata.UID = "replacement-before-receipt" })
		return nil
	}
	ctx, requestID, journal := newSpawnCheckpoints(t)
	for range 2 {
		if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
			t.Fatal("known Create UID was discarded before its immutable receipt")
		}
	}
	if data, found, err := journal.LoadCheckpoint(ctx, "launch-receipt"); err != nil ||
		found && (!bytes.Contains(data, []byte("fixture-pod-uid")) || bytes.Contains(data, []byte("replacement-before-receipt"))) {
		t.Fatalf("replacement changed the acknowledged Pod identity: found=%t error=%v", found, err)
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 0)
}

func TestKubernetesManagerPinsFirstSuccessfulReleaseInstance(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	fixture.client.onRelease = func(ctx context.Context, pod kubernetes.Pod) error {
		fixture.client.change(func(current *kubernetes.Pod) {
			current.Status = bytes.Replace(current.Status, []byte("containerd://fixture-1"), []byte("containerd://replacement"), 1)
		})
		return publishKubernetesRegistration(ctx, fixture.bus, pod, nil)
	}
	ctx, requestID, _ := newSpawnCheckpoints(t)
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
		t.Fatal("a later GET replaced the first observed running container identity")
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
}

func TestKubernetesManagerRestartCannotActivateGatedPod(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	fixture.client.createErr = errors.New("fixture lost Create response")
	ctx, requestID, _ := newSpawnCheckpoints(t)
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
		t.Fatal("fixture did not leave an uncertain gated Pod")
	}
	other, err := kubernetes.NewManager(t.Context(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Spawn(ctx, requestID, fixtureReference); err == nil {
		t.Fatal("saved intent transferred activation authority to another manager")
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 0)
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
		t.Fatalf("original owner could not reconcile its gated Pod: %v", err)
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
}

func TestKubernetesManagerRestartRequiresFreshRegistration(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	ctx, requestID, _ := newSpawnCheckpoints(t)
	first, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
	if err != nil {
		t.Fatal(err)
	}
	restarted := restartKubernetesManager(t, fixture)
	waiting, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	if _, err := restarted.Spawn(waiting, requestID, fixtureReference); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("restarted owner accepted cached registration: %v", err)
	}
	if err := publishKubernetesRegistration(ctx, fixture.bus, fixture.client.snapshot(), nil); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Spawn(ctx, requestID, fixtureReference)
	if err != nil || result.AgentID != first.AgentID || result.RuntimeDetails != first.RuntimeDetails {
		t.Fatalf("restart could not observe original running Pod: %+v, %v", result, err)
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
}

func TestKubernetesManagerRejectsStaleFutureOrWrongTypeRegistration(t *testing.T) {
	for _, kind := range []string{"stale", "future", "wrong type"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newKubernetesManagerFixture(t)
			fixture.client.onRelease = func(ctx context.Context, pod kubernetes.Pod) error {
				message, err := kubernetesRegistration(pod, func(snapshot *agent.AgentSnapshot) {
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
				return fixture.bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message)
			}
			ctx, requestID, _ := newSpawnCheckpoints(t)
			waiting, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
			defer cancel()
			_, err := fixture.manager.Spawn(waiting, requestID, fixtureReference)
			if err == nil || kind != "wrong type" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("invalid registration was accepted or not ignored: %v", err)
			}
			if kind != "wrong type" {
				if err := publishKubernetesRegistration(ctx, fixture.bus, fixture.client.snapshot(), nil); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
					t.Fatalf("valid late heartbeat was not observed: %v", err)
				}
			}
			assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
		})
	}
}

func TestKubernetesManagerRequiresRunningPodAndMatchingRegistration(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	fixture.client.keepPending = true
	released, pendingObserved := make(chan struct{}), make(chan struct{})
	var observedOnce sync.Once
	fixture.client.onGet = func(pod kubernetes.Pod) {
		if !strings.Contains(string(pod.Spec), kubernetes.SchedulingGate) && strings.Contains(string(pod.Status), `"Pending"`) {
			observedOnce.Do(func() { close(pendingObserved) })
		}
	}
	fixture.client.onRelease = func(ctx context.Context, pod kubernetes.Pod) error {
		defer close(released)
		return publishKubernetesRegistration(ctx, fixture.bus, pod, nil)
	}
	ctx, requestID, _ := newSpawnCheckpoints(t)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	finished := spawnKubernetesAsync(t, fixture.manager, ctx, requestID)
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
	fixture.client.change(setManagerPodRunning)
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
			fixture := newKubernetesManagerFixture(t)
			ctx, requestID, _ := newSpawnCheckpoints(t)
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
				t.Fatal(err)
			}
			fixture.client.mu.Lock()
			if reason == "missing" {
				fixture.client.current = nil
			} else {
				pod := fixture.client.current
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
			}
			fixture.client.mu.Unlock()
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
				t.Fatal("retry accepted a missing, replaced or restarted child")
			}
			assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
		})
	}
}

func TestKubernetesManagerRetainsLateRegistrationAfterActionCancellation(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	activated := make(chan struct{})
	fixture.client.onRelease = func(context.Context, kubernetes.Pod) error { close(activated); return nil }
	ctx, requestID, _ := newSpawnCheckpoints(t)
	waiting, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := spawnKubernetesAsync(t, fixture.manager, waiting, requestID)
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
	if err := publishKubernetesRegistration(ctx, fixture.bus, fixture.client.snapshot(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err != nil {
		t.Fatalf("retry missed host-owned late registration: %v", err)
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
}

func TestKubernetesManagerCloseCancelsWaitAndRetainsPod(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
	activated := make(chan struct{})
	fixture.client.onRelease = func(context.Context, kubernetes.Pod) error { close(activated); return nil }
	ctx, requestID, _ := newSpawnCheckpoints(t)
	finished := spawnKubernetesAsync(t, fixture.manager, ctx, requestID)
	select {
	case <-activated:
	case err := <-finished:
		t.Fatalf("launch returned before activation: %v", err)
	case <-time.After(time.Second):
		t.Fatal("launch never activated")
	}
	if err := fixture.manager.Close(); err != nil {
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
	if fixture.client.snapshot().Metadata.UID == "" {
		t.Fatal("manager Close removed the child Pod")
	}
	assertKubernetesLaunchCounts(t, fixture.client, 1, 1)
}

func TestKubernetesManagerRejectsInvalidConfiguration(t *testing.T) {
	fixture := newKubernetesManagerFixture(t)
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
			config := fixture.config
			test.edit(&config)
			manager, err := kubernetes.NewManager(t.Context(), config)
			if err == nil {
				_ = manager.Close()
				t.Fatal("invalid manager configuration was accepted")
			}
		})
	}
	assertKubernetesLaunchCounts(t, fixture.client, 0, 0)
}
