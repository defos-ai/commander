package packages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

const dockerFixtureImage = "ghcr.io/defos-ai/fixture-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeDockerEngine struct {
	mu        sync.Mutex
	current   map[string]docker.Container
	created   chan docker.Spec
	onStart   func(context.Context, docker.Container) error
	pullErr   error
	createErr error
	startErr  error
	pulls     int
	creates   int
	starts    int
	closes    int
}

func (e *fakeDockerEngine) Pull(ctx context.Context, image string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pulls++
	if image != dockerFixtureImage {
		return errors.New("fixture received unexpected image")
	}
	if e.pullErr != nil {
		return e.pullErr
	}
	return ctx.Err()
}

func (e *fakeDockerEngine) Inspect(ctx context.Context, name string) (docker.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return docker.Container{}, err
	}
	for _, current := range e.current {
		if current.Name == name || current.ID == name {
			return copyDockerContainer(current), nil
		}
	}
	return docker.Container{}, docker.ErrNotFound
}

func (e *fakeDockerEngine) Create(ctx context.Context, spec docker.Spec) (docker.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return docker.Container{}, err
	}
	e.creates++
	if _, exists := e.current[spec.Name]; exists {
		return docker.Container{}, errors.New("fixture container name already exists")
	}
	current := docker.Container{
		ID: fmt.Sprintf("%064x", e.creates), Name: spec.Name, Image: spec.Image,
		Labels: maps.Clone(spec.Labels), Environment: maps.Clone(spec.Environment),
		Networks: []string{spec.Network}, Status: "created", Safe: true,
	}
	e.current[spec.Name] = current
	spec.Environment, spec.Labels = maps.Clone(spec.Environment), maps.Clone(spec.Labels)
	e.created <- spec
	err := e.createErr
	e.createErr = nil
	if err != nil {
		return docker.Container{}, err
	}
	return copyDockerContainer(current), nil
}

func (e *fakeDockerEngine) Start(ctx context.Context, containerID string) error {
	e.mu.Lock()
	var current docker.Container
	for name, candidate := range e.current {
		if candidate.ID == containerID {
			candidate.Status = "running"
			e.current[name], current = candidate, copyDockerContainer(candidate)
			break
		}
	}
	if current.ID == "" {
		e.mu.Unlock()
		return docker.ErrNotFound
	}
	e.starts++
	callback, err := e.onStart, e.startErr
	e.startErr = nil
	e.mu.Unlock()
	if callback != nil {
		if publishErr := callback(ctx, current); publishErr != nil {
			return publishErr
		}
	}
	return err
}

func (e *fakeDockerEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closes++
	return nil
}

func (e *fakeDockerEngine) change(change func(*docker.Container)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for name, current := range e.current {
		change(&current)
		e.current[name] = current
	}
}

func (e *fakeDockerEngine) counts() (creates, starts, closes int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.creates, e.starts, e.closes
}

func copyDockerContainer(current docker.Container) docker.Container {
	current.Labels = maps.Clone(current.Labels)
	current.Environment = maps.Clone(current.Environment)
	current.Networks = append([]string(nil), current.Networks...)
	return current
}

type dockerManagerFixture struct {
	manager *docker.Manager
	config  docker.Config
	engine  *fakeDockerEngine
	bus     *fscomms.FsBus
}

func newDockerManagerFixture(t *testing.T) dockerManagerFixture {
	t.Helper()
	root := t.TempDir()
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	engine := &fakeDockerEngine{current: make(map[string]docker.Container), created: make(chan docker.Spec, 10)}
	engine.onStart = func(ctx context.Context, current docker.Container) error {
		return publishDockerRegistration(ctx, bus, current, nil)
	}
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != fixtureReleaseURL+"agent.yaml" && request.URL.String() != fixtureModuleReleaseURL+"agent.yaml" {
			return response(request, http.StatusNotFound, "not found"), nil
		}
		manifest := "schema_version: 1\ntype: fixture-agent\nversion: v0.1.0\ndocker:\n  image: " + dockerFixtureImage + "\n"
		return response(request, http.StatusOK, manifest), nil
	}))
	t.Cleanup(resolver.CloseIdleConnections)
	config := docker.Config{
		Directory: filepath.Join(root, "spawner"), Bus: bus,
		BusURL: "amqp://fixture/", StorageURL: "s3://fixture", Network: "fixture-network",
		Engine: engine, Resolver: resolver, RegistrationTimeout: time.Second,
		Environment: map[string]string{"AWS_REGION": "fixture-region", "AWS_SECRET_ACCESS_KEY": "private-storage-sentinel"},
	}
	manager, err := docker.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return dockerManagerFixture{manager: manager, config: config, engine: engine, bus: bus}
}

func publishDockerRegistration(ctx context.Context, bus *fscomms.FsBus, current docker.Container, change func(*agent.AgentSnapshot)) error {
	snapshot := agent.AgentSnapshot{
		ID: "fixture-agent-" + current.Environment["COMMANDER_AGENT_ID_SUFFIX"], Type: "fixture-agent", Status: agent.AgentReady,
		Server:  agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"},
		Actions: []actionmodel.Action{{Name: "fixture-action", Status: actionmodel.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
	}
	if change != nil {
		change(&snapshot)
	}
	message, err := messages.NewMessage(messages.AgentHeartbeat, snapshot.ID, snapshot)
	if err != nil {
		return err
	}
	return bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message)
}

func restartDockerManager(t *testing.T, fixture dockerManagerFixture) *docker.Manager {
	t.Helper()
	if err := fixture.manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := docker.NewManager(t.Context(), fixture.config)
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

func TestDockerManagerOwnsStateDirectoryUntilClose(t *testing.T) {
	fixture := newDockerManagerFixture(t)
	second, err := docker.NewManager(t.Context(), fixture.config)
	if err == nil {
		_ = second.Close()
		t.Fatal("two Docker spawner hosts acquired the same state directory")
	}
	if !strings.Contains(err.Error(), "another Docker spawner") {
		t.Fatalf("expected immediate ownership conflict, got %v", err)
	}
	if err := fixture.manager.Close(); err != nil {
		t.Fatal(err)
	}
	second, err = docker.NewManager(t.Context(), fixture.config)
	if err != nil {
		t.Fatalf("closed manager left a stale state lock: %v", err)
	}
	defer second.Close()
	// Closing the old host again must not release the new host's ownership.
	if err := fixture.manager.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := docker.NewManager(t.Context(), fixture.config)
	if err == nil {
		_ = third.Close()
		t.Fatal("closed manager released a later host's state lock")
	}
}

func TestDockerManagerRejectsInvalidConfigurationBeforeState(t *testing.T) {
	fixture := newDockerManagerFixture(t)
	for _, test := range []struct {
		name   string
		change func(*docker.Config)
	}{
		{"missing bus", func(c *docker.Config) { c.Bus = nil }},
		{"missing engine", func(c *docker.Config) { c.Engine = nil }},
		{"blank directory", func(c *docker.Config) { c.Directory = " " }},
		{"host network", func(c *docker.Config) { c.Network = "host" }},
		{"filesystem bus", func(c *docker.Config) { c.BusURL = "file:///state/bus" }},
		{"filesystem storage", func(c *docker.Config) { c.StorageURL = "file:///state/storage" }},
		{"missing bus host", func(c *docker.Config) { c.BusURL = "amqp:" }},
		{"missing storage bucket", func(c *docker.Config) { c.StorageURL = "s3:" }},
		{"invalid URL", func(c *docker.Config) { c.BusURL = "amqp://private-config-sentinel@host%/" }},
		{"negative timeout", func(c *docker.Config) { c.RegistrationTimeout = -time.Second }},
		{"inherited key", func(c *docker.Config) {
			c.Environment = map[string]string{"SECRETS_ENCRYPTION_KEY": "private-config-sentinel"}
		}},
		{"inherited home", func(c *docker.Config) { c.Environment = map[string]string{"HOME": "private-config-sentinel"} }},
		{"invalid environment", func(c *docker.Config) {
			c.Environment = map[string]string{"AWS_REGION": "private-config-sentinel\x00"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := fixture.config
			directory := filepath.Join(t.TempDir(), "unused")
			config.Directory = directory
			test.change(&config)
			manager, err := docker.NewManager(t.Context(), config)
			if err == nil {
				_ = manager.Close()
				t.Fatal("invalid Docker launch configuration was accepted")
			}
			if strings.Contains(err.Error(), "private-config-sentinel") {
				t.Fatal("configuration error disclosed a bootstrap value")
			}
			if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid configuration created launch state")
			}
		})
	}
}

// A running container sends heartbeats independently of the spawner's lifetime.
func dockerHeartbeats(t *testing.T, fixture dockerManagerFixture, name string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			current, err := fixture.engine.Inspect(ctx, name)
			if err == nil {
				err = publishDockerRegistration(ctx, fixture.bus, current, nil)
			}
			if err != nil {
				done <- err
				return
			}
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}
}

func TestDockerManagerAssignsIdentityAndReusesOwnedContainer(t *testing.T) {
	for _, name := range []string{"SECRETS_ENCRYPTION_KEY", "API_PASSWORD", "MCP_TOKEN", "OPENAI_API_KEY"} {
		t.Setenv(name, "private-parent-sentinel")
	}
	fixture := newDockerManagerFixture(t)
	requestID := "task:" + id.New()
	first, err := fixture.manager.Spawn(t.Context(), requestID, fixtureReference)
	if err != nil {
		t.Fatal(err)
	}
	if first.Runtime != "docker" || first.ContainerID == "" || first.PID != 0 || first.Package != fixtureReference || first.AgentType != "fixture-agent" || !id.ValidAgent(first.AgentID) {
		t.Fatalf("unexpected Docker result: %+v", first)
	}
	if len(first.Actions) != 1 || first.Actions[0].Name != "fixture-action" || first.Actions[0].Status != actionmodel.Ready {
		t.Fatalf("missing observed action readiness: %+v", first.Actions)
	}
	spec := <-fixture.engine.created
	if spec.Name != "commander-"+first.AgentID || spec.Image != dockerFixtureImage || spec.Network != fixture.config.Network {
		t.Fatal("container did not use its assigned identity, pinned image, and configured network")
	}
	if len(spec.Environment["COMMANDER_AGENT_ID_SUFFIX"]) != 8 || first.AgentID != "fixture-agent-"+spec.Environment["COMMANDER_AGENT_ID_SUFFIX"] {
		t.Fatal("container did not receive its assigned suffix")
	}
	if spec.Environment["BUS_URL"] != fixture.config.BusURL || spec.Environment["STORAGE_URL"] != fixture.config.StorageURL || spec.Environment["AWS_SECRET_ACCESS_KEY"] != "private-storage-sentinel" {
		t.Fatal("provider bootstrap configuration was not passed to container")
	}
	for _, name := range []string{"COMMANDER_AGENT_ID", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD", "MCP_TOKEN", "OPENAI_API_KEY", "PATH", "HOME"} {
		if _, exists := spec.Environment[name]; exists {
			t.Errorf("container inherited %s", name)
		}
	}
	second, err := fixture.manager.Spawn(t.Context(), requestID, fixtureReference)
	if err != nil || second.AgentID != first.AgentID || second.ContainerID != first.ContainerID {
		t.Fatalf("same request did not reuse its container: result=%+v error=%v", second, err)
	}
	if _, err := fixture.manager.Spawn(t.Context(), requestID, "github.com/defos-ai/other-agent@v0.1.0"); err == nil {
		t.Fatal("existing request accepted a conflicting package")
	}
	if err := filepath.WalkDir(fixture.config.Directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && (bytes.Contains(data, []byte("private-parent-sentinel")) || bytes.Contains(data, []byte("private-storage-sentinel"))) {
			t.Error("launch state persisted a bootstrap secret")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Close(); err != nil {
		t.Fatal(err)
	}
	creates, starts, closes := fixture.engine.counts()
	if creates != 1 || starts != 1 || closes != 0 {
		t.Fatalf("unexpected lifecycle counts: create=%d start=%d engine close=%d", creates, starts, closes)
	}
	current, err := fixture.engine.Inspect(t.Context(), first.ContainerID)
	if err != nil || current.Status != "running" {
		t.Fatal("manager shutdown did not retain the running container")
	}
}

func TestDockerManagerRestartAdoptsRunningContainerAfterFreshRegistration(t *testing.T) {
	fixture := newDockerManagerFixture(t)
	requestID := "task:" + id.New()
	first, err := fixture.manager.Spawn(t.Context(), requestID, fixtureModuleReference)
	if err != nil {
		t.Fatal(err)
	}
	restarted := restartDockerManager(t, fixture)
	if _, err := restarted.Spawn(t.Context(), requestID, fixtureReference); err == nil || !strings.Contains(err.Error(), "different package") {
		t.Fatalf("record did not preserve its module package: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := restarted.Spawn(ctx, requestID, fixtureModuleReference); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("restarted manager did not wait for fresh registration: %v", err)
	}
	stop := dockerHeartbeats(t, fixture, first.ContainerID)
	defer stop()
	result, err := restarted.Spawn(t.Context(), requestID, fixtureModuleReference)
	if err != nil || result.AgentID != first.AgentID || result.ContainerID != first.ContainerID || result.Package != fixtureModuleReference {
		t.Fatalf("restart did not adopt original container: result=%+v error=%v", result, err)
	}
	creates, starts, _ := fixture.engine.counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("restart relaunched a running instance: create=%d start=%d", creates, starts)
	}
}

func TestDockerManagerRefusesUncertainOrChangedContainer(t *testing.T) {
	cases := map[string]func(*fakeDockerEngine){
		"missing": func(engine *fakeDockerEngine) {
			engine.mu.Lock()
			clear(engine.current)
			engine.mu.Unlock()
		},
		"exited": func(engine *fakeDockerEngine) { engine.change(func(c *docker.Container) { c.Status = "exited" }) },
		"unsafe": func(engine *fakeDockerEngine) { engine.change(func(c *docker.Container) { c.Safe = false }) },
		"changed image": func(engine *fakeDockerEngine) {
			engine.change(func(c *docker.Container) { c.Image = strings.Replace(c.Image, "aaaa", "bbbb", 1) })
		},
		"changed network": func(engine *fakeDockerEngine) {
			engine.change(func(c *docker.Container) { c.Networks = []string{"different-network"} })
		},
		"changed ownership": func(engine *fakeDockerEngine) {
			engine.change(func(c *docker.Container) { c.Labels = map[string]string{} })
		},
		"changed suffix": func(engine *fakeDockerEngine) {
			engine.change(func(c *docker.Container) { c.Environment["COMMANDER_AGENT_ID_SUFFIX"] = "invalid" })
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newDockerManagerFixture(t)
			requestID := "task:" + id.New()
			if _, err := fixture.manager.Spawn(t.Context(), requestID, fixtureReference); err != nil {
				t.Fatal(err)
			}
			change(fixture.engine)
			restarted := restartDockerManager(t, fixture)
			if _, err := restarted.Spawn(t.Context(), requestID, fixtureReference); err == nil {
				t.Fatal("manager trusted a missing, stopped, unsafe, or changed container")
			}
			creates, starts, _ := fixture.engine.counts()
			if creates != 1 || starts != 1 {
				t.Fatalf("uncertain request created or restarted a container: create=%d start=%d", creates, starts)
			}
		})
	}
}

func TestDockerManagerCancelledWaitRetainsLateRegistration(t *testing.T) {
	fixture := newDockerManagerFixture(t)
	fixture.engine.onStart = nil
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	requestID := "task:" + id.New()
	finished := make(chan error, 1)
	go func() {
		_, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
		finished <- err
	}()
	var spec docker.Spec
	select {
	case spec = <-fixture.engine.created:
	case <-time.After(2 * time.Second):
		t.Fatal("container was not created")
	}
	// Wait for Start, so cancellation exercises registration waiting rather than creation.
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		_, starts, _ := fixture.engine.counts()
		if starts > 0 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("container was not started")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled registration wait returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled registration wait did not return")
	}
	current, err := fixture.engine.Inspect(t.Context(), spec.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishDockerRegistration(t.Context(), fixture.bus, current, nil); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.manager.Spawn(t.Context(), requestID, fixtureReference)
	if err != nil || result.ContainerID != current.ID {
		t.Fatalf("retry missed late registration: result=%+v error=%v", result, err)
	}
	creates, starts, _ := fixture.engine.counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("cancelled wait caused duplicate launch: create=%d start=%d", creates, starts)
	}
}

func TestDockerManagerReconcilesUncertainCreateAndStart(t *testing.T) {
	for _, phase := range []string{"create", "start"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newDockerManagerFixture(t)
			if phase == "create" {
				fixture.engine.createErr = errors.New("fixture lost Create reply after daemon accepted it")
			} else {
				fixture.engine.startErr = errors.New("fixture lost Start reply after daemon accepted it")
			}
			requestID := "task:" + id.New()
			_, _ = fixture.manager.Spawn(t.Context(), requestID, fixtureReference)
			result, err := fixture.manager.Spawn(t.Context(), requestID, fixtureReference)
			if err != nil || result.Runtime != "docker" || result.ContainerID == "" {
				t.Fatalf("retry did not reconcile accepted %s: result=%+v error=%v", phase, result, err)
			}
			creates, starts, _ := fixture.engine.counts()
			if creates != 1 || starts != 1 {
				t.Fatalf("ambiguous %s launched twice: create=%d start=%d", phase, creates, starts)
			}
		})
	}
}

func TestDockerManagerRejectsInvalidRegistration(t *testing.T) {
	for name, change := range map[string]func(*agent.AgentSnapshot){
		"wrong identity": func(snapshot *agent.AgentSnapshot) { snapshot.ID = "other-agent-12345678" },
		"wrong type":     func(snapshot *agent.AgentSnapshot) { snapshot.Type = "other-agent" },
		"invalid action": func(snapshot *agent.AgentSnapshot) { snapshot.Actions[0].InputSchema = json.RawMessage(`[]`) },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newDockerManagerFixture(t)
			fixture.engine.onStart = func(ctx context.Context, current docker.Container) error {
				return publishDockerRegistration(ctx, fixture.bus, current, change)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if result, err := fixture.manager.Spawn(ctx, "task:"+id.New(), fixtureReference); err == nil {
				t.Fatalf("invalid registration was accepted: %+v", result)
			}
		})
	}
}

var _ runtimes.Runtime = (*docker.Manager)(nil)
