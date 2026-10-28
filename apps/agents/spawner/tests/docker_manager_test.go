package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

func dockerDetails(t *testing.T, result runtimes.Result) runtimes.DockerRuntimeDetails {
	t.Helper()
	details, ok := result.RuntimeDetails.(runtimes.DockerRuntimeDetails)
	if result.Runtime != runtimes.RuntimeDocker || !ok {
		t.Fatalf("expected Docker runtime details: %+v", result)
	}
	return details
}

func TestDockerManagerRejectsInvalidConfiguration(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	for _, test := range []struct {
		name   string
		change func(*docker.Config)
	}{
		{"missing bus", func(c *docker.Config) { c.Bus = nil }},
		{"missing engine", func(c *docker.Config) { c.Engine = nil }},
		{"host network", func(c *docker.Config) { c.Network = "host" }},
		{"filesystem bus", func(c *docker.Config) { c.BusURL = "file:///state/bus" }},
		{"filesystem storage", func(c *docker.Config) { c.StorageURL = "file:///state/storage" }},
		{"missing bus host", func(c *docker.Config) { c.BusURL = "amqp:" }},
		{"missing storage bucket", func(c *docker.Config) { c.StorageURL = "s3:" }},
		{"malformed storage escape", func(c *docker.Config) {
			c.StorageURL = "s3://key:private-config-sentinel/secret%zz@bucket/prefix"
		}},
		{"incomplete storage credentials", func(c *docker.Config) {
			c.StorageURL = "s3://:private-config-sentinel/secret@bucket/prefix"
		}},
		{"invalid storage bucket", func(c *docker.Config) {
			c.StorageURL = "s3://key:private-config-sentinel/secret@Bucket/prefix"
		}},
		{"storage query", func(c *docker.Config) {
			c.StorageURL = "s3://key:private-config-sentinel/secret@bucket/prefix?token=private-config-sentinel"
		}},
		{"invalid storage prefix", func(c *docker.Config) {
			c.StorageURL = "s3://key:private-config-sentinel/secret@bucket/../prefix"
		}},
		{"storage credential control character", func(c *docker.Config) {
			c.StorageURL = "s3://key:private-config-sentinel/secret%00@bucket/prefix"
		}},
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
			config := fixture.Config
			test.change(&config)
			manager, err := docker.NewManager(t.Context(), config)
			if err == nil {
				_ = manager.Close()
				t.Fatal("invalid Docker launch configuration was accepted")
			}
			if strings.Contains(err.Error(), "private-config-sentinel") {
				t.Fatal("configuration error disclosed a bootstrap value")
			}
		})
	}
}

func TestDockerManagerForwardsRawS3CredentialURL(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	if err := fixture.Manager.Close(); err != nil {
		t.Fatal(err)
	}
	const storageURL = "s3://key:raw/secret+=@bucket/prefix"
	config := fixture.Config
	config.StorageURL = storageURL
	manager, err := docker.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal("Docker manager rejected an S3 URL with a raw slash in its secret")
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)

	result, err := manager.Spawn(ctx, requestID, fixtures.Reference)

	if err != nil || result.Runtime != runtimes.RuntimeDocker || dockerDetails(t, result).ContainerID == "" || len(result.Actions) != 1 || result.Actions[0].Status != actionmodel.Ready {
		t.Fatal("Docker spawn did not register its child with raw S3 credentials")
	}
	spec := <-fixture.Engine.Created()
	if spec.Environment["STORAGE_URL"] != storageURL {
		t.Fatal("Docker spawn changed the credential-bearing storage URL")
	}
	assertCheckpointExcludes(t, journal, storageURL, "raw/secret+=")

	repeated, err := manager.Spawn(ctx, requestID, fixtures.Reference)

	if err != nil || repeated.AgentID != result.AgentID || dockerDetails(t, repeated).ContainerID != dockerDetails(t, result).ContainerID {
		t.Fatal("Docker spawn did not reconcile the existing child with raw S3 credentials")
	}
	if creates, starts, _ := fixture.Engine.Counts(); creates != 1 || starts != 1 {
		t.Fatal("retry launched another child with the same raw storage credentials")
	}
}

func TestDockerManagerAssignsIdentityAndReusesOwnedContainer(t *testing.T) {
	for _, name := range []string{"SECRETS_ENCRYPTION_KEY", "GITHUB_TOKEN", "API_PASSWORD", "MCP_TOKEN", "OPENAI_API_KEY"} {
		t.Setenv(name, "private-parent-sentinel")
	}
	fixture := fixtures.NewDockerManager(t)
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)

	first, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

	if err != nil {
		t.Fatal(err)
	}
	if first.Runtime != runtimes.RuntimeDocker || dockerDetails(t, first).ContainerID == "" || first.Package != fixtures.Reference || first.AgentType != "fixture-agent" || !id.ValidAgent(first.AgentID) {
		t.Fatalf("unexpected Docker result: %+v", first)
	}
	if len(first.Actions) != 1 || first.Actions[0].Name != "fixture-action" || first.Actions[0].Status != actionmodel.Ready {
		t.Fatalf("missing observed action readiness: %+v", first.Actions)
	}
	spec := <-fixture.Engine.Created()
	if spec.Name != "commander-"+first.AgentID || spec.Image != fixtures.DockerImage || spec.Network != fixture.Config.Network {
		t.Fatal("container did not use its assigned identity, pinned image, and configured network")
	}
	if len(spec.Environment["COMMANDER_AGENT_ID_SUFFIX"]) != 8 || first.AgentID != "fixture-agent-"+spec.Environment["COMMANDER_AGENT_ID_SUFFIX"] {
		t.Fatal("container did not receive its assigned suffix")
	}
	if spec.Environment["BUS_URL"] != fixture.Config.BusURL || spec.Environment["STORAGE_URL"] != fixture.Config.StorageURL || spec.Environment["AWS_SECRET_ACCESS_KEY"] != "private-storage-sentinel" {
		t.Fatal("provider bootstrap configuration was not passed to container")
	}
	for _, name := range []string{"COMMANDER_AGENT_ID", "SECRETS_ENCRYPTION_KEY", "GITHUB_TOKEN", "API_PASSWORD", "MCP_TOKEN", "OPENAI_API_KEY", "PATH", "HOME"} {
		if _, exists := spec.Environment[name]; exists {
			t.Errorf("container inherited %s", name)
		}
	}

	second, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

	if err != nil || second.AgentID != first.AgentID || dockerDetails(t, second).ContainerID != dockerDetails(t, first).ContainerID {
		t.Fatalf("same request did not reuse its container: result=%+v error=%v", second, err)
	}
	if _, err := fixture.Manager.Spawn(ctx, requestID, "github.com/defos-ai/other-agent@v0.1.0"); err == nil {
		t.Fatal("existing request accepted a conflicting package")
	}
	for _, name := range []string{"package-selection", "launch-intent"} {
		if _, found, err := journal.LoadCheckpoint(ctx, name); err != nil || !found {
			t.Fatalf("missing controller checkpoint %s: %v", name, err)
		}
	}
	assertCheckpointExcludes(t, journal, "private-parent-sentinel", "private-storage-sentinel")
	if err := fixture.Manager.Close(); err != nil {
		t.Fatal(err)
	}
	creates, starts, closes := fixture.Engine.Counts()
	if creates != 1 || starts != 1 || closes != 0 {
		t.Fatalf("unexpected lifecycle counts: create=%d start=%d engine close=%d", creates, starts, closes)
	}
	current, err := fixture.Engine.Inspect(t.Context(), dockerDetails(t, first).ContainerID)
	if err != nil || current.Status != "running" {
		t.Fatal("manager shutdown did not retain the running container")
	}
}

func TestDockerManagerRestartAdoptsRunningContainerAfterFreshRegistration(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)

	first, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

	if err != nil {
		t.Fatal(err)
	}
	restarted := fixtures.RestartDockerManager(t, fixture)
	if _, err := restarted.Spawn(ctx, requestID, fixtures.Reference); err == nil || !strings.Contains(err.Error(), "different package") {
		t.Fatalf("record did not preserve its module package: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := restarted.Spawn(waitCtx, requestID, fixtures.ModuleReferenceFixture); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("restarted manager did not wait for fresh registration: %v", err)
	}
	stop := fixtures.DockerHeartbeats(t, fixture, dockerDetails(t, first).ContainerID)
	defer stop()

	result, err := restarted.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

	if err != nil || result.AgentID != first.AgentID || dockerDetails(t, result).ContainerID != dockerDetails(t, first).ContainerID || result.Package != fixtures.ModuleReferenceFixture {
		t.Fatalf("restart did not adopt original container: result=%+v error=%v", result, err)
	}
	creates, starts, _ := fixture.Engine.Counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("restart relaunched a running instance: create=%d start=%d", creates, starts)
	}
}

func TestDockerManagerBootstrapsSecretsOnlyForTrustedBuilder(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	key := strings.Repeat("ab", 32)
	var err error
	fixture.Config.SecretBootstrap, err = runtimes.NewSecretBootstrap(key)
	if err != nil {
		t.Fatal(err)
	}
	fixture.Manager = fixtures.RestartDockerManager(t, fixture)
	for _, reference := range []string{fixtures.DockerBuilderReference, fixtures.DockerImpostorReference, fixtures.Reference} {
		ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
		if _, err := fixture.Manager.Spawn(ctx, requestID, reference); err != nil {
			t.Fatal(err)
		}
		assertCheckpointExcludes(t, journal, key)
		spec := <-fixture.Engine.Created()
		value, supplied := spec.Environment["SECRETS_ENCRYPTION_KEY"]
		if reference == fixtures.DockerBuilderReference {
			if !supplied || value != key {
				t.Fatal("trusted builder did not receive the deployment key")
			}
		} else if supplied {
			t.Fatal("untrusted package received the deployment key")
		}
	}
}

func TestDockerManagerReconcilesBuilderSecretsBootstrap(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	key := strings.Repeat("ab", 32)
	var err error
	fixture.Config.SecretBootstrap, err = runtimes.NewSecretBootstrap(key)
	if err != nil {
		t.Fatal(err)
	}
	fixture.Manager = fixtures.RestartDockerManager(t, fixture)
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)

	first, err := fixture.Manager.Spawn(ctx, requestID, fixtures.DockerBuilderReference)

	if err != nil {
		t.Fatal(err)
	}
	fixture.Manager = fixtures.RestartDockerManager(t, fixture)
	stop := fixtures.DockerHeartbeats(t, fixture, dockerDetails(t, first).ContainerID)

	repeated, err := fixture.Manager.Spawn(ctx, requestID, fixtures.DockerBuilderReference)
	stop()
	if err != nil || dockerDetails(t, repeated).ContainerID != dockerDetails(t, first).ContainerID {
		t.Fatalf("unchanged bootstrap did not reuse the builder: %v", err)
	}
	for _, replacement := range []string{strings.Repeat("cd", 32), ""} {
		fixture.Config.SecretBootstrap = nil
		if replacement != "" {
			fixture.Config.SecretBootstrap, err = runtimes.NewSecretBootstrap(replacement)
			if err != nil {
				t.Fatal(err)
			}
		}
		fixture.Manager = fixtures.RestartDockerManager(t, fixture)
		_, err := fixture.Manager.Spawn(ctx, requestID, fixtures.DockerBuilderReference)
		if err == nil || !strings.Contains(err.Error(), "secrets bootstrap") {
			t.Fatalf("changed or removed bootstrap was accepted: %v", err)
		}
		if strings.Contains(err.Error(), key) || replacement != "" && strings.Contains(err.Error(), replacement) {
			t.Fatal("bootstrap mismatch disclosed an encryption key")
		}
	}
	creates, starts, _ := fixture.Engine.Counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("bootstrap reconciliation relaunched builder: creates=%d starts=%d", creates, starts)
	}
}

func TestDockerManagerRefusesUncertainOrChangedContainer(t *testing.T) {
	cases := map[string]func(*fakes.DockerEngine){
		"missing": func(engine *fakes.DockerEngine) {
			engine.ForgetAll()
		},
		"exited": func(engine *fakes.DockerEngine) { engine.Change(func(c *docker.Container) { c.Status = "exited" }) },
		"unsafe": func(engine *fakes.DockerEngine) { engine.Change(func(c *docker.Container) { c.Safe = false }) },
		"changed image": func(engine *fakes.DockerEngine) {
			engine.Change(func(c *docker.Container) { c.Image = strings.Replace(c.Image, "aaaa", "bbbb", 1) })
		},
		"changed network": func(engine *fakes.DockerEngine) {
			engine.Change(func(c *docker.Container) { c.Networks = []string{"different-network"} })
		},
		"changed ownership": func(engine *fakes.DockerEngine) {
			engine.Change(func(c *docker.Container) { c.Labels = map[string]string{} })
		},
		"changed suffix": func(engine *fakes.DockerEngine) {
			engine.Change(func(c *docker.Container) { c.Environment["COMMANDER_AGENT_ID_SUFFIX"] = "invalid" })
		},
		"unexpected secrets key": func(engine *fakes.DockerEngine) {
			engine.Change(func(c *docker.Container) { c.Environment["SECRETS_ENCRYPTION_KEY"] = strings.Repeat("ab", 32) })
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := fixtures.NewDockerManager(t)
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err != nil {
				t.Fatal(err)
			}
			change(fixture.Engine)
			restarted := fixtures.RestartDockerManager(t, fixture)
			if _, err := restarted.Spawn(ctx, requestID, fixtures.Reference); err == nil {
				t.Fatal("manager trusted a missing, stopped, unsafe, or changed container")
			}
			creates, starts, _ := fixture.Engine.Counts()
			if creates != 1 || starts != 1 {
				t.Fatalf("uncertain request created or restarted a container: create=%d start=%d", creates, starts)
			}
		})
	}
}

func TestDockerManagerCancelledWaitRetainsLateRegistration(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	fixture.Engine.SetOnStart(nil)
	executionCtx, requestID, _ := fixtures.SpawnCheckpoints(t)
	ctx, cancel := context.WithCancel(executionCtx)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)
		finished <- err
	}()
	var spec docker.Spec
	select {
	case spec = <-fixture.Engine.Created():
	case <-time.After(2 * time.Second):
		t.Fatal("container was not created")
	}
	// Wait for Start, so cancellation exercises registration waiting rather than creation.
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		_, starts, _ := fixture.Engine.Counts()
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
	current, err := fixture.Engine.Inspect(t.Context(), spec.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixtures.PublishDockerRegistration(t.Context(), fixture.Bus, current, nil); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.Manager.Spawn(executionCtx, requestID, fixtures.Reference)

	if err != nil || dockerDetails(t, result).ContainerID != current.ID {
		t.Fatalf("retry missed late registration: result=%+v error=%v", result, err)
	}
	creates, starts, _ := fixture.Engine.Counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("cancelled wait caused duplicate launch: create=%d start=%d", creates, starts)
	}
}

func TestDockerManagerReconcilesUncertainCreateAndStart(t *testing.T) {
	for _, phase := range []string{"create", "start"} {
		t.Run(phase, func(t *testing.T) {
			fixture := fixtures.NewDockerManager(t)
			if phase == "create" {
				fixture.Engine.SetCreateError(errors.New("fixture lost Create reply after daemon accepted it"))
			} else {
				fixture.Engine.SetStartError(errors.New("fixture lost Start reply after daemon accepted it"))
			}
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			_, _ = fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

			result, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

			if err != nil || result.Runtime != runtimes.RuntimeDocker || dockerDetails(t, result).ContainerID == "" {
				t.Fatalf("retry did not reconcile accepted %s: result=%+v error=%v", phase, result, err)
			}
			creates, starts, _ := fixture.Engine.Counts()
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
			fixture := fixtures.NewDockerManager(t)
			fixture.Engine.SetOnStart(func(ctx context.Context, current docker.Container) error {
				return fixtures.PublishDockerRegistration(ctx, fixture.Bus, current, change)
			})
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			if result, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
				t.Fatalf("invalid registration was accepted: %+v", result)
			}
		})
	}
}

var _ runtimes.Runtime = (*docker.Manager)(nil)
