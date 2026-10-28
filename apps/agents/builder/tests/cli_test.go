package builder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	builder "github.com/defos-ai/commander/apps/agents/builder"
	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/cli"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

func TestBuilderConstruction(t *testing.T) {
	storage, err := fsartifacts.NewArtifactFileFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := agent.AgentConfig{
		Type: "ignored", ArtifactFileStore: storage,
		Server: agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"},
	}
	var previousID string
	for range 2 {
		instance, err := builder.NewAgent(config, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		snapshot := instance.Describe()
		if snapshot.Type != builder.AgentType || !id.ValidAgent(snapshot.ID) || !strings.HasPrefix(snapshot.ID, "builder-agent-") || snapshot.ID == previousID {
			t.Fatalf("incorrect builder identity: %s / %s", snapshot.ID, snapshot.Type)
		}
		previousID = snapshot.ID
		if len(snapshot.Actions) != 4 || snapshot.Actions[0].Name != builderactions.CheckoutTemplateName || snapshot.Actions[1].Name != builderactions.BuildAgentName || snapshot.Actions[2].Name != builderactions.PublishAgentName || snapshot.Actions[3].Name != builderactions.ReleaseAgentName {
			t.Fatalf("unexpected default catalog: %+v", snapshot.Actions)
		}
		if !slices.Equal(snapshot.Responsibilities, builder.Responsibilities()) {
			t.Fatal("published responsibilities differ from the registered builder")
		}
		descriptions := builder.ActionDescriptions()
		if len(descriptions) != len(snapshot.Actions) {
			t.Fatal("published action descriptions differ from the registered catalog")
		}
		for i, description := range descriptions {
			if description.Name != snapshot.Actions[i].Name || description.Description != snapshot.Actions[i].Description {
				t.Fatalf("published description differs from registered action %s", snapshot.Actions[i].Name)
			}
		}
	}
	config.Actions = []action.Executor{}
	empty, err := builder.NewAgent(config, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Describe().Actions) != 0 {
		t.Fatal("explicit empty catalog was replaced with defaults")
	}
	config.Actions = nil
	if _, err := builder.NewAgent(config, ""); err == nil {
		t.Fatal("default catalog accepted empty workspace configuration")
	}
	if _, err := builderactions.NewPublishAgent(nil, ""); err == nil {
		t.Fatal("publication accepted missing artifact storage")
	}
	if _, err := builderactions.NewPublishAgent(storage, "internal"); err == nil {
		t.Fatal("publication accepted invalid default visibility")
	}
	for _, visibility := range []string{"", "public", "private"} {
		executor, err := builderactions.NewPublishAgent(storage, visibility)
		if err != nil {
			t.Fatal(err)
		}
		assertPublicationVisibilitySchema(t, executor.Action(), visibility)
	}
}

func assertPublicationVisibilitySchema(t *testing.T, definition actionmodel.Action, visibility string) {
	t.Helper()
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Default string `json:"default"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(schema.Required, "visibility") != (visibility == "") || schema.Properties["visibility"].Default != visibility {
		t.Fatal("publication schema did not advertise the configured visibility choice")
	}
}

func TestCLIRegistrationAndCancellation(t *testing.T) {
	ids := map[string]bool{}
	for _, test := range []struct {
		name       string
		empty      bool
		key        string
		suffix     string
		visibility string
		arguments  []string
		wantID     string
	}{
		{name: "missing git"},
		{name: "private publication default", visibility: "private"},
		{name: "empty catalog with environment identity", empty: true, suffix: "a1b2c3d4", wantID: "builder-agent-a1b2c3d4"},
		{name: "malformed unused encryption key and flag identity", empty: true, key: "malformed-private-key",
			suffix: "invalid", arguments: []string{"--agent-id-suffix=b2c3d4e5"}, wantID: "builder-agent-b2c3d4e5"},
		{name: "missing encryption key and empty suffix flag", empty: true,
			suffix: "invalid", arguments: []string{"--agent-id-suffix="}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SECRETS_ENCRYPTION_KEY", test.key)
			if test.key == "" {
				if err := os.Unsetenv("SECRETS_ENCRYPTION_KEY"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("COMMANDER_AGENT_ID_SUFFIX", test.suffix)
			t.Setenv("BUILDER_DEFAULT_REPO_VISIBILITY", test.visibility)
			empty := test.empty
			root := t.TempDir()
			bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
			if err != nil {
				t.Fatal(err)
			}
			defer bus.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			subscription, err := bus.Subscribe(ctx, comms.Inbox{
				Topic: messages.ControllerEvents(messages.DefaultControllerType), Name: "builder-fixture", Mode: comms.TransientExclusive,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer subscription.Close()
			args := []string{
				"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"),
				"--workspace-dir=" + filepath.Join(root, "workspaces"), "--search-paths=", "--heartbeat-interval=0",
			}
			if empty {
				args = append(args, "--actions=")
			}
			args = append(args, test.arguments...)
			serving, stop := context.WithCancel(ctx)
			finished := make(chan struct{})
			var runErr error
			var logs bytes.Buffer
			go func() { defer close(finished); runErr = cli.Run(serving, args, &logs) }()
			defer func() {
				stop()
				select {
				case <-finished:
				case <-time.After(7 * time.Second):
					t.Error("builder host failed to stop")
				}
			}()
			snapshot := readSnapshot(t, ctx, subscription, messages.AgentStarted)
			if snapshot.Type != builder.AgentType || !strings.HasPrefix(snapshot.ID, "builder-agent-") || !id.ValidAgent(snapshot.ID) || ids[snapshot.ID] {
				t.Fatalf("registration must use a valid unique builder identity: %s / %s", snapshot.ID, snapshot.Type)
			}
			if test.wantID != "" && snapshot.ID != test.wantID {
				t.Fatalf("registration ID = %s, want %s", snapshot.ID, test.wantID)
			}
			ids[snapshot.ID] = true
			if snapshot.Status != agent.AgentReady || snapshot.DiscoveryScope == nil || snapshot.DiscoveryScope.Paths == nil || len(snapshot.DiscoveryScope.Paths) != 0 {
				t.Fatalf("incorrect registration readiness/scope: %+v", snapshot)
			}
			if empty {
				if len(snapshot.Actions) != 0 {
					t.Fatal("empty CLI selection exposed default actions")
				}
			} else {
				if len(snapshot.Actions) != 4 {
					t.Fatalf("incorrect catalog length: %d", len(snapshot.Actions))
				}
				definition := snapshot.Actions[0]
				if definition.Name != builderactions.CheckoutTemplateName || definition.Status != actionmodel.Unavailable || len(definition.MissingRequirements) != 1 || definition.MissingRequirements[0].Kind != actionmodel.ExecutableRequirement || definition.MissingRequirements[0].Name != "git" {
					t.Fatalf("missing git was not advertised: %+v", definition)
				}
				build := snapshot.Actions[1]
				if build.Name != builderactions.BuildAgentName || build.Status != actionmodel.Unavailable {
					t.Fatalf("missing build tools were not advertised: %+v", build)
				}
				publish := snapshot.Actions[2]
				if publish.Name != builderactions.PublishAgentName || publish.Status != actionmodel.Unavailable || !slices.ContainsFunc(publish.MissingRequirements, func(r actionmodel.Requirement) bool {
					return r.Kind == actionmodel.ExecutableRequirement && r.Name == "git"
				}) {
					t.Fatalf("missing publication Git was not advertised: %+v", publish)
				}
				assertPublicationVisibilitySchema(t, publish, test.visibility)
				release := snapshot.Actions[3]
				if release.Name != builderactions.ReleaseAgentName || release.Status != actionmodel.Unavailable || !slices.Equal(release.RequiredSecrets, []string{"GITHUB_TOKEN"}) || !slices.Equal(release.MissingRequirements, []actionmodel.Requirement{{Kind: actionmodel.SecretRequirement, Name: "GITHUB_TOKEN"}}) {
					t.Fatalf("release must depend only on its managed token: %+v", release)
				}
			}
			stop()
			select {
			case <-finished:
				if !errors.Is(runErr, context.Canceled) {
					t.Fatalf("unexpected host shutdown result: %v", runErr)
				}
			case <-ctx.Done():
				t.Fatal("host cancellation timed out")
			}
			stopped := readSnapshot(t, ctx, subscription, messages.AgentStopped)
			if stopped.ID != snapshot.ID || stopped.Status != agent.AgentStopped {
				t.Fatalf("incorrect shutdown announcement: %+v", stopped)
			}
			if value, present := os.LookupEnv("SECRETS_ENCRYPTION_KEY"); value != test.key || present != (test.key != "") {
				t.Fatal("builder changed the unused encryption-key environment")
			}
			if test.key != "" && strings.Contains(logs.String(), test.key) {
				t.Fatal("builder exposed the unused encryption key in diagnostics")
			}
		})
	}
}

func readSnapshot(t *testing.T, ctx context.Context, subscription comms.Subscription, kind messages.MessageType) agent.AgentSnapshot {
	t.Helper()
	for {
		delivery, err := subscription.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		message := delivery.Message()
		if message.Type == messages.SecretSyncRequested {
			if err := delivery.Ack(ctx); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if message.Type != kind {
			t.Fatalf("expected %s, received %s", kind, message.Type)
		}
		var snapshot agent.AgentSnapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		if err := delivery.Ack(ctx); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
}

func TestCLIValidatesBeforeOpeningProviders(t *testing.T) {
	t.Setenv("BUILDER_DEFAULT_REPO_VISIBILITY", "")
	for _, test := range []struct{ name, argument, suffix, expected string }{
		{"empty workspace", "--workspace-dir=", "", "workspace-dir"},
		{"unknown action", "--actions=unknown-action", "", "unknown action"},
		{"duplicate action", "--actions=checkout-template,checkout-template", "", "duplicate"},
		{"negative heartbeat", "--heartbeat-interval=-1s", "", "heartbeat-interval"},
		{"positional argument", "unexpected", "", "positional"},
		{"full ID", "--agent-id-suffix=builder-agent-a1b2c3d4", "", "agent-id-suffix"},
		{"invalid environment suffix", "", "invalid", "COMMANDER_AGENT_ID_SUFFIX"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("COMMANDER_AGENT_ID_SUFFIX", test.suffix)
			root := filepath.Join(t.TempDir(), "unused")
			args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--workspace-dir=" + filepath.Join(root, "workspaces")}
			if test.argument != "" {
				args = append(args, test.argument)
			}
			err := cli.Run(t.Context(), args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("invalid configuration was not explained: %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid configuration created runtime state")
			}
		})
	}
	t.Run("invalid default repository visibility", func(t *testing.T) {
		t.Setenv("BUILDER_DEFAULT_REPO_VISIBILITY", "internal")
		root := filepath.Join(t.TempDir(), "unused")
		err := cli.Run(t.Context(), []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--workspace-dir=" + filepath.Join(root, "workspaces")}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "BUILDER_DEFAULT_REPO_VISIBILITY") {
			t.Fatalf("invalid publication default was not explained: %v", err)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid publication default opened providers")
		}
	})
	t.Run("pre-cancelled", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "unused")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := cli.Run(ctx, []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--workspace-dir=" + filepath.Join(root, "workspaces")}, io.Discard)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-cancelled startup returned %v", err)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("pre-cancelled startup created runtime state")
		}
	})
}

func TestCLIWorkspaceEnvironment(t *testing.T) {
	t.Setenv("BUILDER_WORKSPACE_DIR", " ")
	root := t.TempDir()
	args := []string{"--bus-url=nats://127.0.0.1:4222", "--storage-url=" + filepath.Join(root, "storage")}
	if err := cli.Run(t.Context(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "workspace-dir") {
		t.Fatalf("workspace environment setting was not validated: %v", err)
	}
	args = append(args, "--workspace-dir="+filepath.Join(root, "workspaces"))
	if err := cli.Run(t.Context(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "unsupported bus scheme") {
		t.Fatalf("explicit workspace flag did not override the environment: %v", err)
	}
}

func TestCLIRejectsUnavailableProviders(t *testing.T) {
	const secret = "provider-test-secret"
	for _, test := range []struct {
		name, flag, url string
		dependency      string
		invalidConfig   bool
		filesystemForm  string
	}{
		{name: "RabbitMQ bus unavailable", flag: "bus-url", url: "amqp://user:" + secret + "@127.0.0.1:1", dependency: "rabbitmq"},
		{name: "RabbitMQ TLS bus unavailable", flag: "bus-url", url: "amqps://user:" + secret + "@127.0.0.1:1", dependency: "rabbitmq"},
		{name: "unsupported broker", flag: "bus-url", url: "nats://user:" + secret + "@127.0.0.1:4222"},
		{name: "incomplete S3 credentials", flag: "storage-url", url: "s3://:" + secret + "@artifacts/bucket", invalidConfig: true},
		{name: "uppercase FILE URL", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "FILE"},
		{name: "uppercase FS URL", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "FS"},
		{name: "absolute path containing colon", flag: "bus-url", url: "nats://127.0.0.1:4222", filesystemForm: "path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.filesystemForm == "path" && runtime.GOOS == "windows" {
				t.Skip("Windows does not support colons in filenames")
			}
			root := t.TempDir()
			fileURL := func(name string) string {
				if test.filesystemForm == "path" {
					return filepath.Join(root, name+":1")
				}
				scheme := test.filesystemForm
				if scheme == "" {
					scheme = "file"
				}
				return (&url.URL{Scheme: scheme, Path: filepath.Join(root, name)}).String()
			}
			args := []string{
				"--bus-url=" + fileURL("bus"),
				"--storage-url=" + fileURL("storage"),
				"--log-level=info", "--log-format=json",
				"--workspace-dir=" + filepath.Join(root, "workspaces"),
				"--" + test.flag + "=" + test.url,
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var logs bytes.Buffer

			err := cli.Run(ctx, args, &logs)

			if ctx.Err() != nil {
				t.Fatalf("startup did not report the unavailable dependency before its deadline: %v", err)
			}
			if err == nil {
				t.Fatal("startup accepted an unavailable provider")
			}
			message := strings.ToLower(err.Error())
			if !strings.Contains(message, test.flag) {
				t.Errorf("error does not identify %s: %v", test.flag, err)
			}
			if test.invalidConfig {
				if !strings.Contains(message, "credentials") && !strings.Contains(message, "userinfo") {
					t.Errorf("startup did not explain invalid S3 configuration: %v", err)
				}
			} else if test.dependency != "" {
				if !strings.Contains(message, test.dependency) || !strings.Contains(message, "connect failed") {
					t.Errorf("startup must identify the failed %s connection: %v", test.dependency, err)
				}
				var connectionErr *net.OpError
				if !errors.As(err, &connectionErr) {
					t.Errorf("startup must preserve the underlying connection error: %v", err)
				}
			} else if !strings.Contains(message, "unsupported") {
				t.Errorf("startup did not explain the unavailable provider: %v", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(logs.String(), secret) {
				t.Error("startup exposed credentials from the connection URL")
			}
		})
	}
}

func TestCLIHelpDoesNotOpenProviders(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unused")
	var output bytes.Buffer
	args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--help"}

	err := cli.Run(t.Context(), args, &output)

	if err != nil || !strings.Contains(output.String(), "Usage:") || !strings.Contains(output.String(), "managed credentials") {
		t.Fatalf("help did not describe managed credentials: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("help created runtime state")
	}
}
